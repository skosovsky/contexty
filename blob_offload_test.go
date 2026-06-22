package contexty_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestBlob_ThresholdBoundaries(t *testing.T) {
	for _, size := range []int{3, 4, 10, 11} {
		t.Run(strings.Repeat("x", size), func(t *testing.T) {
			// Arrange: thresholds are copied, preview is explicitly host-rendered.
			limits := contexty.BlobThresholdLimits{MaxInlineBytes: 3, MaxBlobBytes: 10}
			previews, puts := 0, 0
			policy, err := contexty.NewBlobThresholdPolicy(
				limits,
				fixtureBlobPreviewer(
					func(_ context.Context, content contexty.BlobContent) (contexty.BlobContent, error) {
						previews++
						content.Bytes[0] = 'Y'
						return contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}, nil
					},
				),
			)
			require.NoError(t, err)
			limits.MaxInlineBytes = 99
			request := fixtureOffloadRequest(t)
			request.Put.Content.Bytes = []byte(strings.Repeat("x", size))
			offloader := contexty.BlobOffloader{
				Policy:         policy,
				PolicyIdentity: contexty.Descriptor{ID: "threshold", Revision: "pinned"},
				Storage: fixtureBlobStore{
					put: func(_ context.Context, received contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
						puts++
						require.Equal(t, request.Put, received)
						return fixtureBlobReceipt(received), nil
					},
					get: nil,
				},
			}
			// Act.
			selection, err := offloader.Project(context.Background(), request)
			// Assert: no implicit reads and only offload writes original bytes.
			switch {
			case size <= 3:
				require.NoError(t, err)
				require.Equal(t, contexty.BlobInline, selection.Disposition)
				require.Equal(t, request.Put.Content, selection.Inline)
				require.Equal(t, 0, previews)
				require.Equal(t, 0, puts)
			case size <= 10:
				require.NoError(t, err)
				require.Equal(t, contexty.BlobOffload, selection.Disposition)
				require.NotNil(t, selection.Stored)
				require.Equal(t, "ok", string(selection.Preview.Bytes))
				require.Equal(t, 1, previews)
				require.Equal(t, 1, puts)
			default:
				require.ErrorIs(t, err, contexty.ErrBlobRejected)
				require.Equal(t, contexty.BlobSelection{}, selection)
				require.Equal(t, 0, previews)
				require.Equal(t, 0, puts)
			}
			if selection.Threshold != nil {
				require.EqualValues(t, 3, selection.Threshold.MaxInlineBytes)
			}
		})
	}
}

func TestBlob_OffloadFailureIsolation(t *testing.T) {
	for _, scenario := range []string{"preview-size", "preview-media", "preview-encoding", "unknown", "policy-error", "policy-cancel", "partial-put"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: policy mutation must not alter the immutable Put input.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := fixtureOffloadRequest(t)
			failure := errors.New("host failure")
			want := contexty.ErrInvalidBlobPolicy
			puts := 0
			policy := fixtureBlobPolicy(
				func(_ context.Context, candidate contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
					candidate.Content.Bytes[0] = 'X'
					candidate.Sources[0].ID = "changed"
					decision := contexty.BlobOffloadDecision{Disposition: contexty.BlobOffload,
						Preview: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}}
					switch scenario {
					case "preview-size":
						decision.Preview.Bytes = []byte("long")
						want = contexty.ErrBlobSizeLimit
					case "preview-media":
						decision.Preview.MIMEType = "application/json"
						want = contexty.ErrBlobMediaLimit
					case "preview-encoding":
						decision.Preview.Bytes = []byte{0xff}
						want = contexty.ErrBlobMediaLimit
					case "unknown":
						decision.Disposition = "unknown"
					case "policy-error":
						want = failure
						return decision, failure
					case "policy-cancel":
						cancel()
						want = context.Canceled
					case "partial-put":
						want = failure
					}
					return decision, nil
				},
			)
			offloader := contexty.BlobOffloader{
				Policy:         policy,
				PolicyIdentity: contexty.Descriptor{ID: "host-policy", Revision: "pinned"},
				Storage: fixtureBlobStore{
					put: func(_ context.Context, received contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
						puts++
						require.Equal(t, request.Put, received)
						return fixtureBlobReceipt(received), failure
					},
					get: nil,
				},
			}
			// Act.
			selection, err := offloader.Project(ctx, request)
			// Assert: failed writes only expose cleanup, never inline fallback/ref.
			require.ErrorIs(t, err, want)
			require.Nil(t, selection.Stored)
			require.Empty(t, selection.Inline.Bytes)
			require.Empty(t, selection.Preview.Bytes)
			require.Equal(t, "payload", string(request.Put.Content.Bytes))
			require.Equal(t, "source", request.Put.Sources[0].ID)
			if scenario == "partial-put" {
				require.Equal(t, 1, puts)
				require.NotNil(t, selection.Cleanup)
			} else {
				require.Equal(t, 0, puts)
				require.Equal(t, contexty.BlobSelection{}, selection)
			}
		})
	}
}

