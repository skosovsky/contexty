package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestDropHeadStrategy_ToolTurnAtomicity(t *testing.T) {
	ctx := context.Background()
	estimator := &contexty.FixedEstimator{TokensPerMessage: 5}
	strategy := contexty.NewDropHeadStrategy(contexty.DropHeadConfig{MinMessages: 1})
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.TextPart{Text: "call"},
				contexty.ToolCallPart{
					ID:        "call_a",
					Name:      "f",
					Arguments: contexty.JSONPayload("{}"),
				},
				contexty.ToolCallPart{
					ID:        "call_b",
					Name:      "f",
					Arguments: contexty.JSONPayload("{}"),
				},
			},
		},
		{
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "call_a", Payload: contexty.TextPayload("r1")},
			},
		},
		{
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "call_b", Payload: contexty.TextPayload("r2")},
			},
		},
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	out, err := strategy.Apply(ctx, msgs, 30, 15, estimator)
	require.NoError(t, err)
	require.NotEmpty(t, out)
	for _, m := range out {
		if m.Role == contexty.RoleTool {
			for _, p := range m.ToolResultParts() {
				assert.Contains(t, []string{"call_a", "call_b"}, p.ToolCallID)
			}
		}
	}
}

func TestDropTailStrategy_ToolTurnAtomicity(t *testing.T) {
	ctx := context.Background()
	estimator := &contexty.FixedEstimator{TokensPerMessage: 5}
	strategy := contexty.NewDropTailStrategy()
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "keep"),
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{ID: "c1", Name: "f", Arguments: contexty.JSONPayload("{}")},
			},
		},
		{
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "c1", Payload: contexty.TextPayload("r")},
			},
		},
	}
	out, err := strategy.Apply(ctx, msgs, 30, 5, estimator)
	require.NoError(t, err)
	if len(out) > 0 && out[len(out)-1].Role == contexty.RoleTool {
		t.Fatalf("orphan tool result at tail: %+v", out)
	}
}

func TestBudgetPipeline_Summarize(t *testing.T) {
	ctx := context.Background()
	msgs := []contexty.Message{contexty.TextMessage(contexty.RoleUser, "long message")}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		TokenLimit: 10,
		Summarizer: stubSummarizer(
			func(context.Context, []contexty.Message) (contexty.Message, error) {
				return contexty.TextMessage(contexty.RoleSystem, "compressed"), nil
			},
		),
	}, contexty.CharTokenEstimator{})
	out, err := pipe.Apply(ctx, msgs)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "compressed", out[0].TextContent())
}

func TestDropHeadStrategy_AtomicityOptOut(t *testing.T) {
	ctx := context.Background()
	optOut := false
	strategy := contexty.NewDropHeadStrategy(contexty.DropHeadConfig{
		KeepTurnAtomicity: &optOut,
	})
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
	out, err := strategy.Apply(ctx, msgs, 20, 12, &contexty.FixedEstimator{TokensPerMessage: 5})
	require.NoError(t, err)
	require.NotEmpty(t, out)
	hasOrphanTool := false
	for i, m := range out {
		if m.Role != contexty.RoleTool {
			continue
		}
		if i == 0 || out[i-1].Role != contexty.RoleAssistant || !out[i-1].HasToolCalls() {
			hasOrphanTool = true
		}
	}
	assert.True(t, hasOrphanTool, "opt-out fast path may split tool turns at strategy level")
}

func TestBudgetPipeline_RepairsStrategyOrphans(t *testing.T) {
	ctx := context.Background()
	optOut := false
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
		TokenLimit: 12,
		TruncateStrategy: contexty.NewDropHeadStrategy(contexty.DropHeadConfig{
			KeepTurnAtomicity: &optOut,
		}),
	}, &contexty.FixedEstimator{TokensPerMessage: 5})
	out, err := pipe.Apply(ctx, msgs)
	require.NoError(t, err)
	for i, m := range out {
		if m.Role == contexty.RoleTool && (i == 0 || out[i-1].Role != contexty.RoleAssistant) {
			t.Fatalf("pipeline left orphan tool result at index %d", i)
		}
	}
}

func TestDropHeadStrategy_FastPathEmitsObserverEvictions(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	ctx = contexty.WithBudgetObservationForTest(ctx, rec, "history")

	optOut := false
	strategy := contexty.NewDropHeadStrategy(contexty.DropHeadConfig{
		KeepTurnAtomicity: &optOut,
		MinMessages:       1,
	})
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		contexty.TextMessage(contexty.RoleUser, "mid"),
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	out, err := strategy.Apply(ctx, msgs, 30, 10, &contexty.FixedEstimator{TokensPerMessage: 10})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, rec.Evictions, 2)
	for _, e := range rec.Evictions {
		assert.Equal(t, contexty.EvictionReasonTruncate, e.Reason)
	}
}

func TestDropHeadStrategy_SelectivePathEmitsObserverEvictions(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	ctx = contexty.WithBudgetObservationForTest(ctx, rec, "history")

	strategy := contexty.NewDropHeadStrategy(contexty.DropHeadConfig{MinMessages: 1})
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{ID: "c1", Name: "fn", Arguments: contexty.JSONPayload("{}")},
			},
		},
		{
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "c1", Payload: contexty.TextPayload("r")},
			},
		},
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	out, err := strategy.Apply(ctx, msgs, 40, 15, &contexty.FixedEstimator{TokensPerMessage: 10})
	require.NoError(t, err)
	require.NotEmpty(t, out)
	require.GreaterOrEqual(t, len(rec.Evictions), 2)
}
