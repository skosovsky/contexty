package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestPrefix_TailIdentity(t *testing.T) {
	// Arrange: identical instructions, different questions.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	first, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, nil)
	require.NoError(t, err)
	messages[2].Parts = []contexty.ContentPart{contexty.TextPart{Text: "different question"}}
	// Act.
	second, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &first.Manifest)
	// Assert: prefix equality does not imply tail equality or bypass host checks.
	require.NoError(t, err)
	require.Equal(t, first.Manifest, second.Manifest)
	require.Empty(t, second.Invalidations)
	require.NotEmpty(t, first.TailDigest)
	require.NotEqual(t, first.TailDigest, second.TailDigest)
	repeated, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &second.Manifest)
	require.NoError(t, err)
	require.Equal(t, second.TailDigest, repeated.TailDigest)
	msg := contexty.TextMessage(contexty.RoleUser, "additional tail")
	msg.ID = "tail-next"
	messages = append(messages, msg)
	third, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &second.Manifest)
	require.NoError(t, err)
	require.Equal(t, second.Manifest, third.Manifest)
	require.NotEqual(t, second.TailDigest, third.TailDigest)
	messages[2], messages[3] = messages[3], messages[2]
	fourth, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &third.Manifest)
	require.NoError(t, err)
	require.Equal(t, third.Manifest, fourth.Manifest)
	require.NotEqual(t, third.TailDigest, fourth.TailDigest)
}

func TestPrefix_TailAdmission(t *testing.T) {
	// Arrange: host allows the prefix but denies private tail before its codec runs.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	encodes, admissions := 0, 0
	messages[2].Extensions = []contexty.Extension{fixturePrefixExtension{encodes: &encodes}}
	recipe.Authorize = func(_ context.Context, msg contexty.Message) error {
		admissions++
		if msg.ID == "tail" {
			return contexty.ErrPrefixAdmission
		}
		return nil
	}
	// Act.
	result, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, nil)
	// Assert: no tail serialization or partial prefix report escapes denial.
	require.ErrorIs(t, err, contexty.ErrPrefixAdmission)
	require.Zero(t, result)
	require.Zero(t, encodes)
	require.Equal(t, 3, admissions)
}

func TestPrefix_EmptyTail(t *testing.T) {
	// Arrange: final boundary terminates the complete request.
	full := fixturePrefixMessages()
	messages, recipe := full[:2], fixturePrefixRecipe()
	// Act.
	first, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, nil)
	require.NoError(t, err)
	second, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &first.Manifest)
	// Assert: empty content still has deterministic identity and no invalidation.
	require.NoError(t, err)
	require.NotEmpty(t, first.TailDigest)
	require.Equal(t, first.TailDigest, second.TailDigest)
	require.Empty(t, second.Invalidations)
}

func TestPrefix_TailOwnership(t *testing.T) {
	// Arrange: mutate the original tail while the prefix is being admitted.
	messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
	baseline, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, nil)
	require.NoError(t, err)
	recipe.Authorize = func(context.Context, contexty.Message) error {
		messages[2].Parts = []contexty.ContentPart{contexty.TextPart{Text: "PRIVATE-TAIL-MUTATION"}}
		messages[2].Role = contexty.RoleAssistant
		return nil
	}
	// Act: all current input was frozen before the prefix callback could mutate it.
	report, err := contexty.DiagnosePrefix(context.Background(), messages, recipe, &baseline.Manifest)
	// Assert: both identities reflect the same original invocation snapshot.
	require.NoError(t, err)
	require.Equal(t, baseline.Manifest, report.Manifest)
	require.Equal(t, baseline.TailDigest, report.TailDigest)
	wire, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "PRIVATE-TAIL-MUTATION")
	changed, err := contexty.DiagnosePrefix(context.Background(), messages, fixturePrefixRecipe(), &report.Manifest)
	require.NoError(t, err)
	require.Equal(t, report.Manifest, changed.Manifest)
	require.NotEqual(t, report.TailDigest, changed.TailDigest)
}

func TestPrefix_TailCancellation(t *testing.T) {
	for _, codecCancel := range []bool{false, true} {
		// Arrange: cancellation during tail admission/encoding dominates host errors.
		ctx, cancel := context.WithCancel(context.Background())
		messages, recipe := fixturePrefixMessages(), fixturePrefixRecipe()
		encodes := 0
		if codecCancel {
			messages[2].Extensions = []contexty.Extension{fixturePrefixExtension{encodes: &encodes, cancel: cancel}}
		} else {
			recipe.Authorize = func(_ context.Context, msg contexty.Message) error {
				if msg.ID == "tail" {
					cancel()
					return contexty.ErrPrefixAdmission
				}
				return nil
			}
		}
		// Act.
		result, err := contexty.DiagnosePrefix(ctx, messages, recipe, nil)
		cancel()
		// Assert: the already-built prefix does not escape as a partial success.
		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, result)
		if codecCancel {
			require.Equal(t, 1, encodes)
		} else {
			require.Zero(t, encodes)
		}
	}
}