func TestBlob_OffloadConfiguration(t *testing.T) {
	for _, scenario := range []string{"nil-policy", "typed-nil-policy", "identity", "negative-preview", "source-media", "source-ref"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: malformed requests fail before host policy or storage.
			request := fixtureOffloadRequest(t)
			calls := 0
			offloader := contexty.BlobOffloader{
				Policy: fixtureBlobPolicy(
					func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
						calls++
						return contexty.BlobOffloadDecision{}, nil
					},
				),
				PolicyIdentity: contexty.Descriptor{ID: "policy", Revision: "pinned"},
				Storage:        nil,
			}
			want := contexty.ErrInvalidBlobPolicy
			switch scenario {
			case "nil-policy":
				offloader.Policy = nil
			case "typed-nil-policy":
				offloader.Policy = fixtureBlobPolicy(nil)
			case "identity":
				offloader.PolicyIdentity = contexty.Descriptor{}
			case "negative-preview":
				request.MaxPreviewBytes = -1
			case "source-media":
				request.Put.Content.MIMEType = "invalid"
			case "source-ref":
				request.Put.Sources[0] = contexty.ContentRef{}
				want = contexty.ErrInvalidBlob
			}
			// Act.
			selection, err := offloader.Project(context.Background(), request)
			// Assert.
			require.ErrorIs(t, err, want)
			require.Equal(t, contexty.BlobSelection{}, selection)
			require.Zero(t, calls)
		})
	}
}

func TestBlob_ThresholdConfiguration(t *testing.T) {
	// Arrange: an offload range always needs an explicit preview renderer.
	for _, limits := range []contexty.BlobThresholdLimits{
		{MaxInlineBytes: -1, MaxBlobBytes: 0},
		{MaxInlineBytes: 2, MaxBlobBytes: 1},
		{MaxInlineBytes: 1, MaxBlobBytes: 2},
	} {
		// Act.
		policy, err := contexty.NewBlobThresholdPolicy(limits, fixtureBlobPreviewer(nil))
		// Assert.
		require.ErrorIs(t, err, contexty.ErrInvalidBlobPolicy)
		require.Nil(t, policy)
	}
	policy, err := contexty.NewBlobThresholdPolicy(
		contexty.BlobThresholdLimits{MaxInlineBytes: 2, MaxBlobBytes: 2},
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, policy)
}

func TestBlob_OffloadPreviewIsolation(t *testing.T) {
	// Arrange: host retains and mutates the preview during Put.
	request := fixtureOffloadRequest(t)
	preview := contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}
	offloader := contexty.BlobOffloader{
		Policy: fixtureBlobPolicy(
			func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
				return contexty.BlobOffloadDecision{Disposition: contexty.BlobOffload, Preview: preview}, nil
			},
		),
		PolicyIdentity: contexty.Descriptor{ID: "policy", Revision: "pinned"},
		Storage: fixtureBlobStore{
			put: func(_ context.Context, received contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
				preview.Bytes[0] = 'X'
				return fixtureBlobReceipt(received), nil
			},
			get: nil,
		},
	}
	// Act.
	selection, err := offloader.Project(context.Background(), request)
	// Assert: preview and receipt are detached, source payload is unchanged.
	require.NoError(t, err)
	require.Equal(t, "ok", string(selection.Preview.Bytes))
	require.Equal(t, "payload", string(request.Put.Content.Bytes))
	require.Equal(t, offloader.PolicyIdentity, selection.Policy)
	require.Nil(t, selection.Cleanup)
	require.Nil(t, selection.Threshold)
}

func TestBlob_ThresholdPreviewFailures(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		// Arrange: neither renderer failure nor cancellation reaches storage.
		ctx, cancel := context.WithCancel(context.Background())
		failure := errors.New("preview renderer failure")
		policy, err := contexty.NewBlobThresholdPolicy(
			contexty.BlobThresholdLimits{MaxInlineBytes: 3, MaxBlobBytes: 10},
			fixtureBlobPreviewer(func(context.Context, contexty.BlobContent) (contexty.BlobContent, error) {
				if canceled {
					cancel()
				}
				return contexty.BlobContent{}, failure
			}),
		)
		require.NoError(t, err)
		offloader := contexty.BlobOffloader{Policy: policy,
			PolicyIdentity: contexty.Descriptor{ID: "threshold", Revision: "pinned"}, Storage: nil}
		// Act.
		selection, err := offloader.Project(ctx, fixtureOffloadRequest(t))
		cancel()
		// Assert.
		if canceled {
			require.ErrorIs(t, err, context.Canceled)
		} else {
			require.ErrorIs(t, err, failure)
		}
		require.Equal(t, contexty.BlobSelection{}, selection)
	}
}
