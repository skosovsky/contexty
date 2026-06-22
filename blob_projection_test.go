package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestBlob_Projection(t *testing.T) {
	// Arrange: a host controls decoding, estimator and explicit reservations.
	resolver, request := fixtureBlobResolver(t)
	// Act.
	projection, err := resolver.Resolve(context.Background(), request)
	// Assert: complete bytes/metadata/cost and decoder identity are bound together.
	require.NoError(t, err)
	require.Equal(t, "payload", projection.Messages[0].TextContent())
	require.Equal(t, 7, projection.Estimate.Total)
	require.Equal(t, 7, projection.Estimate.EffectiveLimit)
	require.Equal(t, contexty.EstimateEstimated, projection.Estimate.Quality)
	require.Equal(t, request.Blob, projection.Blob)
	require.NoError(t, projection.Lineage.Validate())
	ref, err := contexty.BlobDescriptorRef(request.Blob)
	require.NoError(t, err)
	require.Equal(t, []contexty.ContentRef{ref}, projection.Lineage.Records[0].Inputs)
	require.Equal(t, resolver.DecoderIdentity, projection.Lineage.Records[0].Transform)
	require.Equal(t, "resolution", projection.Lineage.Records[0].Outputs[0].Occurrence)
	require.Equal(
		t,
		projection.Estimate.Segments[0].Messages[0].Digest,
		projection.Lineage.Records[0].Outputs[0].Digest,
	)
	projection.Blob.Sources[0].ID = "mutated"
	require.Equal(t, "source", request.Blob.Sources[0].ID)
}

func TestBlob_ProjectionFailures(t *testing.T) {
	for _, scenario := range []string{"overflow", "invalid-budget", "decoder-error", "decoder-cancel", "get-error", "duplicate-id", "unknown-cost", "missing-decoder"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resolver, request := fixtureBlobResolver(t)
			decodes, gets := 0, 0
			failure := errors.New("host decode failed")
			want := contexty.ErrInvalidBlob
			switch scenario {
			case "overflow":
				request.Budget = contexty.EffectiveInputBudget(6)
				want = contexty.ErrBudgetExceeded
			case "invalid-budget":
				request.Budget = contexty.EffectiveInputBudget(-1)
				want = contexty.ErrInvalidBudgetRequest
			case "decoder-error":
				want = failure
			case "decoder-cancel":
				want = context.Canceled
			case "get-error":
				want = contexty.ErrBlobDenied
			case "duplicate-id":
				want = contexty.ErrDuplicateMessageID
			case "unknown-cost":
				want = contexty.ErrUnknownEstimateCost
			}
			resolver.Storage = fixtureBlobStore{
				get: func(context.Context, contexty.BlobGetRequest) (contexty.BlobContent, error) {
					gets++
					if scenario == "get-error" {
						return contexty.BlobContent{}, contexty.ErrBlobDenied
					}
					return contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("payload")}, nil
				},
			}
			resolver.Decoder = fixtureBlobDecoder(
				func(context.Context, contexty.BlobContent) ([]contexty.Message, error) {
					decodes++
					return fixtureBlobDecodeScenario(scenario, failure, cancel)
				},
			)
			if scenario == "missing-decoder" {
				resolver.Decoder = fixtureBlobDecoder(nil)
			}
			// Act.
			projection, err := resolver.Resolve(ctx, request)
			// Assert: neither partial messages nor cost evidence escape failures.
			require.ErrorIs(t, err, want)
			require.Zero(t, projection)
			if scenario == "invalid-budget" || scenario == "missing-decoder" {
				require.Zero(t, gets)
				require.Zero(t, decodes)
			}
			if scenario == "get-error" {
				require.Zero(t, decodes)
			}
		})
	}
}

func TestBlob_ProjectionIsolation(t *testing.T) {
	// Arrange: host callbacks retain/mutate buffers, outputs and caller configuration.
	resolver, request := fixtureBlobResolver(t)
	originalBlob, originalDecoder := request.Blob.Clone(), resolver.DecoderIdentity
	backend := []byte("payload")
	message := contexty.TextMessage(contexty.RoleUser, "payload")
	message.ID = "decoded"
	retained := []contexty.Message{message}
	resolver.Storage = fixtureBlobStore{
		get: func(context.Context, contexty.BlobGetRequest) (contexty.BlobContent, error) {
			request.Blob.Sources[0].ID = "callback-mutated"
			resolver.DecoderIdentity = contexty.Descriptor{ID: "changed", Revision: "changed"}
			resolver.Reporter = nil
			return contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: backend}, nil
		},
	}
	resolver.Decoder = fixtureBlobDecoder(
		func(_ context.Context, content contexty.BlobContent) ([]contexty.Message, error) {
			content.Bytes[0] = 'X'
			return retained, nil
		},
	)
	// Act.
	projection, err := resolver.Resolve(context.Background(), request)
	// Assert: configuration/source/cost bind to the operation's initial snapshot.
	require.NoError(t, err)
	require.Equal(t, originalBlob, projection.Blob)
	require.Equal(t, originalDecoder, projection.Decoder)
	require.Equal(t, "payload", string(backend))
	retained[0].Parts[0] = contexty.TextPart{Text: "later mutation"}
	require.Equal(t, "payload", projection.Messages[0].TextContent())
	require.Equal(t, 7, projection.Estimate.Total)
}

