package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/resource/memory"
)

func fixtureSelectAll(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
	choices := make([]contexty.SelectionChoice, len(candidates))
	for i, candidate := range candidates {
		choices[i] = contexty.SelectionChoice{Ref: candidate.Ref}
	}
	return choices, nil
}

func TestAcceptance_SelectionToolsRoundIsAtomic(t *testing.T) {
	t.Parallel()
	// Arrange: tool exchange is explicitly composed from the Tools segment.
	call := contexty.Message{ID: "call", Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "invoke", Name: "lookup"}}}
	answer := contexty.Message{
		ID:   "answer",
		Role: contexty.RoleTool,
		Parts: []contexty.ContentPart{
			contexty.ToolResultPart{ToolCallID: "invoke", Payload: contexty.TextPayload("answer")},
		},
	}
	policy := contexty.SelectionPolicy{Identity: contexty.Descriptor{ID: "host/partial-tools", Revision: "1"},
		Select: func(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
			require.Len(t, candidates, 1)
			require.Len(t, candidates[0].Members, 2)
			return []contexty.SelectionChoice{{Ref: fixtureRefForMessage(t, answer)}}, nil
		}}
	request := contexty.CompileRequest{
		Tools: []contexty.Message{call, answer},
		Targets: []contexty.CompileTarget{
			{Name: "consumer", Segments: []contexty.SegmentName{contexty.SegmentTools}, Selection: &policy},
		},
	}
	// Act.
	result, err := fixtureEngine().CompileSnapshot(t.Context(), request)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrSelectionRound)
	require.Zero(t, result)
}

func TestAcceptance_SelectionRequiredRefsSurviveTransformationsExactly(t *testing.T) {
	cases := []struct {
		name      string
		transform func(context.Context, []contexty.Message) ([]contexty.Message, error)
	}{
		{
			name: "same ID changed content",
			transform: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "REPLACED"}}
				return messages, nil
			},
		},
		{
			name: "deleted required member",
			transform: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				return messages[1:], nil
			},
		},
		{
			name: "reversed required chronology",
			transform: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				return []contexty.Message{messages[1], messages[0]}, nil
			},
		},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: independent mandatory units carry exact host-required refs.
			messages := []contexty.Message{
				fixtureRollingText("first", "keep first"),
				fixtureRollingText("second", "keep second"),
			}
			policy := contexty.SelectionPolicy{
				Identity: contexty.Descriptor{ID: "host/exact", Revision: "1"},
				Required: []contexty.ContentRef{
					fixtureRefForMessage(t, messages[0]),
					fixtureRefForMessage(t, messages[1]),
				},
				Select: fixtureSelectAll,
			}
			request := contexty.CompileRequest{Memory: messages, Targets: []contexty.CompileTarget{
				{
					Name:      "consumer",
					Segments:  []contexty.SegmentName{contexty.SegmentMemory},
					Selection: &policy,
					Formatter: scenario.transform,
				},
			}}
			// Act.
			result, err := fixtureEngine().CompileSnapshot(t.Context(), request)
			// Assert.
			require.ErrorIs(t, err, contexty.ErrMandatorySelection)
			require.Zero(t, result)
		})
	}
}

func TestAcceptance_SelectionCurrentTurnAllowsExplicitPromptReplacement(t *testing.T) {
	t.Parallel()
	// Arrange: no exact host retention requirement; an explicit option redacts the approved prompt projection.
	raw := fixtureRollingText("turn", "PRIVATE")
	turn := contexty.NewCurrentTurn(raw).WithPromptSafe(fixtureRollingText("turn", "safe@example.test"))
	policy := contexty.SelectionPolicy{
		Identity: contexty.Descriptor{ID: "host/current", Revision: "1"},
		Select:   fixtureSelectAll,
	}
	request := contexty.CompileRequest{CurrentTurn: &turn,
		Options: []contexty.CompileOption{contexty.WithTextReplacement(contexty.TextReplacement{
			Segment: contexty.SegmentHistory, MessageID: "turn", Text: "REDACTED",
		})},
		Targets: []contexty.CompileTarget{{Name: "consumer", IncludeCurrentTurn: true, Selection: &policy}},
	}
	// Act.
	result, err := fixtureEngine(contexty.WithSelectionPolicy(policy)).CompileSnapshot(t.Context(), request)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
	require.Equal(t, "REDACTED", result.Projections["consumer"].Messages[0].TextContent())
	require.Equal(t, "PRIVATE", turn.Raw.TextContent())
}

