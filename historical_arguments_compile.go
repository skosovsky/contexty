package contexty

import (
	"context"
	"encoding/json"
	"slices"
)

func (e *Engine) prepareCompileHistoricalOptions(
	ctx context.Context,
	req CompileRequest,
) (context.Context, compileOptions, error) {
	ctx, options, err := e.prepareCompileOptions(ctx, req.Options)
	if err != nil {
		return ctx, options, err
	}
	if err = validateCompileHistoricalArguments(ctx, req.History, options.historicalArguments); err != nil {
		return ctx, options, err
	}
	return ctx, options, nil
}

func cloneHistoricalArguments(projection HistoricalArgumentProjection) HistoricalArgumentProjection {
	projection.Source = cloneMessageSlice(projection.Source)
	projection.Prompt = cloneMessageSlice(projection.Prompt)
	projection.Lineage = projection.Lineage.Clone()
	projection.Selection.Inline = projection.Selection.Inline.clone()
	projection.Selection.Preview = projection.Selection.Preview.clone()
	if projection.Selection.Stored != nil {
		stored := projection.Selection.Stored.Clone()
		projection.Selection.Stored = &stored
	}
	if projection.Selection.Threshold != nil {
		threshold := *projection.Selection.Threshold
		projection.Selection.Threshold = &threshold
	}
	if projection.Selection.Cleanup != nil {
		cleanup := *projection.Selection.Cleanup
		projection.Selection.Cleanup = &cleanup
	}
	if projection.Reference != nil {
		reference := *projection.Reference
		reference.Object = reference.Object.Clone()
		projection.Reference = &reference
	}
	return projection
}

func validateCompileHistoricalArguments(
	ctx context.Context,
	history []Message,
	projections []HistoricalArgumentProjection,
) error {
	seen := make(map[[2]string]bool)
	for _, projection := range projections {
		if err := validateHistoricalArguments(ctx, history, projection); err != nil {
			return err
		}
		key := [2]string{projection.Reference.Message.ID, projection.Reference.CallID}
		if seen[key] {
			return ErrInvalidHistoricalArguments
		}
		seen[key] = true
	}
	return nil
}

func validateHistoricalArguments(
	ctx context.Context,
	history []Message,
	projection HistoricalArgumentProjection,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ref := projection.Reference
	if ref == nil || ref.Message.Validate() != nil || ref.Policy.Validate() != nil || ref.Object.Validate() != nil ||
		projection.Selection.Disposition != BlobOffload || projection.Selection.Stored == nil || projection.Selection.Cleanup != nil ||
		ref.Policy != projection.Selection.Policy || ref.Object.MIMEType != mimeApplicationJSON ||
		!slices.Contains(ref.Object.Sources, ref.Message) {
		return ErrInvalidHistoricalArguments
	}
	if err := projection.Lineage.Validate(); err != nil {
		return err
	}
	if len(projection.Lineage.Records) != 1 || len(projection.Lineage.Unresolved) != 0 {
		return ErrInvalidHistoricalArguments
	}
	index, part, err := historicalArgumentTarget(history, ref.Message.ID, ref.CallID)
	if err != nil {
		return err
	}
	messageRef, err := historicalArgumentContentRef(ctx, history[index], DefaultJSONSerializer())
	if err != nil {
		return err
	}
	if messageRef != ref.Message {
		return ErrInvalidHistoricalArguments
	}
	return validateHistoricalArgumentPayload(ctx, history[index], part, projection)
}

func validateHistoricalArgumentPayload(ctx context.Context, message Message, part int,
	projection HistoricalArgumentProjection,
) error {
	call, ok := message.Parts[part].(ToolCallPart)
	if !ok {
		return ErrInvalidHistoricalArguments
	}
	wire, err := json.Marshal(call.Arguments)
	if err != nil {
		return err
	}
	ref := projection.Reference
	if ref.Object.Digest != blobDigest(wire) || ref.Object.Length != int64(len(wire)) {
		return ErrBlobDigestMismatch
	}
	storedWire, err := EncodeBlobDescriptor(*projection.Selection.Stored)
	if err != nil {
		return err
	}
	refWire, err := EncodeBlobDescriptor(ref.Object)
	if err != nil || string(storedWire) != string(refWire) {
		return ErrInvalidHistoricalArguments
	}
	return validateHistoricalArgumentPrompt(ctx, projection)
}

