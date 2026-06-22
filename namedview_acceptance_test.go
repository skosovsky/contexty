package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_NamedView_WithBudget(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:   contexty.EffectiveInputBudget(30),
			DropHead: contexty.DropHeadConfig{MinMessages: 1},
		},
		&contexty.FixedEstimator{TokensPerMessage: 15},
	)
	engine := contexty.NewEngine(
		contexty.WithNamedView("classifier", contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Budget:        pipe,
		}),
	)
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "one"),
		contexty.TextMessage(contexty.RoleUser, "two"),
		contexty.TextMessage(contexty.RoleUser, "three"),
	})
	out, err := engine.RenderView(ctx, snap, "classifier")
	require.NoError(t, err)
	assert.NotContains(t, out, "one")
	// Act / Assert: exercise the contract and check its result.
	assert.Contains(t, out, "three")
}

func TestAcceptance_NamedView_FormatterInjected(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	// Act.
	engine := contexty.NewEngine(
		contexty.WithNamedView("wrapped", contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Formatter: func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				out := make([]contexty.Message, len(msgs))
				for i, m := range msgs {
					cloned := m.Clone()
					cloned.Parts = []contexty.ContentPart{
						contexty.TextPart{Text: "WRAP:" + m.TextContent()},
					}
					out[i] = cloned
				}
				return out, nil
			},
		}),
	)
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "x"),
	})
	out, err := engine.RenderView(ctx, snap, "wrapped")
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "WRAP:x", out)
}

func TestAcceptance_NamedView_FormatterReceivesContextAndCanFail(t *testing.T) {
	// Arrange.
	t.Parallel()
	expectedErr := errors.New("view formatter failed")
	ctx := context.WithValue(context.Background(), formatterContextKey{}, "view-trace")
	engine := contexty.NewEngine(
		contexty.WithNamedView("ctx-view", contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Formatter: func(ctx context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				assert.Equal(t, "view-trace", ctx.Value(formatterContextKey{}))
				return msgs, expectedErr
			},
		}),
	)
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hello"),
	})

	_, err := engine.RenderView(ctx, snap, "ctx-view")
	require.Error(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.ErrorIs(t, err, expectedErr)
}
