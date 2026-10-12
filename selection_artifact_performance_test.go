package contexty

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Only artifacts carry this extension: counts exclude Message ownership clones.
type selectionCountExtension struct{ calls *int }

func (e selectionCountExtension) ExtensionType() string { return "selection/count" }
func (e selectionCountExtension) CloneExtension() Extension {
	*e.calls++
	return e
}

func selectionArtifactFixture(n, a int, matched bool, calls *int) ConversationSnapshot {
	messages := make([]Message, n)
	for i := range messages {
		messages[i] = TextMessage(RoleUser, "audit history")
		messages[i].ID = fmt.Sprintf("m%d", i)
		if matched && i < a {
			messages[i].ID = fmt.Sprintf("artifact:a%d", i)
		}
	}
	artifacts := make([]ContextArtifact, a)
	for i := range artifacts {
		artifacts[i] = NewMemoryBlock(fmt.Sprintf("a%d", i),
			JSONPayload(`{"text":"`+strings.Repeat("x", 1024)+`"}`)).ContextArtifact
		if calls != nil {
			artifacts[i].Extensions = []Extension{selectionCountExtension{calls}}
		}
	}
	return EmptySnapshot().WithSegment(SegmentHistory, messages).WithArtifacts(artifacts)
}

func TestSelectionArtifactCloneBound(t *testing.T) {
	for _, n := range []int{10, 100, 300} {
		for _, a := range []int{0, 10, 100, 300} {
			for _, matched := range []bool{false, true} {
				t.Run(fmt.Sprintf("N%d/A%d/matched%t", n, a, matched), func(t *testing.T) {
					// Arrange: reset the artifact-only counter after snapshot ownership copies.
					calls := 0
					snap := selectionArtifactFixture(n, a, matched, &calls)
					calls = 0
					// Act.
					candidates, err := selectionCandidates(t.Context(), snap, nil, nil)
					// Assert: only matches can require artifact ownership copies.
					require.NoError(t, err)
					require.Len(t, candidates, n)
					matches := 0
					for i, candidate := range candidates {
						require.Equal(t, i, candidate.Ordinal)
						if matched && i < a {
							matches++
							artifact := snap.artifacts[fmt.Sprintf("a%d", i)]
							ref, refErr := ArtifactContentRef(artifact)
							require.NoError(t, refErr)
							require.Equal(t, ref, candidate.Ref)
							require.Equal(t, artifact, *candidate.Artifact)
						} else {
							require.Nil(t, candidate.Artifact)
							require.Equal(t, candidate.Members[0], candidate.Ref)
						}
					}
					require.LessOrEqual(t, calls, a)
					require.LessOrEqual(t, calls, matches)
					t.Logf("N=%d A=%d matches=%d artifact_clones=%d", n, a, matches, calls)
				})
			}
		}
	}
}

func BenchmarkSelectionArtifactPreparation(b *testing.B) {
	for _, n := range []int{10, 100, 300} {
		for _, a := range []int{0, 10, 100, 300} {
			for _, matched := range []bool{false, true} {
				b.Run(fmt.Sprintf("N%d/A%d/matched%t", n, a, matched), func(b *testing.B) {
					// Arrange.
					snap := selectionArtifactFixture(n, a, matched, nil)
					b.ReportAllocs()
					b.ResetTimer()
					// Act / Assert: all per-invocation copies, lookup and digests are timed.
					for range b.N {
						if _, err := selectionCandidates(b.Context(), snap, nil, nil); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func selectionBenchmarkMaterializer() ArtifactMaterializationPolicy {
	return ArtifactMaterializationPolicy{
		Identity: Descriptor{ID: "audit/materialize", Revision: "1"},
		Materialize: func(_ context.Context, a ContextArtifact) (ArtifactRepresentation, error) {
			return ArtifactRepresentation{Role: RoleSystem,
				Parts: []ContentPart{TextPart{Text: string(a.Payload.Data)}}}, nil
		},
	}
}

func BenchmarkSelectionArtifactCompile(b *testing.B) {
	for _, n := range []int{10, 100, 300} {
		for _, a := range []int{0, 10, 100, 300} {
			for _, targets := range []int{0, 1, 4} {
				b.Run(fmt.Sprintf("N%d/A%d/targets%d", n, a, targets), func(b *testing.B) {
					benchmarkSelectionArtifactCompile(b, n, a, targets)
				})
			}
		}
	}
}

func benchmarkSelectionArtifactCompile(b *testing.B, n, a, targets int) {
	b.Helper()
	// Arrange: ordinary history plus artifacts materialized by the public engine.
	snap := selectionArtifactFixture(n, a, false, nil)
	engine := NewEngine(WithArtifactMaterialization(selectionBenchmarkMaterializer()))
	request := CompileRequest{History: snap.Segment(SegmentHistory), Artifacts: snap.Artifacts()}
	for i := range targets {
		request.Targets = append(request.Targets, CompileTarget{Name: strconv.Itoa(i),
			Segments: []SegmentName{SegmentHistory}, IncludeArtifacts: true})
	}
	b.ReportAllocs()
	b.ResetTimer()
	// Act / Assert: full public compile, including validation and target selection.
	for range b.N {
		if _, err := engine.CompileSnapshot(b.Context(), request); err != nil {
			b.Fatal(err)
		}
	}
}
