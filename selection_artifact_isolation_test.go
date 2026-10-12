package contexty

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type selectionOwnedExtension struct {
	Data []byte `json:"data"`
}

func (e *selectionOwnedExtension) ExtensionType() string { return "selection/owned" }
func (e *selectionOwnedExtension) CloneExtension() Extension {
	return &selectionOwnedExtension{Data: append([]byte(nil), e.Data...)}
}

func selectionOwnedArtifact(binary bool) ContextArtifact {
	payload := JSONPayload(`{"text":"` + strings.Repeat("x", 64*1024) + `"}`)
	if binary {
		payload = BinaryPayload([]byte(strings.Repeat("x", 64*1024)), "image/png")
	}
	artifact := NewMemoryBlock("owned", payload).ContextArtifact
	artifact.Extensions = []Extension{&selectionOwnedExtension{Data: []byte("original")}}
	artifact.SourceRefs = []SourceRef{{Namespace: "host", ID: "source"}}
	artifact.OwnerRef = &SourceRef{Namespace: "host", ID: "owner"}
	artifact.Budget = &ArtifactBudgetPolicy{TokenLimit: 1000000}
	return artifact
}

func mutateSelectionArtifact(artifact *ContextArtifact) {
	if len(artifact.Payload.Data) > 0 {
		artifact.Payload.Data[0] = '!'
	}
	if len(artifact.Payload.Binary) > 0 {
		artifact.Payload.Binary[0] = '!'
	}
	artifact.Extensions[0].(*selectionOwnedExtension).Data[0] = '!'
	artifact.SourceRefs[0].ID = "mutated"
	artifact.OwnerRef.ID = "mutated"
	artifact.Budget.TokenLimit = 0
}

func TestSelectionArtifactOwnedBoundaries(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(fmt.Sprintf("binary%t", binary), func(t *testing.T) {
			// Arrange: two candidates intentionally address the same artifact in different segments.
			artifact := selectionOwnedArtifact(binary)
			message := TextMessage(RoleUser, "materialized")
			message.ID = "artifact:" + artifact.ID
			snap := EmptySnapshot().WithArtifacts([]ContextArtifact{artifact}).
				WithSegment(SegmentMemory, []Message{message}).WithSegment(SegmentSystem, []Message{message})
			before := snap.Artifacts()
			candidates, err := selectionCandidates(t.Context(), snap, nil, nil)
			require.NoError(t, err)
			require.Len(t, candidates, 2)
			policy := &SelectionPolicy{Identity: Descriptor{ID: "mutator", Revision: "1"},
				Select: func(_ context.Context, input []ContextCandidate) ([]SelectionChoice, error) {
					mutateSelectionArtifact(input[0].Artifact)
					input[0].Messages[0].Parts[0] = TextPart{Text: "mutated"}
					input[0].Members[0].ID = "mutated"
					return []SelectionChoice{{Ref: input[0].Ref}, {Ref: input[1].Ref}}, nil
				}}
			// Act: callback mutation, then direct mutation of a prepared candidate.
			_, err = selectionPlan(t.Context(), candidates, policy)
			// Assert: callback receives no aliases to prepared candidates.
			require.NoError(t, err)
			require.Equal(t, artifact, *candidates[0].Artifact)
			require.Equal(t, message, candidates[0].Messages[0])
			mutateSelectionArtifact(candidates[0].Artifact)
			require.Equal(t, artifact, *candidates[1].Artifact)
			require.Equal(t, before, snap.Artifacts())
		})
	}
}

func TestSelectionArtifactAccessorAndRevision(t *testing.T) {
	// Arrange: lexical order differs from insertion order, and IDs include another prefix.
	artifact := selectionOwnedArtifact(false)
	artifact.ID = "artifact:owned"
	other := NewMemoryBlock("z", TextPayload("last")).ContextArtifact
	prior := EmptySnapshot().WithArtifacts([]ContextArtifact{other, artifact})
	updated := artifact.Clone()
	updated.Payload = JSONPayload(`{"revision":2}`)
	next := prior.WithArtifact(updated)
	messages := make([]Message, 3)
	for i, id := range []string{"artifact:artifact:owned", "artifact:owned", "xartifact:artifact:owned"} {
		messages[i] = TextMessage(RoleUser, "materialized")
		messages[i].ID = id
	}
	// Act.
	oldCandidates, oldErr := selectionCandidates(t.Context(), prior.WithSegment(SegmentMemory, messages), nil, nil)
	newCandidates, newErr := selectionCandidates(t.Context(), next.WithSegment(SegmentMemory, messages), nil, nil)
	owned := prior.Artifacts()
	mutateSelectionArtifact(&owned[0])
	// Assert: strip exactly one prefix; current content controls the ref; prior state is unchanged.
	require.NoError(t, oldErr)
	require.NoError(t, newErr)
	require.NotEqual(t, oldCandidates[0].Ref, newCandidates[0].Ref)
	for _, candidates := range [][]ContextCandidate{oldCandidates, newCandidates} {
		require.Nil(t, candidates[1].Artifact)
		require.Nil(t, candidates[2].Artifact)
	}
	require.Equal(t, []ContextArtifact{artifact, other}, prior.Artifacts())
	require.Equal(t, updated, next.Artifacts()[0])
}

