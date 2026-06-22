package contexty_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestHistoricalArgument_Projection(t *testing.T) {
	// Arrange: one completed multi-call round and an unrelated pending round.
	request := fixtureHistoricalArgumentRequest()
	request.History = append(request.History, contexty.Message{
		ID:   "pending",
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{ID: "pending-call", Name: "opaque", Arguments: contexty.TextPayload("untouched")},
		},
	})
	baseline := make([]contexty.Message, len(request.History))
	for i, message := range request.History {
		baseline[i] = message.Clone()
	}
	var stored contexty.BlobPutRequest
	offloader := contexty.BlobOffloader{
		Policy: fixtureBlobPolicy(
			func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
				return contexty.BlobOffloadDecision{Disposition: contexty.BlobOffload,
					Preview: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}}, nil
			},
		),
		PolicyIdentity: contexty.Descriptor{ID: "policy", Revision: "pinned"},
		Storage: fixtureBlobStore{
			put: func(_ context.Context, put contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
				stored = put
				return fixtureBlobReceipt(put), nil
			},
			get: nil,
		},
	}
	// Act: prepare only prompt projection, never read a workspace path or storage.
	projection, err := offloader.ProjectHistoricalArguments(context.Background(), request)
	// Assert: original execution/approval data is untouched; ref points to original.
	require.NoError(t, err)
	require.Equal(t, baseline, projection.Source)
	require.Equal(t, baseline, request.History)
	require.Equal(t, baseline[0].Extensions, projection.Prompt[0].Extensions)
	require.Equal(t, baseline[0].Parts[1], projection.Prompt[0].Parts[1])
	require.Equal(t, baseline[1:], projection.Prompt[1:])
	call := projection.Prompt[0].Parts[0].(contexty.ToolCallPart)
	require.Equal(t, "first", call.ID)
	require.Equal(t, "opaque-a", call.Name)
	require.Equal(t, "ok", call.Arguments.Text)
	require.Equal(t, projection.Reference.Object, *call.ArgumentsBlob)
	var original contexty.ToolPayload
	require.NoError(t, json.Unmarshal(stored.Content.Bytes, &original))
	require.Equal(t, baseline[0].Parts[0].(contexty.ToolCallPart).Arguments, original)
	require.Equal(t, []contexty.ContentRef{projection.Reference.Message}, stored.Sources)
	require.NoError(t, projection.Lineage.Validate())
	wire, err := request.Codec.Marshal(projection.Prompt[0])
	require.NoError(t, err)
	var decoded contexty.Message
	require.NoError(t, request.Codec.Unmarshal(wire, &decoded))
	require.Equal(t, projection.Prompt[0], decoded)
	projection.Source[0].Extensions = nil
	projection.Source[0].Parts = nil
	require.NotEmpty(t, projection.Prompt[0].Extensions)
	require.NotEmpty(t, request.History[0].Parts)
}

func TestHistoricalArgument_TargetFailures(t *testing.T) {
	for _, scenario := range []string{"pending", "missing-message", "missing-call", "empty-id", "malformed-round", "missing-codec"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: reject before policy/Put (neither port is installed).
			request := fixtureHistoricalArgumentRequest()
			want := contexty.ErrInvalidHistoricalArguments
			switch scenario {
			case "pending":
				request.History[1].Parts = request.History[1].Parts[:1]
			case "missing-message":
				request.MessageID = "absent"
			case "missing-call":
				request.CallID = "absent"
			case "empty-id":
				request.ID = ""
			case "malformed-round":
				request.History[1].Parts = append(request.History[1].Parts, request.History[1].Parts[0])
				want = contexty.ErrInvalidToolRound
			case "missing-codec":
				request.Codec = contexty.DefaultJSONSerializer()
			}
			// Act.
			projection, err := (contexty.BlobOffloader{}).ProjectHistoricalArguments(context.Background(), request)
			// Assert: no partial source/prompt or durable ref escapes.
			require.ErrorIs(t, err, want)
			require.Zero(t, projection)
		})
	}
}

func TestHistoricalArgument_StorageFailures(t *testing.T) {
	for _, scenario := range []string{"inline", "reject", "partial-put", "cancel-put", "invalid-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: no failure may publish a prompt/reference or silently inline.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := fixtureHistoricalArgumentRequest()
			failure := errors.New("host Put failure")
			puts := 0
			want := failure
			offloader := contexty.BlobOffloader{
				Policy: fixtureBlobPolicy(
					func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
						disposition := contexty.BlobOffload
						switch scenario {
						case "inline":
							disposition = contexty.BlobInline
						case "reject":
							disposition = contexty.BlobReject
							want = contexty.ErrBlobRejected
						}
						return contexty.BlobOffloadDecision{Disposition: disposition,
							Preview: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}}, nil
					},
				),
				PolicyIdentity: contexty.Descriptor{ID: "policy", Revision: "pinned"},
				Storage: fixtureBlobStore{
					put: func(_ context.Context, put contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
						puts++
						receipt := fixtureBlobReceipt(put)
						switch scenario {
						case "cancel-put":
							cancel()
							want = context.Canceled
							return receipt, nil
						case "invalid-receipt":
							receipt.Length++
							want = contexty.ErrInvalidBlob
							return receipt, nil
						}
						return receipt, failure
					},
					get: nil,
				},
			}
			// Act.
			projection, err := offloader.ProjectHistoricalArguments(ctx, request)
			// Assert.
			if scenario == "inline" {
				require.NoError(t, err)
				require.Equal(t, request.History, projection.Source)
				require.Equal(t, projection.Source, projection.Prompt)
				require.Nil(t, projection.Reference)
				require.Zero(t, puts)
				return
			}
			require.ErrorIs(t, err, want)
			require.Nil(t, projection.Source)
			require.Nil(t, projection.Prompt)
			require.Nil(t, projection.Reference)
			require.Nil(t, projection.Selection.Stored)
			if scenario == "reject" {
				require.Zero(t, puts)
				require.Nil(t, projection.Selection.Cleanup)
			} else {
				require.Equal(t, 1, puts)
				require.NotNil(t, projection.Selection.Cleanup)
			}
		})
	}
}

func TestHistoricalArgument_CodecCancellation(t *testing.T) {
	// Arrange: cancellation during metadata decoding stops re-encoding and Put.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := fixtureHistoricalArgumentRequest()
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
		cancel()
		return fixtureWireExtension{wire: string(data)}, nil
	})
	request.Codec.Extensions = registry
	// Act.
	projection, err := (contexty.BlobOffloader{}).ProjectHistoricalArguments(ctx, request)
	// Assert.
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, projection)
}
