package contexty_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixturePreparedHistoricalArguments(
	t *testing.T,
) (contexty.HistoricalArgumentRequest, contexty.HistoricalArgumentProjection) {
	t.Helper()
	request := fixtureHistoricalArgumentRequest()
	return request, fixturePrepareHistoricalCall(t, request)
}

func fixturePrepareHistoricalCall(
	t *testing.T,
	request contexty.HistoricalArgumentRequest,
) contexty.HistoricalArgumentProjection {
	t.Helper()
	offloader := contexty.BlobOffloader{
		Policy: fixtureBlobPolicy(
			func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
				return contexty.BlobOffloadDecision{Disposition: contexty.BlobOffload,
					Preview: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}}, nil
			},
		),
		PolicyIdentity: contexty.Descriptor{ID: "argument-policy", Revision: "pinned"},
		Storage: fixtureBlobStore{
			put: func(_ context.Context, put contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
				receipt := fixtureBlobReceipt(put)
				receipt.Object.ID = "argument-" + request.CallID
				return receipt, nil
			},
			get: nil,
		},
	}
	prepared, err := offloader.ProjectHistoricalArguments(context.Background(), request)
	require.NoError(t, err)
	return prepared
}

func fixtureHistoricalArgumentRequest() contexty.HistoricalArgumentRequest {
	history := fixtureRoundMessages()
	call := history[0].Parts[0].(contexty.ToolCallPart)
	call.Arguments = contexty.ToolPayload{Text: strings.Repeat("original arguments ", 512),
		Data: json.RawMessage(`{"opaque_path":"working-file"}`), MIMEType: "application/json"}
	history[0].Parts[0] = call
	history[0].Extensions = []contexty.Extension{
		fixtureWireExtension{wire: `{"approval_digest":"host-original","operation":"host-record"}`},
	}
	history[1].Parts = append(
		history[1].Parts,
		contexty.ToolResultPart{ToolCallID: "second", Payload: contexty.TextPayload("done")},
	)
	codec := contexty.DefaultJSONSerializer()
	codec.Extensions = fixtureArtifactLabelRegistry()
	return contexty.HistoricalArgumentRequest{ID: "historical-projection", History: history,
		MessageID: "assistant", CallID: "first", ScopeRef: "write-scope", RetentionRef: "retention",
		MaxPreviewBytes: 2, AllowedPreviewMediaTypes: []string{fixtureBlobMIME}, Codec: codec}
}
