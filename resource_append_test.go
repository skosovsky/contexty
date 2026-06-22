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
					CompilationID: "append-admission", Artifacts: []contexty.ContextArtifact{old},
					Targets: []contexty.CompileTarget{{Name: "memory", SourceSegment: contexty.SegmentMemory}},
				})
				// Assert: final prompt, targets and artifacts agree; Source/evidence stay unchanged.
				require.NoError(t, err)
				want := "old"
				if limit == 8 {
					want = "old\nsafe"
				}
				require.Len(t, compiled.Artifacts, 1)
				require.Equal(t, want, compiled.Artifacts[0].Payload.Text)
				require.Len(t, compiled.Payload.Memory, 1)
				require.Equal(t, want, compiled.Payload.Memory[0].TextContent())
				require.Equal(t, compiled.Payload.Memory, compiled.Projections["memory"].Messages)
				require.Equal(t, []contexty.ContextArtifact{old}, compiled.Source.Artifacts)
				require.Empty(t, compiled.DerivePersistenceProjection(contexty.SegmentMemory))
				require.Equal(t, 4, compiled.Manifest.Resources[0].Estimate.Total)
				merge := compiled.Manifest.Resources[0].Merge
				if limit == 0 {
					require.Nil(t, merge)
				} else {
					require.NotNil(t, merge)
					require.Equal(t, limit == 8, merge.Admitted)
					require.Len(t, merge.Inputs, 2)
					require.Len(t, compiled.ArtifactEstimates, 2)
					require.Equal(t, 8, compiled.ArtifactEstimates[1].Tokens)
				}
				accepted, err := compiled.Record.Accept("host-accept")
				require.NoError(t, err)
				fixtureAssertAppendReplay(t, compiled, accepted, reads)
			},
		)
	}
}
