package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResource_DeferredNative(t *testing.T) {
	// Arrange: only selected metadata is declared; reader executes inside deferred.
	block, calls := fixtureResourceBlock(t)
	profile := fixtureTraceProfile()
	profile.RequireOrigins = true
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(block), contexty.WithTraceProfile(profile))
	original := block.Resources[0]
	block.Resources[0].ID = "caller mutation"
	block.Resources[0].Configuration.Estimate.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
	// Act: the same resolved message reaches main and both target pipelines.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "resources",
		Targets: []contexty.CompileTarget{{Name: "first", SourceSegment: contexty.SegmentMemory},
			{Name: "second", SourceSegment: contexty.SegmentMemory}}})
	// Assert: no target refetch, no raw body in Source or prompt, full resource edges.
	require.NoError(t, err)
	require.Equal(t, 1, *calls)
	require.Empty(t, result.Source.Memory)
	require.Len(t, result.Source.DeferredResources, 1)
	require.Equal(t, original.ID, result.Source.DeferredResources[0].ID)
	require.Equal(
		t,
		contexty.EstimateEstimated,
		result.Source.DeferredResources[0].Configuration.Estimate.Capabilities[contexty.EstimateText],
	)
	require.Equal(t, "safe", result.Payload.Memory[0].TextContent())
	for _, projection := range result.Projections {
		require.Equal(t, "safe", projection.Messages[0].TextContent())
	}
	stages := make(map[string]bool)
	for _, record := range result.Lineage.Records {
		stages[record.Stage] = true
	}
	for _, stage := range []string{"resource-read", "resource-project", "resource-labels", "resource-materialize"} {
		require.True(t, stages[stage], stage)
	}
	require.NoError(t, result.Lineage.Validate())
}

func TestResource_DeferredBindingFailures(t *testing.T) {
	for _, scenario := range []string{"missing", "extra", "id", "descriptor", "configuration", "budget", "body"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: callback returns independently valid or malformed unselected evidence.
			block, _ := fixtureResourceBlock(t)
			resolve := block.Resolve
			block.Resolve = func(ctx context.Context) (contexty.DeferredResult, error) {
				result, err := resolve(ctx)
				if err != nil {
					return result, err
				}
				switch scenario {
				case "missing":
					result.Resources = nil
				case "extra":
					result.Resources = append(result.Resources, result.Resources[0])
				case "id":
					result.Resources[0].ID = "unselected"
				case "descriptor":
					result.Resources[0].Resource.Reference.Revision = "unselected"
				case "configuration":
					result.Resources[0].Configuration.Reader.Revision = "unselected"
				case "budget":
					result.Resources[0].Estimate.Budget = contexty.EffectiveInputBudget(99)
				case "body":
					result.Resources[0].Source.Artifact.Payload.Text = "changed"
				}
				return result, nil
			}
			engine := contexty.NewEngine(contexty.WithDeferredBlocks(block))
			// Act / Assert: no unselected dependency or partial prompt can escape.
			result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
			require.Error(t, err)
			require.Zero(t, result)
		})
	}
}

func TestResource_DeferredPreflight(t *testing.T) {
	for _, scenario := range []string{"callback", "id", "bytes", "budget", "configuration", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: all selected dependencies must be valid before any reader.
			block, calls := fixtureResourceBlock(t)
			blocks := []contexty.DeferredBlock{block}
			switch scenario {
			case "callback":
				blocks[0].Resolve = nil
			case "id":
				blocks[0].Resources[0].ID = ""
			case "bytes":
				blocks[0].Resources[0].MaxBytes--
			case "budget":
				blocks[0].Resources[0].Budget.EffectiveLimit = -1
			case "configuration":
				blocks[0].Resources[0].Configuration.Reader.Revision = ""
			case "duplicate":
				blocks = append(blocks, block)
			}
			engine := contexty.NewEngine(contexty.WithDeferredBlocks(blocks...))
			// Act / Assert: invalid declaration cannot execute even the first block.
			result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
			require.Error(t, err)
			require.Zero(t, result)
			require.Zero(t, *calls)
		})
	}
}

func TestResource_DeferredManifestConfiguration(t *testing.T) {
	// Arrange: declarations become immutable manifest configuration before any I/O.
	block, calls := fixtureResourceBlock(t)
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(block),
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureBindings(fixtureRecordProfile(),
			fixtureBinding(contexty.RecordingResolver, "", "", 0))))
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "declaration"})
	// Assert: selected revision/config/budget/byte bound are retained without raw body or scope.
	require.NoError(t, err)
	require.Equal(t, 1, *calls)
	require.Equal(t, block.Resources, result.Manifest.CompileConfiguration.Deferred[0].Resources)
	wire, err := contexty.EncodeManifest(*result.Manifest)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "private body")
	require.NotContains(t, string(wire), "fresh-read")
	cloned, err := result.Manifest.Clone()
	require.NoError(t, err)
	cloned.CompileConfiguration.Deferred[0].Resources[0].Configuration.Estimate.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
	require.Equal(
		t,
		contexty.EstimateEstimated,
		result.Manifest.CompileConfiguration.Deferred[0].Resources[0].Configuration.Estimate.Capabilities[contexty.EstimateText],
	)
}
