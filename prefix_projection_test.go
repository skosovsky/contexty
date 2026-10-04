package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestPrefix_NativeCompaction(t *testing.T) {
	// Arrange: the baseline describes the raw aged prefix; native compile will summarize it.
	history := []contexty.Message{fixtureRollingText("a", "aaaaaaaa"), fixtureRollingText("b", "bbbbbbbb"),
		fixtureRollingText("c", "cccccccc"), fixtureRollingText("d", "d"), fixtureRollingText("e", "e")}
	recipe := fixturePrefixRecipe()
	recipe.Boundaries = []contexty.PrefixBoundary{{ID: "aged-prefix", AfterMessageID: "c"}}
	previous, err := contexty.BuildPrefixManifest(context.Background(), history, recipe)
	require.NoError(t, err)
	calls := 0
	engine := fixtureRollingEngine(t, &calls)
	compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "prefix-compaction", History: history,
	})
	require.NoError(t, err)
	require.Len(t, compiled.Compactions, 1)
	accepted, err := compiled.Compactions[0].Accept("host-prefix-acceptance")
	require.NoError(t, err)
	fact := contexty.ContentRef{ID: accepted.ID, Digest: accepted.Digest}
	recipe.Boundaries[0].AfterMessageID = compiled.Payload.History[0].ID
	recipe.Evidence = []contexty.PrefixProjectionEvidence{
		{MessageID: compiled.Payload.History[0].ID, Compaction: &fact},
	}
	// Act: diagnose the real finalized projection, not a synthetic summary flag.
	report, err := contexty.DiagnosePrefix(context.Background(), compiled.Payload.FlattenMessages(), recipe, &previous)
	// Assert: compaction invalidates the old prefix and never executes the summarizer again.
	require.NoError(t, err)
	require.Equal(t, "aged-prefix", report.FirstAffectedBoundary)
	require.Equal(t, []contexty.PrefixInvalidationReason{
		contexty.PrefixContentChanged, contexty.PrefixOrderChanged, contexty.PrefixCompactionChanged,
	}, report.Invalidations[0].Reasons)
	require.Equal(t, 1, calls)
	require.Equal(t, history, compiled.Source.History)
	require.Equal(t, fact, *report.Manifest.Boundaries[0].Messages[0].Compaction)
}

func TestPrefix_NativeOffload(t *testing.T) {
	// Arrange: a confirmed historical argument projection retains the original ID and metadata.
	request, prepared := fixturePreparedHistoricalArguments(t)
	recipe := fixturePrefixRecipe()
	recipe.Codec = request.Codec
	recipe.Boundaries = []contexty.PrefixBoundary{{ID: "completed-round", AfterMessageID: request.History[1].ID}}
	previous, err := contexty.BuildPrefixManifest(context.Background(), request.History, recipe)
	require.NoError(t, err)
	engine := fixtureEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory,
		contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
			contexty.CharTokenEstimator{})))
	compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		History: request.History,
		Options: []contexty.CompileOption{contexty.WithHistoricalArgumentProjection(prepared)},
	})
	require.NoError(t, err)
	fact, err := contexty.BlobDescriptorRef(prepared.Reference.Object)
	require.NoError(t, err)
	recipe.Evidence = []contexty.PrefixProjectionEvidence{{MessageID: request.MessageID, Offload: &fact}}
	// Act: diagnose actual compiled preview bytes, without blob retrieval or original argument mutation.
	report, err := contexty.DiagnosePrefix(context.Background(), compiled.Payload.FlattenMessages(), recipe, &previous)
	// Assert: offload and full content identity change, not message order or persistence.
	require.NoError(t, err)
	require.Equal(t, "completed-round", report.FirstAffectedBoundary)
	require.Equal(t, []contexty.PrefixInvalidationReason{
		contexty.PrefixContentChanged, contexty.PrefixOffloadChanged,
	}, report.Invalidations[0].Reasons)
	require.Equal(t, request.History, compiled.DerivePersistenceProjection(contexty.SegmentHistory))
	require.Equal(t, request.History, compiled.Source.History)
	require.Equal(t, fact, *report.Manifest.Boundaries[0].Messages[0].Offload)
}