func TestBlob_ProjectionInvalidConfiguration(t *testing.T) {
	for _, scenario := range []string{"resolution-id", "decoder-identity", "reporter-nil", "reporter-zero", "storage-nil"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: invalid configuration fails before any retrieval or decoding.
			resolver, request := fixtureBlobResolver(t)
			gets := 0
			resolver.Storage = fixtureBlobStore{
				get: func(context.Context, contexty.BlobGetRequest) (contexty.BlobContent, error) {
					gets++
					return contexty.BlobContent{}, errors.New("unexpected Get")
				},
			}
			want := contexty.ErrInvalidBlob
			switch scenario {
			case "resolution-id":
				request.ID = ""
			case "decoder-identity":
				resolver.DecoderIdentity = contexty.Descriptor{}
			case "reporter-nil":
				resolver.Reporter = nil
			case "reporter-zero":
				resolver.Reporter = &contexty.EstimateReporter{}
				want = contexty.ErrInvalidEstimateReport
			case "storage-nil":
				resolver.Storage = (*fixtureBlobStore)(nil)
			}
			// Act.
			projection, err := resolver.Resolve(context.Background(), request)
			// Assert.
			require.ErrorIs(t, err, want)
			require.Zero(t, projection)
			require.Zero(t, gets)
		})
	}
}

func TestBlob_DescriptorIdentity(t *testing.T) {
	// Arrange: equal raw bytes are not equal storage/replay metadata identities.
	descriptor := fixtureBlobReceipt(fixtureBlobRequest(t))
	baseline, err := contexty.BlobDescriptorRef(descriptor)
	require.NoError(t, err)
	for _, field := range []string{"revision", "mime", "retention", "source"} {
		t.Run(field, func(t *testing.T) {
			// Arrange.
			changed := descriptor.Clone()
			switch field {
			case "revision":
				changed.Object.Revision = "changed"
			case "mime":
				changed.MIMEType = "application/json"
			case "retention":
				changed.RetentionRef = "changed"
			case "source":
				changed.Sources[0].ID = "changed"
			}
			// Act.
			ref, refErr := contexty.BlobDescriptorRef(changed)
			// Assert: full descriptor digest changes despite equal raw digest.
			require.NoError(t, refErr)
			require.Equal(t, descriptor.Digest, changed.Digest)
			require.NotEqual(t, baseline.Digest, ref.Digest)
		})
	}
}

func TestBlob_ProjectionCounterFailures(t *testing.T) {
	for _, scenario := range []string{"counter-error", "counter-cancel", "decoder-cancel"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: cancellation/errors must stop later cost callbacks, not just discard output.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resolver, request := fixtureBlobResolver(t)
			failure := errors.New("host counter failed")
			perCalls, totalCalls := 0, 0
			counter := fixtureEvidenceEstimator{
				per: func(context.Context, []contexty.Message) ([]int, error) {
					perCalls++
					if scenario == "counter-error" {
						return nil, failure
					}
					cancel()
					return []int{7}, nil
				},
				total: func(context.Context, []contexty.Message) (int, error) {
					totalCalls++
					return 7, nil
				},
			}
			reporter, err := contexty.NewEstimateReporter(
				counter,
				fixtureEstimateProfile(),
				contexty.DefaultJSONSerializer(),
			)
			require.NoError(t, err)
			resolver.Reporter = reporter
			if scenario == "decoder-cancel" {
				resolver.Decoder = fixtureBlobDecoder(
					func(context.Context, contexty.BlobContent) ([]contexty.Message, error) {
						cancel()
						message := contexty.TextMessage(contexty.RoleUser, "payload")
						message.ID = "decoded"
						return []contexty.Message{message}, nil
					},
				)
			}
			// Act.
			projection, err := resolver.Resolve(ctx, request)
			// Assert.
			if scenario == "counter-error" {
				require.ErrorIs(t, err, contexty.ErrTokenCountFailed)
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
			require.Zero(t, projection)
			require.Zero(t, totalCalls)
			if scenario == "decoder-cancel" {
				require.Zero(t, perCalls)
			} else {
				require.Equal(t, 1, perCalls)
			}
		})
	}
}
