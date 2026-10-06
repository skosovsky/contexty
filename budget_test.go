package contexty_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestFinal_Budget(t *testing.T) {
	// Arrange: a post-budget patch expands otherwise valid history.
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)},
		contexty.CharTokenEstimator{},
	)
	engine := fixtureEngine(contexty.WithBudgetPipeline(pipe))
	input := contexty.TextMessage(contexty.RoleUser, "x")
	input.ID = "h"
	req := contexty.CompileRequest{
		History: []contexty.Message{input},
		Options: []contexty.CompileOption{contexty.WithTextReplacement(contexty.TextReplacement{
			Segment: contexty.SegmentHistory, MessageID: "h", Text: strings.Repeat("x", 100),
		})},
	}
	// Act.
	_, err := engine.CompileSnapshot(ctx, req)
	// Assert: no successful payload above the hard limit.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Equal(t, "x", req.History[0].TextContent())
}

func TestTarget_FinalBudget(t *testing.T) {
	// Arrange: only the first target expands its output after its budget pass.
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)},
		contexty.CharTokenEstimator{},
	)
	formatter := func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
		msgs[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: strings.Repeat("x", 100)}}
		return msgs, nil
	}
	req := contexty.CompileRequest{
		History: []contexty.Message{contexty.TextMessage(contexty.RoleUser, "x")},
		Targets: []contexty.CompileTarget{
			{
				Segments:  []contexty.SegmentName{contexty.SegmentHistory},
				Name:      "small",
				Budget:    pipe,
				Formatter: formatter,
			},
		},
	}
	engine := fixtureEngine()
	// Act.
	_, err := engine.CompileSnapshot(ctx, req)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Equal(t, "x", req.History[0].TextContent())

	// Arrange: the same formatter is harmless within the target's budget.
	req.Targets[0].Formatter = func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
		msgs[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "safe"}}
		return msgs, nil
	}
	req.Targets = append(
		req.Targets,
		contexty.CompileTarget{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "other"},
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, req)
	// Assert: target-local output doesn't change main or other output.
	require.NoError(t, err)
	require.Equal(t, "safe", result.Projections["small"].Messages[0].TextContent())
	require.Equal(t, "x", result.Projections["other"].Messages[0].TextContent())
	require.Equal(t, "x", result.Payload.History[0].TextContent())
}

func TestFinal_BudgetPersistence(t *testing.T) {
	// Arrange: an accepted compile-only patch has a separate persistence projection.
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)},
		contexty.CharTokenEstimator{},
	)
	engine := fixtureEngine(contexty.WithBudgetPipeline(pipe))
	input := contexty.TextMessage(contexty.RoleUser, "raw")
	input.ID = "h"
	req := contexty.CompileRequest{
		History: []contexty.Message{input},
		Options: []contexty.CompileOption{contexty.WithTextReplacement(contexty.TextReplacement{
			Segment: contexty.SegmentHistory, MessageID: "h", Text: "safe",
		})},
	}
	// Act.
	result, err := engine.CompileSnapshot(ctx, req)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, "safe", result.Payload.History[0].TextContent())
	require.Equal(t, "raw", fixturePersistenceSegment(t, result, contexty.SegmentHistory)[0].TextContent())

	// Arrange: pending is protected even when larger than the limit.
	req.Pending = []contexty.Message{contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 11))}
	// Act.
	_, err = engine.CompileSnapshot(ctx, req)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrPendingExceedsBudget)
	require.Equal(t, strings.Repeat("x", 11), req.Pending[0].TextContent())

	// Arrange: protected current turn exceeds the limit; Raw must survive intact.
	req.Pending = nil
	turn := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "private raw input"))
	turn = turn.WithPromptSafe(contexty.TextMessage(contexty.RoleUser, strings.Repeat("x", 11)))
	req.CurrentTurn = &turn
	// Act.
	_, err = engine.CompileSnapshot(ctx, req)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrPendingExceedsBudget)
	require.Equal(t, "private raw input", turn.Raw.TextContent())
}
