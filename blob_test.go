package contexty_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestBlob_Storage(t *testing.T) {
	// Arrange: backend retains its own mutable receipt/content containers.
	request := fixtureBlobRequest(t)
	receipt := fixtureBlobReceipt(request)
	backend := request.Content
	store := fixtureBlobStore{
		put: func(_ context.Context, received contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
			require.Equal(t, request, received)
			received.Content.Bytes[0] = 'X'
			received.Sources[0].ID = "mutated"
			return receipt, nil
		},
		get: func(_ context.Context, received contexty.BlobGetRequest) (contexty.BlobContent, error) {
			require.Equal(t, "fresh-read-scope", received.ScopeRef)
			require.Equal(t, receipt.Object, received.Object)
			require.EqualValues(t, len(backend.Bytes), received.MaxBytes)
			return backend, nil
		},
	}
	// Act.
	outcome, err := contexty.PutBlob(context.Background(), store, request)
	require.NoError(t, err)
	content, err := contexty.ResolveBlob(context.Background(), store, "fresh-read-scope", *outcome.Published,
		contexty.BlobLimits{MaxBytes: int64(len(backend.Bytes)), AllowedMediaTypes: []string{fixtureBlobMIME}})
	// Assert: only confirmed refs are exposed, byte scopes aren't permissions.
	require.NoError(t, err)
	require.Nil(t, outcome.Cleanup)
	require.Equal(t, request.Content, content)
	require.Equal(t, "payload", string(request.Content.Bytes))
	require.Equal(t, "source", request.Sources[0].ID)
	receipt.Sources[0].ID = "later-mutation"
	require.Equal(t, "source", outcome.Published.Sources[0].ID)
	content.Bytes[0] = 'Y'
	require.Equal(t, "payload", string(backend.Bytes))
}

func TestBlob_PutFailures(t *testing.T) {
	for _, scenario := range []string{"failure", "partial-failure", "invalid-receipt", "canceled-after-put", "empty-scope"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: failed or unvalidated writes cannot publish a durable ref.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := fixtureBlobRequest(t)
			failure := errors.New("host Put failure")
			calls := 0
			store := fixtureBlobStore{
				put: func(_ context.Context, received contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
					calls++
					receipt := fixtureBlobReceipt(received)
					switch scenario {
					case "failure":
						return contexty.BlobDescriptor{}, failure
					case "partial-failure":
						return receipt, failure
					case "invalid-receipt":
						receipt.Length++
					case "canceled-after-put":
						cancel()
					}
					return receipt, nil
				},
			}
			if scenario == "empty-scope" {
				request.ScopeRef = ""
			}
			// Act.
			outcome, err := contexty.PutBlob(ctx, store, request)
			// Assert: known writes remain explicit cleanup intents, not lost references.
			require.Error(t, err)
			require.Nil(t, outcome.Published)
			if scenario == "failure" || scenario == "empty-scope" {
				require.Nil(t, outcome.Cleanup)
			} else {
				require.NotNil(t, outcome.Cleanup)
				require.Equal(t, "retention", outcome.Cleanup.RetentionRef)
			}
			if scenario == "empty-scope" {
				require.Zero(t, calls)
				require.ErrorIs(t, err, contexty.ErrInvalidBlob)
			}
			if scenario == "canceled-after-put" {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestBlob_Failures(t *testing.T) {
	for _, scenario := range []struct {
		name string
		want error
	}{
		{name: "missing", want: contexty.ErrBlobMissing},
		{name: "expired", want: contexty.ErrBlobExpired},
		{name: "denied", want: contexty.ErrBlobDenied},
		{name: "size", want: contexty.ErrBlobSizeLimit},
		{name: "media", want: contexty.ErrBlobMediaLimit},
		{name: "digest", want: contexty.ErrBlobDigestMismatch},
		{name: "canceled", want: context.Canceled},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := fixtureBlobRequest(t)
			descriptor := fixtureBlobReceipt(request)
			store := fixtureBlobStore{
				get: func(context.Context, contexty.BlobGetRequest) (contexty.BlobContent, error) {
					content := contexty.BlobContent{
						MIMEType: fixtureBlobMIME,
						Bytes:    append([]byte(nil), request.Content.Bytes...),
					}
					switch scenario.name {
					case "missing", "expired", "denied":
						return contexty.BlobContent{}, scenario.want
					case "size":
						content.Bytes = append(content.Bytes, 'x')
					case "media":
						content.MIMEType = "application/json"
					case "digest":
						content.Bytes[0] = 'X'
					case "canceled":
						cancel()
					}
					return content, nil
				},
			}
			// Act.
			content, err := contexty.ResolveBlob(ctx, store, "read-scope", descriptor,
				contexty.BlobLimits{MaxBytes: descriptor.Length, AllowedMediaTypes: []string{fixtureBlobMIME}})
			// Assert: missing/auth/expiry/digest outcomes are not successful partial reads.
			require.ErrorIs(t, err, scenario.want)
			require.Zero(t, content)
		})
	}
}

func TestLarge_PayloadMeasurement(t *testing.T) {
	// Arrange: deterministic large-output fixture, not a claim about production traffic.
	text := strings.Repeat("document-line\n", 32768)
	message := contexty.TextMessage(contexty.RoleUser, text)
	message.ID = "large-payload"
	// Act.
	tokens, err := (contexty.CharTokenEstimator{}).Estimate(context.Background(), []contexty.Message{message})
	// Assert: fixture dominates a bounded preview and motivates the optional offload stage.
	require.NoError(t, err)
	require.Greater(t, len(text), 400000)
	require.Greater(t, tokens, 100000)
	t.Logf("synthetic payload: bytes=%d approximate_tokens=%d", len(text), tokens)
}

func TestBlob_PreflightLimits(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		limit contexty.BlobLimits
		want  error
	}{
		{name: "byte-limit", limit: contexty.BlobLimits{MaxBytes: 3, AllowedMediaTypes: []string{fixtureBlobMIME}}, want: contexty.ErrBlobSizeLimit},
		{name: "media-limit", limit: contexty.BlobLimits{MaxBytes: 100}, want: contexty.ErrBlobMediaLimit},
		{name: "negative-limit", limit: contexty.BlobLimits{MaxBytes: -1}, want: contexty.ErrInvalidBlob},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: limits must be enforced before even asking the backend to read.
			descriptor := fixtureBlobReceipt(fixtureBlobRequest(t))
			reads := 0
			store := fixtureBlobStore{
				get: func(context.Context, contexty.BlobGetRequest) (contexty.BlobContent, error) {
					reads++
					return contexty.BlobContent{}, errors.New("unexpected read")
				},
			}
			// Act.
			content, err := contexty.ResolveBlob(context.Background(), store, "read-scope", descriptor, scenario.limit)
			// Assert.
			require.ErrorIs(t, err, scenario.want)
			require.Zero(t, content)
			require.Zero(t, reads)
		})
	}
}

