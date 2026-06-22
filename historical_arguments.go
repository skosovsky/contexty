package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrInvalidHistoricalArguments = errors.New("contexty: invalid historical argument projection")

const traceStageHistoricalArguments = "historical-arguments"

type HistoricalArgumentRequest struct {
	ID                       string
	History                  []Message
	MessageID                string
	CallID                   string
	ScopeRef                 string
	RetentionRef             string
	MaxPreviewBytes          int64
	AllowedPreviewMediaTypes []string
	Codec                    JSONSerializer
}

// HistoricalArgumentReference describes a prompt-only projection, never an
// execution/approval record. The original whole-message ref includes host metadata.
type HistoricalArgumentReference struct {
	Message ContentRef     `json:"message"`
	CallID  string         `json:"call_id"`
	Object  BlobDescriptor `json:"object"`
	Policy  Descriptor     `json:"policy"`
}

type HistoricalArgumentProjection struct {
	Source    []Message                    `json:"source"`
	Prompt    []Message                    `json:"prompt"`
	Selection BlobSelection                `json:"selection"`
	Reference *HistoricalArgumentReference `json:"reference"`
	Lineage   Lineage                      `json:"lineage"`
}

// ProjectHistoricalArguments explicitly prepares one completed historical call.
// Pending rounds are never projected, even if another call in that round has a
// result. All operation/approval metadata stays opaque and byte-identical.
func (o BlobOffloader) ProjectHistoricalArguments(ctx context.Context,
	request HistoricalArgumentRequest,
) (HistoricalArgumentProjection, error) {
	if err := ctx.Err(); err != nil {
		return HistoricalArgumentProjection{}, err
	}
	if request.ID == "" || request.MessageID == "" || request.CallID == "" {
		return HistoricalArgumentProjection{}, ErrInvalidHistoricalArguments
	}
	request.Codec = snapshotJSONSerializer(request.Codec)
	history := cloneMessageSlice(request.History)
	index, part, err := historicalArgumentTarget(history, request.MessageID, request.CallID)
	if err != nil {
		return HistoricalArgumentProjection{}, err
	}
	source, err := historicalArgumentMessageRef(ctx, history[index], request.Codec)
	if err != nil {
		return HistoricalArgumentProjection{}, fmt.Errorf("%w: %w", ErrInvalidHistoricalArguments, err)
	}
	call, ok := history[index].Parts[part].(ToolCallPart)
	if !ok {
		return HistoricalArgumentProjection{}, ErrInvalidHistoricalArguments
	}
	wire, err := json.Marshal(call.Arguments)
	if err != nil {
		return HistoricalArgumentProjection{}, err
	}
	selection, err := o.Project(ctx, BlobOffloadRequest{
		Put: BlobPutRequest{ScopeRef: request.ScopeRef, RetentionRef: request.RetentionRef,
			Content: BlobContent{MIMEType: mimeApplicationJSON, Bytes: wire}, Sources: []ContentRef{source}},
		MaxPreviewBytes: request.MaxPreviewBytes, AllowedPreviewMediaTypes: request.AllowedPreviewMediaTypes,
	})
	if err != nil {
		return failedHistoricalArguments(selection, request.ScopeRef, request.RetentionRef), err
	}
	projection, err := buildHistoricalArguments(ctx, request, history, index, part, source, selection)
	if err != nil {
		return failedHistoricalArguments(selection, request.ScopeRef, request.RetentionRef), err
	}
	return projection, nil
}

func historicalArgumentTarget(history []Message, messageID, callID string) (int, int, error) {
	if err := validateUniqueMessageIDs(history); err != nil {
		return 0, 0, err
	}
	observations, err := InspectToolRoundStates(history, nil)
	if err != nil {
		return 0, 0, err
	}
	for _, observation := range observations {
		if observation.AssistantID != messageID {
			continue
		}
		if observation.State != ToolRoundComplete {
			return 0, 0, ErrInvalidHistoricalArguments
		}
		for index, part := range history[observation.Start].Parts {
			call, ok := part.(ToolCallPart)
			if ok && call.ID == callID {
				if call.ArgumentsBlob != nil {
					return 0, 0, ErrInvalidHistoricalArguments
				}
				return observation.Start, index, nil
			}
		}
	}
	return 0, 0, ErrInvalidHistoricalArguments
}

