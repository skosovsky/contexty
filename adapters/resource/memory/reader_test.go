package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/resource/memory"
)

func resourceFixture(t *testing.T, id string) (contexty.ResourceBody, contexty.ResourceReadRequest) {
	t.Helper()
	body := contexty.ResourceBody{Reference: contexty.Descriptor{ID: id, Revision: "pinned"},
		Artifact: contexty.NewRetrievalDocument("body-"+id, contexty.TextPayload("private-"+id)).ContextArtifact}
	body.Artifact.SourceRefs = []contexty.SourceRef{{ID: id}}
	descriptor, err := contexty.DescribeResource(body.Reference, "same name", body.Artifact)
	require.NoError(t, err)
	return body, contexty.ResourceReadRequest{ScopeRef: "fresh", Resource: descriptor, MaxBytes: descriptor.Length}
}

func TestReaderSelectionAndOwnership(t *testing.T) {
	// Arrange: identical display names, separate opaque references and original bytes.
	first, firstRequest := resourceFixture(t, "first")
	second, secondRequest := resourceFixture(t, "second")
	var authorized []string
	reader, err := memory.New(memory.Config{MaxBodyBytes: firstRequest.MaxBytes + secondRequest.MaxBytes,
		Authorize: func(_ context.Context, request contexty.ResourceReadRequest) error {
			authorized = append(authorized, request.Resource.Reference.ID)
			return nil
		}}, first, second)
	require.NoError(t, err)
	first.Artifact.Payload.Text = "caller-mutated"
	first.Artifact.SourceRefs[0].ID = "caller-mutated"
	// Act: host selects only the second descriptor; changing its display name is irrelevant.
	secondRequest.Resource.Name = "renamed"
	selected, err := reader.ReadResource(context.Background(), secondRequest)
	// Assert: no lookup-by-name, no other body loaded; returned containers are independent.
	require.NoError(t, err)
	require.Equal(t, second, selected)
	require.Equal(t, []string{"second"}, authorized)
	selected.Artifact.Payload.Text = "returned-mutated"
	selected.Artifact.SourceRefs[0].ID = "returned-mutated"
	reloaded, err := reader.ReadResource(context.Background(), secondRequest)
	require.NoError(t, err)
	require.Equal(t, second, reloaded)
	original, err := reader.ReadResource(context.Background(), firstRequest)
	require.NoError(t, err)
	require.Equal(t, "private-first", original.Artifact.Payload.Text)
	require.Equal(t, "first", original.Artifact.SourceRefs[0].ID)
}

func TestReaderFailures(t *testing.T) {
	for _, scenario := range []string{"denied", "missing", "revision", "digest", "bytes", "scope", "canceled", "callback-canceled"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: failures must never return raw or partial content.
			body, request := resourceFixture(t, "selected")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			reader, err := memory.New(memory.Config{MaxBodyBytes: request.MaxBytes,
				Authorize: func(context.Context, contexty.ResourceReadRequest) error {
					calls++
					if scenario == "callback-canceled" {
						cancel()
						return contexty.ErrResourceDenied
					}
					if scenario == "denied" {
						return contexty.ErrResourceDenied
					}
					return nil
				}}, body)
			require.NoError(t, err)
			expected := contexty.ErrResourceMismatch
			switch scenario {
			case "denied":
				expected = contexty.ErrResourceDenied
			case "missing":
				request.Resource.Reference.ID = "missing"
				expected = contexty.ErrResourceMissing
			case "revision":
				request.Resource.Reference.Revision = "changed"
			case "digest":
				request.Resource.Content.Digest = "0000000000000000000000000000000000000000000000000000000000000000"
			case "bytes":
				request.MaxBytes--
				expected = contexty.ErrResourceSizeLimit
			case "scope":
				request.ScopeRef = ""
				expected = contexty.ErrResourceDenied
			case "canceled":
				cancel()
				expected = context.Canceled
			case "callback-canceled":
				expected = context.Canceled
			}
			// Act / Assert.
			result, readErr := reader.ReadResource(ctx, request)
			require.ErrorIs(t, readErr, expected)
			require.Zero(t, result)
			if scenario == "canceled" || scenario == "scope" || scenario == "bytes" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestReaderConfiguration(t *testing.T) {
	// Arrange: application owns input snapshots and authorization configuration.
	body, request := resourceFixture(t, "selected")
	allow := func(context.Context, contexty.ResourceReadRequest) error { return nil }
	// Act / Assert: no permissive default authorization or ambiguous identity.
	_, err := memory.New(memory.Config{MaxBodyBytes: request.MaxBytes})
	require.ErrorIs(t, err, memory.ErrConfiguration)
	_, err = memory.New(memory.Config{MaxBodyBytes: -1, Authorize: allow})
	require.ErrorIs(t, err, memory.ErrConfiguration)
	_, err = memory.New(memory.Config{MaxBodyBytes: request.MaxBytes - 1, Authorize: allow}, body)
	require.ErrorIs(t, err, contexty.ErrResourceSizeLimit)
	_, err = memory.New(memory.Config{MaxBodyBytes: request.MaxBytes, Authorize: allow}, body, body)
	require.ErrorIs(t, err, memory.ErrConfiguration)
	body.Artifact.Kind = contexty.ArtifactKind("unsupported")
	_, err = memory.New(memory.Config{MaxBodyBytes: request.MaxBytes, Authorize: allow}, body)
	require.ErrorIs(t, err, contexty.ErrResourceUnsupported)
	var missing *memory.Reader
	result, err := missing.ReadResource(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrResourceDenied)
	require.Zero(t, result)
}
