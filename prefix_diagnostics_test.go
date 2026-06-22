package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestPrefix_Comparison(t *testing.T) {
	// Arrange: previous identity is explicit, not a permission to skip admission.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	initial, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, nil)
	require.NoError(t, err)
	require.False(t, initial.Compared)
	require.Empty(t, initial.Invalidations)
	// Act: an unchanged run and a change after the first boundary.
	same, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &initial.Manifest)
	require.NoError(t, err)
	messages[1].Parts = []contexty.ContentPart{contexty.TextPart{Text: "new reference"}}
	changed, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &initial.Manifest)
	// Assert: first policy prefix remains identical; only the second is affected.
	require.NoError(t, err)
	require.True(t, same.Compared)
	require.Empty(t, same.Invalidations)
	require.Empty(t, same.FirstAffectedBoundary)
	require.Equal(t, "static-reference", changed.FirstAffectedBoundary)
	require.Len(t, changed.Invalidations, 1)
	require.Equal(
		t,
		[]contexty.PrefixInvalidationReason{contexty.PrefixContentChanged},
		changed.Invalidations[0].Reasons,
	)
	require.False(t, changed.Invalidations[0].Removed)
}

func TestPrefix_Invalidation(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*contexty.PrefixRecipe, []contexty.Message)
		reason contexty.PrefixInvalidationReason
	}{
		{name: "content", reason: contexty.PrefixContentChanged, change: func(_ *contexty.PrefixRecipe, msgs []contexty.Message) {
			msgs[1].Parts = []contexty.ContentPart{contexty.TextPart{Text: "different"}}
		}},
		{name: "order", reason: contexty.PrefixOrderChanged, change: func(recipe *contexty.PrefixRecipe, msgs []contexty.Message) {
			msgs[0], msgs[1] = msgs[1], msgs[0]
			recipe.Boundaries = recipe.Boundaries[1:]
			recipe.Boundaries[0].AfterMessageID = "policy"
		}},
		{name: "policy", reason: contexty.PrefixPolicyChanged, change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			recipe.Policy.Revision = "updated"
		}},
		{name: "codec", reason: contexty.PrefixCodecChanged, change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			recipe.Encoding.Revision = "updated"
		}},
		{name: "compaction", reason: contexty.PrefixCompactionChanged, change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			ref := fixtureRef(t, "summary", "accepted summary")
			recipe.Evidence = []contexty.PrefixProjectionEvidence{{MessageID: "reference", Compaction: &ref}}
		}},
		{name: "offload", reason: contexty.PrefixOffloadChanged, change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			ref := fixtureRef(t, "offload", "confirmed preview")
			recipe.Evidence = []contexty.PrefixProjectionEvidence{{MessageID: "reference", Offload: &ref}}
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange.
			messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
			previous, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
			require.NoError(t, err)
			scenario.change(&recipe, messages)
			// Act.
			changed, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &previous)
			// Assert: report the actual cause, not a generic cache miss.
			require.NoError(t, err)
			require.NotEmpty(t, changed.Invalidations)
			require.Contains(t, changed.Invalidations[0].Reasons, scenario.reason)
		})
	}
}

func TestPrefix_RemovedBoundary(t *testing.T) {
	// Arrange: delete an earlier boundary without changing any prompt bytes.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	previous, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
	require.NoError(t, err)
	recipe.Boundaries = recipe.Boundaries[1:]
	// Act.
	changed, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &previous)
	// Assert: surviving prefix is identical, but the removed boundary is explicit.
	require.NoError(t, err)
	require.Equal(t, "static-policy", changed.FirstAffectedBoundary)
	require.Len(t, changed.Invalidations, 1)
	require.True(t, changed.Invalidations[0].Removed)
	require.Equal(t, []contexty.PrefixInvalidationReason{contexty.PrefixOrderChanged}, changed.Invalidations[0].Reasons)
	require.Equal(t, previous.Boundaries[1].Digest, changed.Manifest.Boundaries[0].Digest)
}

func TestPrefix_RendererMismatch(t *testing.T) {
	// Arrange: incompatibility must not claim stable rendering or call the host.
	recipe := fixturePrefixRecipe()
	previous, err := contexty.BuildPrefixManifest(context.Background(), fixturePrefixMessages(), recipe)
	require.NoError(t, err)
	recipe.Renderer.Revision = "incompatible"
	recipe.Authorize = func(context.Context, contexty.Message) error {
		t.Fatal("renderer mismatch must fail before callbacks")
		return nil
	}
	// Act.
	result, err := contexty.DiagnosePrefix(context.Background(), fixturePrefixMessages(), recipe, &previous)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrPrefixRendererMismatch)
	require.Zero(t, result)
	previous.Digest = "tampered"
	result, err = contexty.DiagnosePrefix(context.Background(), fixturePrefixMessages(), recipe, &previous)
	require.ErrorIs(t, err, contexty.ErrInvalidPrefixManifest)
	require.Zero(t, result)
}

func TestPrefix_WireConfirmation(t *testing.T) {
	// Arrange: adapter checks and hashes its own actual prefix bytes.
	report, err := contexty.DiagnosePrefix(context.Background(), fixturePrefixMessages(), fixturePrefixRecipe(), nil)
	require.NoError(t, err)
	confirmation := contexty.PrefixWireConfirmation{
		BoundaryID: "static-reference", SemanticDigest: report.Manifest.Boundaries[1].Digest,
		Renderer: report.Manifest.Renderer, WireDigest: fixtureRef(t, "wire", "adapter wire").Digest,
	}
	// Act.
	confirmed, err := contexty.WithPrefixWireConfirmation(report, confirmation)
	// Assert: semantic identity is untouched and outputs are independently owned.
	require.NoError(t, err)
	require.Equal(t, report.Manifest, confirmed.Manifest)
	require.Empty(t, report.WireConfirmations)
	require.Equal(t, []contexty.PrefixWireConfirmation{confirmation}, confirmed.WireConfirmations)
	confirmed.Manifest.Boundaries[0].Messages[0].Content.ID = "mutated output"
	require.NoError(t, report.Manifest.Validate())
	again, err := contexty.DiagnosePrefix(
		context.Background(),
		fixturePrefixMessages(),
		fixturePrefixRecipe(),
		&report.Manifest,
	)
	require.NoError(t, err)
	require.Empty(t, again.WireConfirmations)
	confirmation.SemanticDigest = fixtureRef(t, "stale", "old content").Digest
	invalid, err := contexty.WithPrefixWireConfirmation(report, confirmation)
	require.ErrorIs(t, err, contexty.ErrPrefixWireMismatch)
	require.Zero(t, invalid)
}