func buildHistoricalArguments(ctx context.Context, request HistoricalArgumentRequest,
	history []Message, index, part int, source ContentRef, selection BlobSelection,
) (HistoricalArgumentProjection, error) {
	projection := HistoricalArgumentProjection{Source: cloneMessageSlice(history), Prompt: cloneMessageSlice(history),
		Selection: selection, Reference: nil, Lineage: Lineage{Records: nil, Unresolved: nil}}
	if selection.Disposition == BlobInline {
		return projection, nil
	}
	call, ok := projection.Prompt[index].Parts[part].(ToolCallPart)
	if !ok {
		return HistoricalArgumentProjection{}, ErrInvalidHistoricalArguments
	}
	call.Arguments = blobPreviewPayload(selection.Preview)
	blob := selection.Stored.Clone()
	call.ArgumentsBlob = &blob
	projection.Prompt[index].Parts[part] = call
	output, err := historicalArgumentMessageRef(ctx, projection.Prompt[index], request.Codec)
	if err != nil {
		return HistoricalArgumentProjection{}, fmt.Errorf("%w: %w", ErrInvalidHistoricalArguments, err)
	}
	output.Occurrence = request.ID
	projection.Reference = &HistoricalArgumentReference{Message: source, CallID: request.CallID,
		Object: selection.Stored.Clone(), Policy: selection.Policy}
	projection.Lineage.Records = []LineageRecord{
		{
			ID:        request.ID,
			Transform: selection.Policy,
			Inputs: []ContentRef{
				source,
			},
			Outputs:     []ContentRef{output},
			DecisionRef: "",
			Stage:       traceStageHistoricalArguments,
		},
	}
	if err := projection.Lineage.Validate(); err != nil {
		return HistoricalArgumentProjection{}, err
	}
	if err := ctx.Err(); err != nil {
		return HistoricalArgumentProjection{}, err
	}
	return projection, nil
}

func failedHistoricalArguments(selection BlobSelection, scope, retention string) HistoricalArgumentProjection {
	var failed HistoricalArgumentProjection
	failed.Selection.Cleanup = selection.Cleanup
	if selection.Stored != nil {
		failed.Selection.Cleanup = &BlobCleanupIntent{Object: selection.Stored.Object,
			ScopeRef: scope, RetentionRef: retention}
	}
	return failed
}

func historicalArgumentMessageRef(ctx context.Context, message Message, codec JSONSerializer) (ContentRef, error) {
	wire, err := historicalArgumentMessageWire(ctx, message, codec)
	if err != nil {
		return ContentRef{}, err
	}
	var restored Message
	err = codec.Unmarshal(wire, &restored)
	if canceled := ctx.Err(); canceled != nil {
		return ContentRef{}, canceled
	}
	if err != nil {
		return ContentRef{}, err
	}
	restoredWire, err := historicalArgumentMessageWire(ctx, restored, codec)
	if err != nil {
		return ContentRef{}, err
	}
	digest, err := canonicalJSONDigest(wire)
	if err != nil {
		return ContentRef{}, err
	}
	restoredDigest, err := canonicalJSONDigest(restoredWire)
	if err != nil || restored.ID != message.ID || restoredDigest != digest {
		return ContentRef{}, ErrInvalidHistoricalArguments
	}
	return ContentRef{ID: message.ID, Digest: digest, Occurrence: ""}, nil
}

func historicalArgumentMessageWire(ctx context.Context, message Message, codec JSONSerializer) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wire, err := codec.Marshal(message)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	return wire, err
}
