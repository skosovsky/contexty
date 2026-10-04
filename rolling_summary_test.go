package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestRolling_Summary(t *testing.T) {
	// Arrange: keep two exact recent messages and compact only the preceding prefix.
	messages := fixtureProtectedHistory()[:3]
	messages = append(messages, contexty.Message{ID: "tail-a", Role: contexty.RoleUser},
		contexty.Message{ID: "tail-b", Role: contexty.RoleAssistant})
	calls := 0
	summarizer := stubSummarizer(func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
		inputs := request.Messages
		calls++
		require.Equal(t, messages[:3], inputs)
		return contexty.Message{ID: "summary", Role: contexty.RoleSystem}, nil
	})
	policy := fixtureRollingPolicy(2)
	pipeline := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(15), Summarizer: summarizer},
		&contexty.FixedEstimator{TokensPerMessage: 5},
		contexty.WithRollingSummary(policy),
	)
	policy.RecentMessages = 100 // Option freezes caller-owned configuration.
	// Act.
	outBudget, err := pipeline.Apply(context.Background(), messages)
	out := outBudget.Messages
	// Assert.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Len(t, out, 3)
	require.Equal(t, "summary", out[0].ID)
	require.Equal(t, messages[3:], out[1:])
	// Arrange/Act/Assert: already fitting input does not trigger compaction.
	outBudget, err = pipeline.Apply(context.Background(), messages[3:])
	out = outBudget.Messages
	require.NoError(t, err)
	require.Equal(t, messages[3:], out)
	require.Equal(t, 1, calls)
}

func TestRolling_SummaryRoundBoundary(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending-before-tail", true: "complete-boundary"}[complete], func(t *testing.T) {
			// Arrange: requested tail begins inside a complete round or after pending.
			messages := fixtureProtectedHistory()
			recent := 1
			if complete {
				messages = append(messages, contexty.Message{ID: "second-result", Role: contexty.RoleTool,
					Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "second"}}})
				recent = 2
			} else {
				messages = append(messages, contexty.Message{ID: "additional-tail", Role: contexty.RoleUser})
			}
			messages = append(messages, contexty.Message{ID: "last", Role: contexty.RoleUser})
			calls := 0
			pipeline := contexty.NewBudgetPipeline(contexty.BudgetConfig{
				Budget: contexty.EffectiveInputBudget(25),
				Summarizer: stubSummarizer(
					func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
						inputs := request.Messages
						calls++
						require.Equal(t, messages[:3], inputs)
						return contexty.Message{ID: "summary", Role: contexty.RoleSystem}, nil
					},
				),
			}, &contexty.FixedEstimator{TokensPerMessage: 5}, contexty.WithRollingSummary(fixtureRollingPolicy(recent)))
			// Act.
			outBudget, err := pipeline.Apply(context.Background(), messages)
			out := outBudget.Messages
			// Assert: all four tail messages survive, regardless of requested minimum.
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Equal(t, messages[3:], out[1:])
			rounds, err := contexty.InspectToolRoundStates(out, nil)
			require.NoError(t, err)
			require.Equal(
				t,
				map[bool]contexty.ToolRoundState{false: contexty.ToolRoundPending, true: contexty.ToolRoundComplete}[complete],
				rounds[0].State,
			)
		})
	}
}

func TestRolling_SummaryFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		recent  int
		budget  int
		summary bool
		want    error
	}{
		{name: "zero recent", recent: 0, budget: 15, summary: true, want: contexty.ErrInvalidRollingSummary},
		{name: "negative recent", recent: -1, budget: 15, summary: true, want: contexty.ErrInvalidRollingSummary},
		{name: "missing summarizer", recent: 2, budget: 15, want: contexty.ErrInvalidRollingSummary},
		{name: "oversized tail", recent: 2, budget: 9, summary: true, want: contexty.ErrRecentTailExceedsBudget},
		{name: "no prefix capacity", recent: 2, budget: 10, summary: true, want: contexty.ErrBudgetExceeded},
		{name: "whole history protected", recent: int(^uint(0) >> 1), budget: 15, summary: true, want: contexty.ErrRecentTailExceedsBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: an explicit drop strategy must not override a rolling recipe.
			messages := fixtureProtectedHistory()[:3]
			messages = append(messages, contexty.Message{ID: "tail-a", Role: contexty.RoleUser},
				contexty.Message{ID: "tail-b", Role: contexty.RoleAssistant})
			calls := 0
			var summarizer contexty.Summarizer
			if tc.summary {
				summarizer = stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
					calls++
					return contexty.Message{ID: "summary", Role: contexty.RoleSystem}, nil
				})
			}
			pipeline := contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(tc.budget),
					Summarizer: summarizer, TruncateStrategy: contexty.NewDropStrategy()},
				&contexty.FixedEstimator{
					TokensPerMessage: 5,
				},
				contexty.WithRollingSummary(fixtureRollingPolicy(tc.recent)),
			)
			// Act.
			outBudget, err := pipeline.Apply(context.Background(), messages)
			out := outBudget.Messages
			// Assert: no partial output or implicit deletion fallback.
			require.ErrorIs(t, err, tc.want)
			require.Nil(t, out)
			if tc.name != "no prefix capacity" {
				require.Zero(t, calls)
			}
		})
	}
}

func TestRolling_SummaryIdentity(t *testing.T) {
	// Arrange: a summary reuses a preserved message identity.
	messages := fixtureProtectedHistory()[:3]
	pipeline := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10),
		Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
			return contexty.Message{ID: "keep", Role: contexty.RoleSystem}, nil
		})}, &contexty.FixedEstimator{TokensPerMessage: 5}, contexty.WithRollingSummary(fixtureRollingPolicy(1)))
	// Act.
	outBudget, err := pipeline.Apply(context.Background(), messages)
	out := outBudget.Messages
	// Assert.
	require.ErrorIs(t, err, contexty.ErrInvalidRollingSummary)
	require.Nil(t, out)
}

func TestRolling_SummaryConfiguration(t *testing.T) {
	// Arrange: invalid target recipe must fail before any engine callback executes.
	calls := 0
	hook := contexty.RedactionHook{Replacer: func(text string) string { calls++; return text }}
	policy := fixtureRollingPolicy(1)
	policy.Descriptor = contexty.Descriptor{}
	pipeline := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10),
		Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
			calls++
			return contexty.Message{ID: "summary", Role: contexty.RoleSystem}, nil
		})}, &contexty.FixedEstimator{TokensPerMessage: 5}, contexty.WithRollingSummary(policy))
	engine := contexty.NewEngine(contexty.WithTransformHooks(hook))
	// Act.
	_, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{
			History: fixtureProtectedHistory()[:3],
			Targets: []contexty.CompileTarget{
				{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "invalid", Budget: pipeline},
			},
		},
	)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrInvalidDescriptor)
	require.Zero(t, calls)
	// Arrange: compaction capture pins a policy inconsistent with the selected recipe.
	profile := fixtureCompactionFixture(t).Profile
	pipeline = contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10),
		Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
			calls++
			return contexty.Message{ID: "summary", Role: contexty.RoleSystem}, nil
		})}, contexty.CharTokenEstimator{}, contexty.WithRollingSummary(fixtureRollingPolicy(1)), contexty.WithCompactionCapture(profile))
	// Act.
	outBudget, err := pipeline.Apply(context.Background(), fixtureProtectedHistory()[:3])
	out := outBudget.Messages
	// Assert.
	require.ErrorIs(t, err, contexty.ErrInvalidCompaction)
	require.Nil(t, out)
	require.Zero(t, calls)
}
