package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureSelectionMessages() []contexty.Message {
	return []contexty.Message{
		fixtureRollingText("old", "old"),
		fixtureRollingText("middle", "middle"),
		fixtureRollingText("new", "new"),
	}
}

func TestAcceptance_SelectionPriorityAndChronology(t *testing.T) {
	t.Parallel()
	// Arrange: two units fit; equal priorities use preparation ordinal, not plan order.
	messages := fixtureSelectionMessages()
	calls := 0
	policy := contexty.SelectionPolicy{
		Identity: contexty.Descriptor{ID: "host/priorities", Revision: "1"},
		Select: func(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
			calls++
			candidates[0].Messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "MUTATED"}}
			return []contexty.SelectionChoice{
				{Ref: candidates[2].Ref, Priority: 10},
				{Ref: candidates[1].Ref, Priority: 10},
				{Ref: candidates[0].Ref, Priority: 20},
			}, nil
		},
	}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(2)},
		&contexty.FixedEstimator{TokensPerMessage: 1},
	)
	engine := fixtureEngine(
		contexty.WithSelectionPolicy(policy),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	// Act.
	result, err := engine.CompileSnapshot(t.Context(), contexty.CompileRequest{History: messages})
	// Assert.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, []string{"old", "middle"}, fixtureRollingMessageIDs(result.Payload.History))
	require.Equal(t, "old", result.Payload.History[0].TextContent())
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, result.Selection.Candidates[2].Reason)
	require.Equal(t, messages, result.Source.History)
}

func TestAcceptance_SelectionRejectsInvalidPlans(t *testing.T) {
	scenarios := []struct {
		name   string
		choose func([]contexty.ContextCandidate) []contexty.SelectionChoice
		want   error
	}{
		{name: "missing", choose: func([]contexty.ContextCandidate) []contexty.SelectionChoice {
			return []contexty.SelectionChoice{{Ref: contexty.ContentRef{ID: "absent", Digest: "unknown"}}}
		}, want: contexty.ErrUnavailableCandidate},
		{name: "stale", choose: func(c []contexty.ContextCandidate) []contexty.SelectionChoice {
			ref := c[0].Ref
			ref.Digest = "stale"
			return []contexty.SelectionChoice{{Ref: ref}}
		}, want: contexty.ErrUnavailableCandidate},
		{name: "duplicate", choose: func(c []contexty.ContextCandidate) []contexty.SelectionChoice {
			return []contexty.SelectionChoice{{Ref: c[0].Ref}, {Ref: c[0].Ref}}
		}, want: contexty.ErrDuplicateSelection},
		{
			name:   "mandatory",
			choose: func([]contexty.ContextCandidate) []contexty.SelectionChoice { return nil },
			want:   contexty.ErrMandatorySelection,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: current turn is a mandatory prompt-safe unit.
			raw := fixtureRollingText("turn", "PRIVATE")
			turn := contexty.NewCurrentTurn(raw).WithPromptSafe(fixtureRollingText("turn", "safe"))
			policy := contexty.SelectionPolicy{
				Identity: contexty.Descriptor{ID: "host/select", Revision: "1"},
				Select: func(_ context.Context, c []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
					return scenario.choose(c), nil
				},
			}
			// Act.
			result, err := fixtureEngine(contexty.WithSelectionPolicy(policy)).
				CompileSnapshot(t.Context(), contexty.CompileRequest{CurrentTurn: &turn})
			// Assert.
			require.ErrorIs(t, err, scenario.want)
			require.Zero(t, result)
		})
	}
}

