package contexty_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestPrefix_Constraints(t *testing.T) {
	for _, denial := range []error{errors.New("host freshness expired"), contexty.ErrBudgetExceeded, contexty.ErrPrefixAdmission} {
		t.Run(denial.Error(), func(t *testing.T) {
			// Arrange: an equal previous digest cannot permit stale/overbudget/private reuse.
			messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
			encodes := 0
			messages[0].Extensions = []contexty.Extension{fixturePrefixExtension{encodes: &encodes}}
			previous, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
			require.NoError(t, err)
			encodes = 0
			recipe.Authorize = func(context.Context, contexty.Message) error { return denial }
			// Act.
			result, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &previous)
			// Assert: no encoding, stable claim or partial result after host rejection.
			require.ErrorIs(t, err, denial)
			require.Zero(t, result)
			require.Zero(t, encodes)
		})
	}
}

func TestPrefix_CodecCancellation(t *testing.T) {
	// Arrange: a host codec cancels with a concurrent error during the first message.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	encodes, admissions := 0, 0
	messages[0].Extensions = []contexty.Extension{fixturePrefixExtension{encodes: &encodes, cancel: cancel}}
	recipe.Authorize = func(context.Context, contexty.Message) error { admissions++; return nil }
	// Act.
	result, err := contexty.DiagnosePrefix(ctx, messages, recipe, nil)
	// Assert: cancellation dominates codec failure and stops later host callbacks.
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	require.Equal(t, 1, encodes)
	require.Equal(t, 1, admissions)
}

func TestPrefix_InputOwnership(t *testing.T) {
	// Arrange: a host callback mutates original configuration after the preflight snapshot.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	previous, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
	require.NoError(t, err)
	baseline := previous.Digest
	recipe.Authorize = func(context.Context, contexty.Message) error {
		previous.Digest = "host mutation"
		recipe.Boundaries[1].AfterMessageID = "absent"
		messages[1].Parts = []contexty.ContentPart{contexty.TextPart{Text: "host mutation"}}
		return nil
	}
	// Act.
	result, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &previous)
	// Assert: original values were owned before the first callback, including prior state.
	require.NoError(t, err)
	require.Equal(t, baseline, result.Manifest.Digest)
	require.Empty(t, result.Invalidations)
	require.NoError(t, result.Manifest.Validate())
	result.Manifest.Boundaries[0].Messages[0].Content.ID = "changed returned boundary"
	require.Equal(t, "policy", result.Manifest.Boundaries[1].Messages[0].Content.ID)
}

func TestPrefix_PolicyAndManifestCodec(t *testing.T) {
	// Arrange: canonical capabilities and message hints are both policy input.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	recipe.SupportedHints = []string{"optional", "supported", "optional"}
	previous, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
	require.NoError(t, err)
	wire, err := json.Marshal(previous)
	require.NoError(t, err)
	var decoded contexty.PrefixManifest
	require.NoError(t, json.Unmarshal(wire, &decoded))
	require.NoError(t, decoded.Validate())
	messages[1].LLMCache = &contexty.CachePolicyRef{Type: "supported"}
	// Act.
	report, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &decoded)
	// Assert: metadata mutation is both full typed content and policy invalidation.
	require.NoError(t, err)
	require.Equal(t, []contexty.PrefixInvalidationReason{
		contexty.PrefixContentChanged, contexty.PrefixPolicyChanged,
	}, report.Invalidations[0].Reasons)
	require.Equal(t, "static-reference", report.FirstAffectedBoundary)
	decoded.RequiredHints = []string{"unsupported"}
	result, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &decoded)
	require.ErrorIs(t, err, contexty.ErrInvalidPrefixManifest)
	require.Zero(t, result)
}

func TestPrefix_WireFailures(t *testing.T) {
	for _, scenario := range []string{"missing boundary", "renderer", "noncanonical digest", "duplicate", "old invalid fact"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: no stale or malformed adapter assertion may enter the report.
			report, err := contexty.DiagnosePrefix(
				context.Background(),
				fixturePrefixMessages(),
				fixturePrefixRecipe(),
				nil,
			)
			require.NoError(t, err)
			confirmation := contexty.PrefixWireConfirmation{
				BoundaryID: "static-policy", SemanticDigest: report.Manifest.Boundaries[0].Digest,
				Renderer: report.Manifest.Renderer, WireDigest: fixtureRef(t, "wire", "prefix bytes").Digest,
			}
			switch scenario {
			case "missing boundary":
				confirmation.BoundaryID = "absent"
			case "renderer":
				confirmation.Renderer.Revision = "different"
			case "noncanonical digest":
				confirmation.WireDigest = "DEADBEEF"
			case "duplicate":
				report.WireConfirmations = []contexty.PrefixWireConfirmation{confirmation}
			case "old invalid fact":
				invalid := confirmation
				invalid.SemanticDigest = "stale"
				report.WireConfirmations = []contexty.PrefixWireConfirmation{invalid}
			}
			// Act.
			result, err := contexty.WithPrefixWireConfirmation(report, confirmation)
			// Assert.
			require.ErrorIs(t, err, contexty.ErrPrefixWireMismatch)
			require.Zero(t, result)
		})
	}
}

func TestPrefix_SummaryReplacement(t *testing.T) {
	// Arrange: replace the original reference with an accepted summary projection.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	previous, err := contexty.BuildPrefixManifest(context.Background(), messages, recipe)
	require.NoError(t, err)
	messages[1].ID = "summary"
	messages[1].Parts = []contexty.ContentPart{contexty.TextPart{Text: "accepted condensed content"}}
	recipe.Boundaries[1].AfterMessageID = "summary"
	ref := fixtureRef(t, "compaction", "accepted record")
	recipe.Evidence = []contexty.PrefixProjectionEvidence{{MessageID: "summary", Compaction: &ref}}
	// Act.
	result, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &previous)
	// Assert: a summary never masquerades as the original prefix.
	require.NoError(t, err)
	require.Equal(t, "static-reference", result.FirstAffectedBoundary)
	require.Equal(t, []contexty.PrefixInvalidationReason{
		contexty.PrefixContentChanged, contexty.PrefixOrderChanged, contexty.PrefixCompactionChanged,
	}, result.Invalidations[0].Reasons)
	require.NotEqual(t, previous.Boundaries[1].Digest, result.Manifest.Boundaries[1].Digest)
}