func TestAcceptance_SelectionMandatoryOverflowIsAtomic(t *testing.T) {
	t.Parallel()
	// Arrange: mandatory context exceeds capacity even with no optional candidates.
	message := fixtureRollingText("required", "keep")
	policy := contexty.SelectionPolicy{Identity: contexty.Descriptor{ID: "host/required", Revision: "1"},
		Required: []contexty.ContentRef{fixtureRefForMessage(t, message)}, Select: fixtureSelectAll}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1)},
		&contexty.FixedEstimator{TokensPerMessage: 2})
	// Act.
	result, err := fixtureEngine(
		contexty.WithSelectionPolicy(policy),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	).
		CompileSnapshot(
			t.Context(), contexty.CompileRequest{History: []contexty.Message{message}})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Zero(t, result)
}

func TestAcceptance_SelectionPrioritySharesCapacityAcrossHistoryAndArtifacts(t *testing.T) {
	t.Parallel()
	// Arrange: one unit fits; a higher-priority artifact competes with ordinary history.
	artifact := contexty.NewMemoryBlock("memory", contexty.TextPayload("remember"))
	artifactRef := fixtureArtifactContentRef(t, artifact.ContextArtifact)
	policy := contexty.SelectionPolicy{Identity: contexty.Descriptor{ID: "host/global", Revision: "1"},
		Select: func(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
			choices, err := fixtureSelectAll(t.Context(), candidates)
			for i := range choices {
				if choices[i].Ref == artifactRef {
					choices[i].Priority = 100
				}
			}
			return choices, err
		}}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1)},
		&contexty.FixedEstimator{TokensPerMessage: 1})
	request := contexty.CompileRequest{History: []contexty.Message{fixtureRollingText("history", "earlier")},
		Artifacts: []contexty.ContextArtifact{artifact.ContextArtifact}}
	// Act.
	result, err := fixtureEngine(
		contexty.WithSelectionPolicy(policy),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	).
		CompileSnapshot(t.Context(), request)
	// Assert.
	require.NoError(t, err)
	require.Empty(t, result.Payload.History)
	require.Len(t, result.Payload.Memory, 1)
	require.Equal(t, artifact.ContextArtifact, result.Artifacts[0])
	for _, candidate := range result.Selection.Candidates {
		if candidate.Segment == contexty.SegmentHistory {
			require.False(t, candidate.Selected)
			require.Equal(t, contexty.ReasonTokenBudgetExceeded, candidate.Reason)
		}
	}
}

func TestAcceptance_SelectionOutputsPinDifferentModelProfiles(t *testing.T) {
	t.Parallel()
	// Arrange: two consumers use independent models and input capacities.
	mainProfile := fixtureEstimateProfile()
	consumerProfile := fixtureEstimateProfile()
	consumerProfile.Model = contexty.Descriptor{ID: "consumer-model", Revision: "2"}
	consumerProfile.Estimator = contexty.Descriptor{ID: "consumer-estimator", Revision: "2"}
	mainReporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		mainProfile,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	consumerReporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		consumerProfile,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	mainBudget := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(3)},
		mainReporter,
	)
	consumerBudget := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
		consumerReporter,
	)
	engine := fixtureEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, mainBudget),
		contexty.WithTraceProfile(
			fixtureTraceProfile(),
		),
		contexty.WithCompileRecording(fixtureRecordProfile("consumer")),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	request := contexty.CompileRequest{
		CompilationID: "different-models",
		History:       fixtureSelectionMessages(),
		Targets: []contexty.CompileTarget{
			{Name: "consumer", Segments: []contexty.SegmentName{contexty.SegmentHistory}, Budget: consumerBudget},
		},
	}
	// Act.
	result, err := engine.CompileSnapshot(t.Context(), request)
	// Assert: wider consumer sees the shared preparation, and replay preserves its profile.
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 1)
	require.Len(t, result.Projections["consumer"].Messages, 3)
	require.Equal(t, consumerProfile, result.Estimates[1].Report.Profile)
	accepted, err := result.Record.Accept("host/accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(t.Context(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, result.Estimates, replayed.Manifest.EstimateReports)
}

