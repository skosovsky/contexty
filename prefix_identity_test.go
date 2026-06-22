package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestPrefix_Identity(t *testing.T) {
	// Arrange: two exact prefix boundaries followed by variable content.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	first, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
	require.NoError(t, err)
	messages[2].Parts = []contexty.ContentPart{contexty.TextPart{Text: "variable tail changed"}}
	// Act: tail mutation cannot change either prefix or manifest identity.
	second, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.NoError(t, first.Validate())
	messages[1].LLMCache = &contexty.CachePolicyRef{Type: "wire-affecting"}
	third, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
	require.NoError(t, err)
	require.Equal(t, first.Boundaries[0].Digest, third.Boundaries[0].Digest)
	require.NotEqual(t, first.Boundaries[1].Digest, third.Boundaries[1].Digest)
	require.NotEqual(t, first.Digest, third.Digest)
	third.Boundaries[1].Messages[0].Content.Digest = first.Boundaries[1].Messages[1].Content.Digest
	require.ErrorIs(t, third.Validate(), contexty.ErrInvalidPrefixManifest)
}

func TestPrefix_IdentityAdmission(t *testing.T) {
	// Arrange: host denies a private message before semantic encoding.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	calls := 0
	recipe.Authorize = func(_ context.Context, msg contexty.Message) error {
		calls++
		msg.Parts[0] = contexty.TextPart{Text: "owned callback input"}
		return contexty.ErrPrefixAdmission
	}
	// Act.
	manifest, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
	// Assert: failure yields no identity and never mutates caller content.
	require.ErrorIs(t, err, contexty.ErrPrefixAdmission)
	require.Zero(t, manifest)
	require.Equal(t, 1, calls)
	require.Equal(t, "policy", messages[0].TextContent())
	ctx, cancel := context.WithCancel(context.Background())
	recipe.Authorize = func(context.Context, contexty.Message) error {
		cancel()
		return contexty.ErrPrefixAdmission
	}
	manifest, err = contexty.BuildPrefixManifest(ctx, messages, recipe)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, manifest)
}

func TestPrefix_IdentityInvalidBoundary(t *testing.T) {
	for _, boundaries := range [][]contexty.PrefixBoundary{
		nil,
		{{ID: "missing", AfterMessageID: "absent"}},
		{{ID: "reversed", AfterMessageID: "reference"}, {ID: "earlier", AfterMessageID: "policy"}},
		{{ID: "duplicate", AfterMessageID: "policy"}, {ID: "duplicate", AfterMessageID: "reference"}},
	} {
		// Arrange: malformed declarations must fail before host callbacks.
		recipe := fixturePrefixRecipe()
		recipe.Boundaries = boundaries
		recipe.Authorize = func(context.Context, contexty.Message) error {
			t.Fatal("invalid boundary must fail before admission")
			return nil
		}
		// Act.
		manifest, err := contexty.BuildPrefixManifest(context.Background(), fixturePrefixMessages(), recipe)
		// Assert.
		require.ErrorIs(t, err, contexty.ErrInvalidPrefixBoundary)
		require.Zero(t, manifest)
	}
}

func TestPrefix_IdentityMetadata(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*contexty.PrefixRecipe, []contexty.Message)
	}{
		{name: "renderer", change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			recipe.Renderer.Revision = "changed"
		}},
		{name: "encoding", change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			recipe.Encoding.Revision = "changed"
		}},
		{name: "policy", change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			recipe.Policy.Revision = "changed"
		}},
		{name: "role", change: func(_ *contexty.PrefixRecipe, messages []contexty.Message) {
			messages[0].Role = contexty.RoleUser
		}},
		{name: "extension", change: func(_ *contexty.PrefixRecipe, messages []contexty.Message) {
			messages[0].Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"label":"host-private"}`}}
		}},
		{name: "compaction", change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			ref := fixtureRef(t, "summary-fact", "accepted projection")
			recipe.Evidence = []contexty.PrefixProjectionEvidence{{MessageID: "policy", Compaction: &ref}}
		}},
		{name: "offload", change: func(recipe *contexty.PrefixRecipe, _ []contexty.Message) {
			ref := fixtureRef(t, "offload-fact", "confirmed projection")
			recipe.Evidence = []contexty.PrefixProjectionEvidence{{MessageID: "policy", Offload: &ref}}
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: pin the same baseline independently for every semantic change.
			messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
			baseline, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
			require.NoError(t, err)
			scenario.change(&recipe, messages)
			// Act.
			changed, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
			// Assert: full typed metadata and projection facts affect both prefixes.
			require.NoError(t, err)
			require.NotEqual(t, baseline.Boundaries[0].Digest, changed.Boundaries[0].Digest)
			require.NotEqual(t, baseline.Boundaries[1].Digest, changed.Boundaries[1].Digest)
			require.NoError(t, changed.Validate())
		})
	}
}

func TestPrefix_IdentityFreshAuthorization(t *testing.T) {
	// Arrange: only the two prefix messages require admission, on every run.
	recipe := fixturePrefixRecipe()
	calls := 0
	recipe.Authorize = func(_ context.Context, msg contexty.Message) error {
		require.NotEqual(t, "tail", msg.ID)
		calls++
		return nil
	}
	// Act.
	first, err := contexty.BuildPrefixManifest(context.Background(), fixturePrefixMessages(), recipe)
	require.NoError(t, err)
	second, err := contexty.BuildPrefixManifest(context.Background(), fixturePrefixMessages(), recipe)
	// Assert: identity equality never bypasses fresh host checks.
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 4, calls)
	recipe.RequiredHints = []string{"unsupported"}
	recipe.Authorize = func(context.Context, contexty.Message) error {
		t.Fatal("unsupported required hint must fail before callbacks")
		return nil
	}
	result, err := contexty.BuildPrefixManifest(context.Background(), fixturePrefixMessages(), recipe)
	require.ErrorIs(t, err, contexty.ErrPrefixRequiredHint)
	require.Zero(t, result)
}