func TestBlob_DescriptorCodec(t *testing.T) {
	// Arrange: descriptor wire contains only immutable ref metadata, never the payload.
	descriptor := fixtureBlobReceipt(fixtureBlobRequest(t))
	// Act.
	wire, err := contexty.EncodeBlobDescriptor(descriptor)
	require.NoError(t, err)
	restored, err := contexty.DecodeBlobDescriptor(wire)
	// Assert: codec requires no storage port and preserves source revisions.
	require.NoError(t, err)
	require.Equal(t, descriptor, restored)
	require.NotContains(t, string(wire), "payload")
	restored.Sources[0].ID = "mutated"
	require.Equal(t, "source", descriptor.Sources[0].ID)
	for _, invalid := range [][]byte{[]byte(`{}`), []byte(`null`), []byte(`{"object":`)} {
		// Arrange / Act / Assert: malformed or incomplete wire is never a successful ref.
		decoded, decodeErr := contexty.DecodeBlobDescriptor(invalid)
		require.ErrorIs(t, decodeErr, contexty.ErrInvalidBlob)
		require.Zero(t, decoded)
	}
}

func TestBlob_NilPort(t *testing.T) {
	// Arrange: a typed nil implementation is absent, not a usable host port.
	var storage *fixtureBlobStore
	request := fixtureBlobRequest(t)
	// Act.
	outcome, putErr := contexty.PutBlob(context.Background(), storage, request)
	content, readErr := contexty.ResolveBlob(context.Background(), storage, "scope", fixtureBlobReceipt(request),
		contexty.BlobLimits{MaxBytes: 100, AllowedMediaTypes: []string{fixtureBlobMIME}})
	// Assert: no method dispatch or panic on the typed nil receiver.
	require.ErrorIs(t, putErr, contexty.ErrInvalidBlob)
	require.Zero(t, outcome)
	require.ErrorIs(t, readErr, contexty.ErrInvalidBlob)
	require.Zero(t, content)
}