func validateHistoricalArgumentPrompt(ctx context.Context, projection HistoricalArgumentProjection) error {
	ref := projection.Reference
	index, part, err := historicalArgumentTarget(projection.Source, ref.Message.ID, ref.CallID)
	if err != nil {
		return err
	}
	sourceRef, err := historicalArgumentContentRef(ctx, projection.Source[index], DefaultJSONSerializer())
	if err != nil {
		return err
	}
	if sourceRef != ref.Message {
		return ErrInvalidHistoricalArguments
	}
	if len(projection.Source) != len(projection.Prompt) {
		return ErrInvalidHistoricalArguments
	}
	expected := cloneMessageSlice(projection.Source)
	call, ok := expected[index].Parts[part].(ToolCallPart)
	if !ok {
		return ErrInvalidHistoricalArguments
	}
	call.Arguments = blobPreviewPayload(projection.Selection.Preview)
	object := ref.Object.Clone()
	call.ArgumentsBlob = &object
	expected[index].Parts[part] = call
	for i := range expected {
		if err := ctx.Err(); err != nil {
			return err
		}
		wanted, err := historicalArgumentContentRef(ctx, expected[i], DefaultJSONSerializer())
		if err != nil {
			return err
		}
		actual, err := historicalArgumentContentRef(ctx, projection.Prompt[i], DefaultJSONSerializer())
		if err != nil {
			return err
		}
		if wanted != actual {
			return ErrInvalidHistoricalArguments
		}
	}
	return validateHistoricalArgumentEdge(ctx, projection, index)
}

func validateHistoricalArgumentEdge(ctx context.Context, projection HistoricalArgumentProjection, index int) error {
	edge := projection.Lineage.Records[0]
	ref := projection.Reference
	output, err := historicalArgumentContentRef(ctx, projection.Prompt[index], DefaultJSONSerializer())
	if err != nil {
		return err
	}
	validInput := slices.Equal(edge.Inputs, []ContentRef{ref.Message})
	if edge.Stage != traceStageHistoricalArguments || edge.Transform != ref.Policy || edge.DecisionRef != "" ||
		!validInput || len(edge.Outputs) != 1 {
		return ErrInvalidHistoricalArguments
	}
	if edge.Outputs[0].Occurrence != edge.ID || baseContentRef(edge.Outputs[0]) != output {
		return ErrInvalidHistoricalArguments
	}
	return nil
}

func applyHistoricalArguments(ctx context.Context, snapshot ConversationSnapshot,
	projections []HistoricalArgumentProjection,
) (ConversationSnapshot, error) {
	history := snapshot.Segment(SegmentHistory)
	for _, projection := range projections {
		ref := projection.Reference
		index, part, err := historicalArgumentTarget(history, ref.Message.ID, ref.CallID)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		before := history[index].Clone()
		call, ok := history[index].Parts[part].(ToolCallPart)
		if !ok {
			return ConversationSnapshot{}, ErrInvalidHistoricalArguments
		}
		call.Arguments = blobPreviewPayload(projection.Selection.Preview)
		object := ref.Object.Clone()
		call.ArgumentsBlob = &object
		history[index].Parts[part] = call
		if err := traceHistoricalArguments(ctx, before, history[index], ref.Policy); err != nil {
			return ConversationSnapshot{}, err
		}
		if recorder := transformRecorderFrom(ctx); recorder != nil {
			recorder.set(before.ID, ActionFormatted, ReasonHistoricalArguments)
		}
	}
	return snapshot.WithSegment(SegmentHistory, history), nil
}

func traceHistoricalArguments(ctx context.Context, before, after Message, policy Descriptor) error {
	trace := traceFromContext(ctx)
	if trace == nil {
		return ctx.Err()
	}
	input, err := trace.inputRef(before)
	if err != nil {
		return err
	}
	if err = trace.profile.Labels.validateLabels(ctx, after.Extensions, true); err != nil {
		return err
	}
	output, err := historicalArgumentContentRef(ctx, after, trace.profile.Codec)
	if err != nil {
		return err
	}
	invocation := trace.nextID(traceStageHistoricalArguments)
	output.Occurrence = invocation
	if err := trace.appendRecord(LineageRecord{ID: invocation, Transform: policy, Inputs: []ContentRef{input},
		Outputs: []ContentRef{output}, DecisionRef: "", Stage: traceStageHistoricalArguments}); err != nil {
		return err
	}
	if err := captureTraceMessage(ctx, after, traceStageHistoricalArguments); err != nil {
		return err
	}
	trace.latest[baseContentRef(output)] = output
	return ctx.Err()
}

func historicalArgumentContentRef(ctx context.Context, message Message, codec JSONSerializer) (ContentRef, error) {
	if err := ctx.Err(); err != nil {
		return ContentRef{}, err
	}
	ref, err := MessageContentRef(message, codec)
	if canceled := ctx.Err(); canceled != nil {
		return ContentRef{}, canceled
	}
	return ref, err
}