func TestSelectionArtifactCompileIsolation(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(fmt.Sprintf("binary%t", binary), func(t *testing.T) {
			// Arrange: mutating host callbacks serve main plus four independent targets.
			artifact := selectionOwnedArtifact(binary)
			calls := 0
			policy := ArtifactMaterializationPolicy{Identity: Descriptor{ID: "media", Revision: "1"},
				Materialize: func(_ context.Context, input ContextArtifact) (ArtifactRepresentation, error) {
					calls++
					parts, err := ArtifactContentParts(input)
					mutateSelectionArtifact(&input)
					return ArtifactRepresentation{Role: RoleSystem, Parts: parts}, err
				}}
			selectionCalls := 0
			selection := SelectionPolicy{Identity: Descriptor{ID: "mutator", Revision: "1"},
				Select: func(_ context.Context, input []ContextCandidate) ([]SelectionChoice, error) {
					selectionCalls++
					choices := make([]SelectionChoice, len(input))
					for i := range input {
						choices[i].Ref = input[i].Ref
						if input[i].Artifact != nil {
							require.Equal(t, artifact, *input[i].Artifact)
							mutateSelectionArtifact(input[i].Artifact)
						}
					}
					return choices, nil
				}}
			// Typed media needs an explicit token cost, including artifact-local admission.
			pipe := NewBudgetPipeline(BudgetConfig{Budget: EffectiveInputBudget(100)},
				&FixedEstimator{TokensPerMessage: 1})
			engine := NewEngine(
				WithArtifactMaterialization(policy),
				WithSelectionPolicy(selection),
				WithBudgetPipeline(pipe),
			)
			request := CompileRequest{Artifacts: []ContextArtifact{artifact}}
			for i := range 4 {
				request.Targets = append(request.Targets, CompileTarget{Name: strconv.Itoa(i),
					IncludeArtifacts: true, Selection: &selection, Budget: pipe})
			}
			// Act.
			first, err := engine.CompileSnapshot(t.Context(), request)
			require.NoError(t, err)
			mutateSelectionArtifact(&first.Artifacts[0])
			projection := first.Projections["0"]
			mutateSelectionArtifact(&projection.Artifacts[0])
			projection.Messages[0].SourceRefs[0].ID = "mutated"
			second, secondErr := engine.CompileSnapshot(t.Context(), request)
			// Assert: one materialization per request, with no cache carried between requests.
			require.NoError(t, secondErr)
			require.Equal(t, 2, calls)
			require.Equal(t, 10, selectionCalls)
			require.Equal(t, artifact, request.Artifacts[0])
			require.Equal(t, artifact, second.Artifacts[0])
			require.Equal(t, artifact, first.PreparedSnapshot.Artifacts()[0])
			require.Equal(t, second.Selection, first.Selection)
			require.Equal(t, second.Payload, first.Payload)
			for _, name := range []string{"1", "2", "3"} {
				p := first.Projections[name]
				require.Equal(t, artifact, p.Artifacts[0])
				require.Equal(t, second.Projections[name].Messages, p.Messages)
				require.Equal(t, second.Projections[name].Selection, p.Selection)
			}
		})
	}
}

func TestSelectionArtifactCompileErrors(t *testing.T) {
	for _, scenario := range []string{"empty", "invalid", "canceled", "callback error", "callback cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange.
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request := CompileRequest{}
			policy := selectionBenchmarkMaterializer()
			calls := 0
			hostErr := errors.New("host materializer")
			if scenario != "empty" {
				request.Artifacts = selectionArtifactFixture(0, 1, false, nil).Artifacts()
			}
			switch scenario {
			case "invalid":
				request.Artifacts[0].Budget = &ArtifactBudgetPolicy{TokenLimit: -1}
			case "canceled":
				cancel()
			case "callback error", "callback cancellation":
				policy.Materialize = func(context.Context, ContextArtifact) (ArtifactRepresentation, error) {
					calls++
					if scenario == "callback cancellation" {
						cancel()
					}
					return ArtifactRepresentation{}, hostErr
				}
			}
			engine := NewEngine(WithArtifactMaterialization(policy))
			// Act.
			result, err := engine.CompileSnapshot(ctx, request)
			// Assert.
			assertSelectionArtifactCompileError(t, scenario, result, err, hostErr, calls)
		})
	}
}

