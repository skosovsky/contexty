package contexty_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Truncation_Atomicity(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{ID: "a", Name: "fn", Arguments: contexty.JSONPayload("{}")},
			},
		},
		{
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "a", Payload: contexty.TextPayload("r")},
			},
		},
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget: contexty.EffectiveInputBudget(12),
	}, &contexty.FixedEstimator{TokensPerMessage: 5})
	// Act.
	outBudget, err := pipe.Apply(ctx, msgs)
	out := outBudget.Messages
	// Assert.
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "new", out[0].TextContent())
	for _, m := range out {
		assert.NotEqual(t, contexty.RoleAssistant, m.Role)
		assert.NotEqual(t, contexty.RoleTool, m.Role)
	}
}

func TestAcceptance_Budget_PreflightReservesPendingAndSystem(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{
					Budget:   contexty.EffectiveInputBudget(40),
					DropHead: contexty.DropHeadConfig{MinMessages: 1},
				},
				&contexty.FixedEstimator{TokensPerMessage: 10},
			),
		),
	)
	// reserved: system(10) + pending(10) = 20, available history = 20 -> at most 2 history msgs survive (+ pending merged after)
	pendingMsg := contexty.Message{
		ID: "p", Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "pend"}},
	}
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{contexty.TextMessage(contexty.RoleSystem, "sys")},
		History: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "h1"),
			contexty.TextMessage(contexty.RoleUser, "h2"),
			contexty.TextMessage(contexty.RoleUser, "h3"),
		},
		Pending: []contexty.Message{pendingMsg},
	})
	// Assert.
	require.NoError(t, err)
	// 2 history trimmed + 1 pending = 3 in payload history
	require.Len(t, result.Payload.History, 3)
	assert.Equal(t, "p", result.Payload.History[2].ID)
}

func TestAcceptance_BudgetSummary_IdentityIgnoresObserverState(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	segments := make([]contexty.SegmentName, 0, 2)
	policy := contexty.MessageIdentityFunc(
		func(idCtx contexty.MessageIdentityContext, msg contexty.Message) (string, error) {
			segments = append(segments, idCtx.Segment)
			return fmt.Sprintf("id:%s:%d:%s", idCtx.Segment, idCtx.Index, msg.TextContent()), nil
		},
	)
	summarizer := fixtureSummarizer{summary: contexty.TextMessage(contexty.RoleAssistant, "summary")}
	req := contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "h1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "one"}},
			},
			{
				ID:    "h2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "two"}},
			},
		},
		IdentityPolicy:         policy,
		RequireDurableIdentity: true,
	}
	pipeWithoutObserver := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1), Summarizer: summarizer},
		&contexty.FixedEstimator{TokensPerMessage: 1},
	)
	// Act.
	withoutObserver, err := fixtureEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipeWithoutObserver),
	).CompileSnapshot(ctx, req)
	// Assert.
	require.NoError(t, err)

	pipeWithObserver := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1), Summarizer: summarizer},
		&contexty.FixedEstimator{TokensPerMessage: 1},
		contexty.WithBudgetObserver(fixtureObserver{}),
	)
	withObserver, err := fixtureEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipeWithObserver),
	).CompileSnapshot(ctx, req)
	require.NoError(t, err)

	require.Len(t, withoutObserver.Payload.History, 1)
	require.Len(t, withObserver.Payload.History, 1)
	assert.Equal(t, withoutObserver.Payload.History[0].ID, withObserver.Payload.History[0].ID)
	assert.Contains(t, segments, contexty.SegmentHistory)
}
