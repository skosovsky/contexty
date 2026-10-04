package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResourceAppend_Admission(t *testing.T) {
	for _, limit := range []int{0, 4, 8} {
		t.Run(
			map[int]string{0: "incoming-excluded", 4: "merged-excluded", 8: "merged-admitted"}[limit],
			func(t *testing.T) {
				// Arrange: incoming safe costs four; old plus newline plus safe costs eight.
				block, reads := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
					artifact.MergePolicy = contexty.PolicyAppend
					artifact.Budget = &contexty.ArtifactBudgetPolicy{TokenLimit: limit}
				})
				old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
				old.SourceRefs = []contexty.SourceRef{{ID: "old-source"}}
				engine := fixtureDedupRecordingEngine(block)
				// Act.
				compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
					CompilationID: "append-admission",
					Artifacts:     []contexty.ContextArtifact{old},
					Targets: []contexty.CompileTarget{
						{
							Name:             "memory",
							Segments:         []contexty.SegmentName{contexty.SegmentMemory},
							IncludeArtifacts: true,
						},
					},
				})
				// Assert: final prompt, targets and artifacts agree; Source/evidence stay unchanged.
				require.NoError(t, err)
				require.Equal(t, []contexty.ContextArtifact{old}, compiled.Source.Artifacts)
				require.Equal(t, 4, compiled.Manifest.Resources[0].Estimate.Total)
				require.NotNil(t, compiled.Manifest.Resources[0].Merge)
				require.True(t, compiled.Manifest.Resources[0].Merge.Prepared)
				require.Len(t, compiled.ArtifactEstimates, 1)
				require.Equal(t, 8, compiled.ArtifactEstimates[0].Tokens)
				if limit == 8 {
					require.Len(t, compiled.Artifacts, 1)
					require.Equal(t, "old\nsafe", compiled.Artifacts[0].Payload.Text)
					require.Len(t, compiled.Payload.Memory, 1)
				} else {
					require.Empty(t, compiled.Artifacts)
					require.Empty(t, compiled.Payload.Memory)
				}
				require.Equal(t, compiled.Payload.Memory, compiled.Projections["memory"].Messages)
				accepted, err := compiled.Record.Accept("host-accept")
				require.NoError(t, err)
				fixtureAssertAppendReplay(t, compiled, accepted, reads)
			},
		)
	}
}