func assertSelectionArtifactCompileError(
	t *testing.T,
	scenario string,
	result CompileResult,
	err, hostErr error,
	calls int,
) {
	t.Helper()
	if scenario == "empty" {
		require.NoError(t, err)
		require.Empty(t, result.Artifacts)
		return
	}
	require.Zero(t, result)
	switch scenario {
	case "canceled", "callback cancellation":
		require.ErrorIs(t, err, context.Canceled)
	case "callback error":
		require.ErrorIs(t, err, hostErr)
	default:
		require.Error(t, err)
	}
	if strings.HasPrefix(scenario, "callback") {
		require.Equal(t, 1, calls)
	}
}

// The same fixture runs on the archived baseline and the candidate. The golden
// includes chronology, exact refs, priorities, coverage lineage and all targets.
func TestSelectionArtifactCompatibility(t *testing.T) {
	// Arrange.
	type target struct {
		Messages  []Message          `json:"Messages"`
		Artifacts []ContextArtifact  `json:"Artifacts"`
		Selection *SelectionDecision `json:"Selection"`
		Lineage   Lineage            `json:"Lineage"`
	}
	type output struct {
		Payload   AbstractPayload    `json:"Payload"`
		Artifacts []ContextArtifact  `json:"Artifacts"`
		Selection *SelectionDecision `json:"Selection"`
		Lineage   Lineage            `json:"Lineage"`
		Targets   map[string]target  `json:"Targets"`
	}
	snap := selectionArtifactFixture(10, 10, false, nil)
	policy := SelectionPolicy{Identity: Descriptor{ID: "compatibility", Revision: "1"},
		Select: func(_ context.Context, candidates []ContextCandidate) ([]SelectionChoice, error) {
			choices := make([]SelectionChoice, len(candidates))
			for i, c := range candidates {
				choices[i] = SelectionChoice{Ref: c.Ref, Priority: i % 3}
			}
			return choices, nil
		}}
	engine := NewEngine(WithArtifactMaterialization(selectionBenchmarkMaterializer()), WithSelectionPolicy(policy))
	request := CompileRequest{History: snap.Segment(SegmentHistory), Artifacts: snap.Artifacts()}
	var outputs []output
	// Act.
	for _, count := range []int{0, 1, 4} {
		request.Targets = nil
		for i := range count {
			request.Targets = append(request.Targets, CompileTarget{Name: strconv.Itoa(i),
				Segments: []SegmentName{SegmentHistory}, IncludeArtifacts: true, Selection: &policy})
		}
		result, err := engine.CompileSnapshot(t.Context(), request)
		require.NoError(t, err)
		item := output{result.Payload, result.Artifacts, result.Selection, result.Lineage, map[string]target{}}
		for name, p := range result.Projections {
			item.Targets[name] = target{p.Messages, p.Artifacts, p.Selection, p.Lineage}
		}
		outputs = append(outputs, item)
	}
	wire, err := json.Marshal(outputs)
	// Assert: exact output hash from the archived baseline at 08b2101.
	require.NoError(t, err)
	require.Equal(t, "5c7d45f2d2eda652d18aa568312e33d1b2334b096cae1bc8e9c0cb779778df52",
		fmt.Sprintf("%x", sha256.Sum256(wire)))
	t.Logf("compatibility_sha256=%x", sha256.Sum256(wire))
}

func TestSelectionArtifactCodecRoundTrip(t *testing.T) {
	// Arrange: host types on both messages and artifacts retain codec and clone semantics.
	registry := NewExtensionRegistry()
	registry.Register("selection/owned", func(wire []byte) (Extension, error) {
		var e selectionOwnedExtension
		err := json.Unmarshal(wire, &e)
		return &e, err
	})
	codec := ConversationCodec{Extensions: registry}
	artifact := selectionOwnedArtifact(true)
	message := TextMessage(RoleUser, "materialized")
	message.ID = "artifact:" + artifact.ID
	message.Extensions = []Extension{&selectionOwnedExtension{Data: []byte("message")}}
	snap := EmptySnapshot().WithArtifact(artifact).WithSegment(SegmentMemory, []Message{message})
	wire, err := codec.Encode(snap)
	require.NoError(t, err)
	// Act.
	decoded, err := codec.Decode(wire)
	require.NoError(t, err)
	candidates, err := selectionCandidates(t.Context(), decoded, nil, nil)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, artifact, *candidates[0].Artifact)
	require.Equal(t, message, candidates[0].Messages[0])
	mutateSelectionArtifact(candidates[0].Artifact)
	candidates[0].Messages[0].Extensions[0].(*selectionOwnedExtension).Data[0] = '!'
	require.Equal(t, snap.Artifacts(), decoded.Artifacts())
	require.Equal(t, snap.Segment(SegmentMemory), decoded.Segment(SegmentMemory))
}