func TestAcceptance_SelectionSharedResourceResolvesOnce(t *testing.T) {
	t.Parallel()
	// Arrange: two independent consumers share one authorized resource resolution.
	block, reads := fixtureResourceBlock(t)
	engine := fixtureEngine(contexty.WithDeferredBlocks(block))
	request := contexty.CompileRequest{Targets: []contexty.CompileTarget{
		{Name: "one", IncludeArtifacts: true}, {Name: "two", IncludeArtifacts: true},
	}}
	// Act.
	result, err := engine.CompileSnapshot(t.Context(), request)
	// Assert: authorization and reading happen during preparation, not per consumer.
	require.NoError(t, err)
	require.Equal(t, 1, *reads)
	require.NotEmpty(t, result.Projections["one"].Artifacts)
	require.Equal(t, result.Projections["one"].Artifacts, result.Projections["two"].Artifacts)
}

func fixtureSelectionToolRound() []contexty.Message {
	return []contexty.Message{
		{
			ID:    "call",
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "invoke", Name: "lookup"}},
		},
		{
			ID:   "answer",
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "invoke", Payload: contexty.TextPayload("answer")},
			},
		},
	}
}

func fixtureSelectionFinalRoundRequest(target bool, removal string) (*contexty.Engine, contexty.CompileRequest) {
	formatter := func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
		switch removal {
		case "call":
			return messages[1:], nil
		case "result":
			return messages[:1], nil
		default:
			return nil, nil
		}
	}
	request := contexty.CompileRequest{Tools: fixtureSelectionToolRound()}
	engine := fixtureEngine(contexty.WithSegmentFormatter(contexty.SegmentTools, formatter))
	if target {
		engine = fixtureEngine()
		request.Targets = []contexty.CompileTarget{
			{Name: "consumer", Segments: []contexty.SegmentName{contexty.SegmentTools}, Formatter: formatter},
		}
	}
	return engine, request
}

func TestAcceptance_SelectionFinalToolsRoundIntegrity(t *testing.T) {
	for _, target := range []bool{false, true} {
		for _, removal := range []string{"whole", "call", "result"} {
			t.Run(map[bool]string{false: "main", true: "target"}[target]+"/"+removal, func(t *testing.T) {
				// Arrange: admission keeps a complete optional round, then formatter removes members.
				engine, request := fixtureSelectionFinalRoundRequest(target, removal)
				// Act.
				result, err := engine.CompileSnapshot(t.Context(), request)
				// Assert: an optional round may disappear completely, but losing either member is invalid.
				if removal != "whole" {
					require.ErrorIs(t, err, contexty.ErrInvalidToolRound)
					require.Zero(t, result)
					return
				}
				require.NoError(t, err)
				if target {
					require.Empty(t, result.Projections["consumer"].Messages)
				} else {
					require.Empty(t, result.Payload.Tools)
				}
			})
		}
	}
}

