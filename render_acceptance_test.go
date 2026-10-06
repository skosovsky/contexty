package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Render_ViewNonMutating(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:   contexty.EffectiveInputBudget(20),
			DropHead: contexty.DropHeadConfig{MinMessages: 1},
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := fixtureEngine(
		contexty.WithNamedView("trim", contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Budget:        pipe,
		}),
	)
	before := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "keep-me-1"),
		contexty.TextMessage(contexty.RoleUser, "keep-me-2"),
	}
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, cloneTestMsgs(before))
	_, err := engine.RenderView(ctx, snap, "trim")
	require.NoError(t, err)
	after := snap.Segment(contexty.SegmentHistory)
	// Act / Assert: exercise the contract and check its result.
	require.True(t, contexty.MessagesEqual(before, after))
}

func TestAcceptance_RenderView_BuiltinParity(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	snap := contexty.EmptySnapshot().
		WithSegment(contexty.SegmentSystem, []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "sys"),
		}).
		WithSegment(contexty.SegmentHistory, []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "hello"),
		}).
		WithSegment(contexty.SegmentMemory, []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "mem"),
		}).
		WithSegment(contexty.SegmentTools, []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "tool-a"),
			contexty.TextMessage(contexty.RoleUser, "tool-b"),
		})
	engine := fixtureEngine()
	// Act / Assert: exercise the contract and check its result.
	for _, name := range []string{string(contexty.ViewLLMXML), string(contexty.ViewFlatClassifier)} {
		viewName := name
		t.Run(viewName, func(t *testing.T) {
			t.Parallel()
			viaRender, err := contexty.Render(ctx, snap, contexty.ViewType(viewName))
			require.NoError(t, err)
			viaRenderView, err := engine.RenderView(ctx, snap, viewName)
			require.NoError(t, err)
			assert.Equal(t, viaRender, viaRenderView)
		})
	}
}

func TestAcceptance_RenderView_BuiltinCollisionRejected(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithNamedView(string(contexty.ViewLLMXML), contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Formatter: func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				out := make([]contexty.Message, len(msgs))
				for i := range msgs {
					out[i] = contexty.TextMessage(contexty.RoleUser, "OVERRIDDEN")
				}
				return out, nil
			},
		}),
	)
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hello"),
	})
	out, err := engine.RenderView(ctx, snap, string(contexty.ViewLLMXML))
	// Assert: registration cannot silently lose to builtin dispatch.
	require.ErrorIs(t, err, contexty.ErrInvalidCompileConfiguration)
	require.Empty(t, out)
}