func TestAcceptance_SelectionRejectsRawTurnAndPartialRound(t *testing.T) {
	t.Parallel()
	// Arrange: raw content shares an ID with its approved projection but has another digest.
	raw := fixtureRollingText("turn", "PRIVATE")
	turn := contexty.NewCurrentTurn(raw).WithPromptSafe(fixtureRollingText("turn", "safe"))
	rawRef := fixtureRefForMessage(t, raw)
	policy := contexty.SelectionPolicy{
		Identity: contexty.Descriptor{ID: "host/raw", Revision: "1"},
		Select: func(context.Context, []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
			return []contexty.SelectionChoice{{Ref: rawRef}}, nil
		},
	}
	// Act / Assert.
	result, err := fixtureEngine(contexty.WithSelectionPolicy(policy)).
		CompileSnapshot(t.Context(), contexty.CompileRequest{CurrentTurn: &turn})
	require.ErrorIs(t, err, contexty.ErrUnavailableCandidate)
	require.Zero(t, result)
	// Arrange: select a tool result instead of its complete assistant/result unit.
	assistant := contexty.Message{
		ID:    "call",
		Role:  contexty.RoleAssistant,
		Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "c", Name: "tool"}},
	}
	answer := contexty.Message{
		ID:   "answer",
		Role: contexty.RoleTool,
		Parts: []contexty.ContentPart{
			contexty.ToolResultPart{ToolCallID: "c", Payload: contexty.TextPayload("answer")},
		},
	}
	policy.Select = func(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
		require.Len(t, candidates, 1)
		require.Len(t, candidates[0].Messages, 2)
		return []contexty.SelectionChoice{{Ref: fixtureRefForMessage(t, candidates[0].Messages[1])}}, nil
	}
	// Act / Assert.
	result, err = fixtureEngine(contexty.WithSelectionPolicy(policy)).
		CompileSnapshot(t.Context(), contexty.CompileRequest{History: []contexty.Message{assistant, answer}})
	require.ErrorIs(t, err, contexty.ErrSelectionRound)
	require.Zero(t, result)
}

func TestAcceptance_ArtifactAdmissionUsesEachOutputEstimator(t *testing.T) {
	t.Parallel()
	// Arrange: one estimator excludes an artifact; the other admits the same exact candidate.
	artifact := contexty.NewMemoryBlock("doc", contexty.TextPayload("data")).
		WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 10})
	ref := fixtureArtifactContentRef(t, artifact)
	main := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
		&contexty.FixedEstimator{TokensPerMessage: 20},
	)
	target := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
		&contexty.FixedEstimator{TokensPerMessage: 5},
	)
	calls := 0
	selection := contexty.SelectionPolicy{
		Identity: contexty.Descriptor{ID: "host/evidence", Revision: "1"},
		Select: func(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
			calls++
			require.Len(t, candidates, 1)
			require.Equal(t, ref, candidates[0].Ref)
			return []contexty.SelectionChoice{{Ref: ref}}, nil
		},
	}
	engine := fixtureEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, main),
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile("consumer")),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "1"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	// Act.
	result, err := engine.CompileSnapshot(
		t.Context(),
		contexty.CompileRequest{
			CompilationID: "independent-artifact",
			Artifacts:     []contexty.ContextArtifact{artifact},
			Targets: []contexty.CompileTarget{
				{Name: "consumer", ArtifactRefs: []contexty.ContentRef{ref}, Budget: target, Selection: &selection},
			},
		},
	)
	// Assert.
	require.NoError(t, err)
	require.Empty(t, result.Payload.Memory)
	require.Empty(t, result.Artifacts)
	require.Equal(t, []string{"doc"}, result.Projections["consumer"].ArtifactIDs)
	require.Equal(t, 20, result.ArtifactEstimates[0].Tokens)
	require.Equal(t, 5, result.Projections["consumer"].ArtifactEstimates[0].Tokens)
	require.Equal(t, 1, calls)
	accepted, err := result.Record.Accept("host/accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(t.Context(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Empty(t, replayed.Artifacts)
	require.Equal(t, []contexty.ContextArtifact{artifact}, replayed.Outputs[1].Artifacts)
	envelope, err := contexty.ExportProjection(
		result.Projections["consumer"],
		contexty.ExportSelection{ArtifactPayloadRefs: []contexty.ContentRef{ref}},
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	require.Len(t, envelope.Artifacts, 1)
}
