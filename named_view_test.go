package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestNamedView_CallbackCancellation(t *testing.T) {
	for _, role := range []bool{false, true} {
		t.Run(map[bool]string{false: "formatter", true: "role"}[role], func(t *testing.T) {
			// Arrange: named read-only views obey the same cancellation boundaries.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			formatterCalls := 0
			formatter := func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				formatterCalls++
				cancel()
				return messages, contexty.ErrInvalidDescriptor
			}
			options := []contexty.EngineOption{
				contexty.WithNamedView("named", contexty.ViewConfiguration{Formatter: formatter}),
			}
			if role {
				options = append(options, contexty.WithRoleProjectionPolicy(contexty.RoleProjectionFunc(
					func(contexty.Message) (contexty.Role, error) {
						cancel()
						return contexty.RoleUser, contexty.ErrInvalidDescriptor
					})))
			}
			snapshot := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory,
				[]contexty.Message{fixtureRollingText("input", "safe")})
			// Act.
			text, err := contexty.NewEngine(options...).RenderView(ctx, snapshot, "named")
			// Assert.
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, text)
			if role {
				require.Zero(t, formatterCalls)
			}
		})
	}
}

func TestNamedView_RoleOwnership(t *testing.T) {
	// Arrange: a role policy tries to modify content, but only its role result counts.
	engine := contexty.NewEngine(
		contexty.WithNamedView("named", contexty.ViewConfiguration{}),
		contexty.WithRoleProjectionPolicy(
			contexty.RoleProjectionFunc(func(message contexty.Message) (contexty.Role, error) {
				message.Parts[0] = contexty.TextPart{Text: "mutated"}
				return contexty.RoleAssistant, nil
			}),
		),
	)
	snapshot := contexty.EmptySnapshot().
		WithSegment(contexty.SegmentHistory, []contexty.Message{fixtureRollingText("input", "safe")})
	// Act.
	text, err := engine.RenderView(context.Background(), snapshot, "named")
	// Assert.
	require.NoError(t, err)
	require.Equal(t, "safe", text)
	require.Equal(t, "safe", snapshot.Segment(contexty.SegmentHistory)[0].TextContent())
}

func TestNamedView_FinalBudget(t *testing.T) {
	// Arrange: formatting expands a named view after initial budget admission.
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(4)},
		contexty.CharTokenEstimator{},
	)
	engine := contexty.NewEngine(contexty.WithNamedView("named", contexty.ViewConfiguration{Budget: pipe,
		Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
			messages[0].Parts[0] = contexty.TextPart{Text: "expanded beyond budget"}
			return messages, nil
		}}))
	snapshot := contexty.EmptySnapshot().
		WithSegment(contexty.SegmentHistory, []contexty.Message{fixtureRollingText("input", "safe")})
	// Act.
	text, err := engine.RenderView(context.Background(), snapshot, "named")
	// Assert: no oversized projection escapes, and input remains unchanged.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Empty(t, text)
	require.Equal(t, "safe", snapshot.Segment(contexty.SegmentHistory)[0].TextContent())
}
