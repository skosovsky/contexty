package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResourceAppend_Sequence(t *testing.T) {
	for _, scenario := range []struct {
		name              string
		sameBlock, strict bool
	}{
		{"same-block", true, false}, {"separate-blocks", false, false},
		{"strict-same-block", true, true}, {"strict-separate-blocks", false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: two actual resolutions append distinct bytes to one existing identity.
			first, firstReads := fixtureNamedAppendBlock(t, "first", "one")
			second, secondReads := fixtureNamedAppendBlock(t, "second", "two")
			blocks := []contexty.DeferredBlock{first, second}
			if scenario.sameBlock {
				blocks = []contexty.DeferredBlock{fixtureCombineResourceBlocks(first, second)}
			}
			old := contexty.NewMemoryBlock("shared", contexty.TextPayload("old")).ContextArtifact
			oldRef, err := contexty.ArtifactContentRef(old)
			require.NoError(t, err)
			// Act.
			compiled, err := fixtureAppendSequenceEngine(
				blocks,
				scenario.strict,
			).CompileSnapshot(context.Background(), contexty.CompileRequest{
				CompilationID: "append-sequence",
				Artifacts:     []contexty.ContextArtifact{old},
				Origins:       []contexty.ContentRef{oldRef},
				Targets:       []contexty.CompileTarget{{Name: "memory", SourceSegment: contexty.SegmentMemory}},
			})
			// Assert: only the last derived revision reaches main/target; both incoming records remain evidence.
			require.NoError(t, err)
			require.Len(t, compiled.Payload.Memory, 1)
			require.Equal(t, "old\none\ntwo", compiled.Payload.Memory[0].TextContent())
			require.Equal(t, compiled.Payload.Memory, compiled.Projections["memory"].Messages)
			require.Len(t, compiled.Artifacts, 1)
			require.Len(t, compiled.Manifest.Resources, 2)
			require.Equal(
				t,
				compiled.Manifest.Resources[0].Merge.Lineage.Records[0].Outputs[0],
				compiled.Manifest.Resources[1].Merge.Inputs[0],
			)
			require.Equal(t, []contexty.ContextArtifact{old}, compiled.Source.Artifacts)
			accepted, err := compiled.Record.Accept("host-accept")
			require.NoError(t, err)
			fixtureAssertAppendSequenceReplay(t, compiled, accepted)
			require.Equal(t, 1, *firstReads)
			require.Equal(t, 1, *secondReads)
		})
	}
}