func TestAcceptance_SelectionPendingRoundCannotLoseSemanticParts(t *testing.T) {
	for _, loseCall := range []bool{false, true} {
		t.Run(map[bool]string{false: "result", true: "call"}[loseCall], func(t *testing.T) {
			// Arrange: preserve message IDs while stripping one semantic part of a mandatory pending round.
			formatter := func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				index := 1
				if loseCall {
					index = 0
				}
				messages[index].Parts = []contexty.ContentPart{contexty.TextPart{Text: "stripped"}}
				return messages, nil
			}
			request := contexty.CompileRequest{Pending: fixtureSelectionToolRound(), Targets: []contexty.CompileTarget{
				{Name: "consumer", IncludeCurrentTurn: true, Formatter: formatter},
			}}
			// Act.
			result, err := fixtureEngine().CompileSnapshot(t.Context(), request)
			// Assert: stable IDs cannot disguise loss of pending tool semantics.
			require.Error(t, err)
			require.Zero(t, result)
		})
	}
}

func TestAcceptance_SelectionPendingInFlightCallCannotLoseToolSemantics(t *testing.T) {
	t.Parallel()
	// Arrange: one in-flight call has no result yet, so replacing it with text would evade orphan-result checks.
	request := contexty.CompileRequest{Pending: fixtureSelectionToolRound()[:1], Targets: []contexty.CompileTarget{
		{
			Name:               "consumer",
			IncludeCurrentTurn: true,
			Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "stripped"}}
				return messages, nil
			},
		},
	}}
	// Act.
	result, err := fixtureEngine().CompileSnapshot(t.Context(), request)
	// Assert: pending tool semantics remain mandatory even when the message ID survives.
	require.Error(t, err)
	require.Zero(t, result)
}

func TestAcceptance_SelectionConsumerArtifactTurnLifecycle(t *testing.T) {
	for _, bound := range []string{"current", "previous"} {
		t.Run(bound, func(t *testing.T) {
			// Arrange: a resource projects an artifact bound to one exact turn.
			block, reads := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
				artifact.Lifecycle = contexty.ArtifactLifecycleTurnBound
				artifact.BoundTurnID = bound
			})
			request := contexty.CompileRequest{TurnID: "current", Targets: []contexty.CompileTarget{
				{Name: "one", IncludeArtifacts: true}, {Name: "two", IncludeArtifacts: true},
			}}
			// Act.
			result, err := fixtureEngine(contexty.WithDeferredBlocks(block)).CompileSnapshot(t.Context(), request)
			// Assert: every consumer obeys the same preparation lifecycle without reading again.
			require.NoError(t, err)
			require.Equal(t, 1, *reads)
			for _, projection := range result.Projections {
				if bound == "current" {
					require.Len(t, projection.Artifacts, 1)
					require.Len(t, projection.Messages, 1)
				} else {
					require.Empty(t, projection.Artifacts)
					require.Empty(t, projection.Messages)
				}
			}
		})
	}
}

func TestAcceptance_SelectionConsumersCannotBypassResourceAuthorization(t *testing.T) {
	t.Parallel()
	// Arrange: a selected resource exists, but its exact host scope is denied.
	resolver, request, body := fixtureResourceFixture(t)
	authorizations := 0
	reader, err := memory.New(memory.Config{MaxBodyBytes: 4096,
		Authorize: func(_ context.Context, received contexty.ResourceReadRequest) error {
			authorizations++
			require.Equal(t, request.Read, received)
			return contexty.ErrResourceDenied
		}}, body)
	require.NoError(t, err)
	resolver.Reader = reader
	engine := fixtureEngine(contexty.WithDeferredBlocks(fixtureAdapterResourceBlock(t, resolver, request)))
	compileRequest := contexty.CompileRequest{Targets: []contexty.CompileTarget{
		{Name: "one", IncludeArtifacts: true}, {Name: "two", IncludeArtifacts: true},
	}}
	// Act.
	result, err := engine.CompileSnapshot(t.Context(), compileRequest)
	// Assert: denied preparation aborts every consumer without expanding scope or retrying authorization.
	require.ErrorIs(t, err, contexty.ErrResourceDenied)
	require.Equal(t, 1, authorizations)
	require.Zero(t, result)
}
