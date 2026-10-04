package contexty

import (
	"context"
	"slices"
)

const traceStageOutputPolicy = "output-policy"
const ReasonOutputPolicy = "output_policy"

type outputPolicyDecisionsKey struct{}

func WithArtifactMaterialization(policy ArtifactMaterializationPolicy) EngineOption {
	return func(e *Engine) { owned := policy; e.artifactMaterialization = &owned }
}

func WithOutputPolicy(policy OutputPolicy) EngineOption {
	return func(e *Engine) { owned := policy; e.outputPolicy = &owned }
}

func payloadSnapshot(payload AbstractPayload) ConversationSnapshot {
	return EmptySnapshot().WithSegment(SegmentSystem, payload.System).
		WithSegment(SegmentHistory, payload.History).WithSegment(SegmentTools, payload.Tools).
		WithSegment(SegmentMemory, payload.Memory)
}

func snapshotPayload(snap ConversationSnapshot) AbstractPayload {
	return AbstractPayload{
		System:  snap.Segment(SegmentSystem),
		History: snap.Segment(SegmentHistory),
		Tools:   snap.Segment(SegmentTools),
		Memory:  snap.Segment(SegmentMemory),
	}
}

func semanticOutputMessages(ctx context.Context, payload AbstractPayload) []Message {
	if finalBudgetChannel(ctx).kind == ManifestMainOutput {
		return payload.FlattenMessages()
	}
	return snapshotAllMessages(payloadSnapshot(payload))
}

func (e *Engine) acceptSemanticOutput(
	ctx context.Context,
	payload AbstractPayload,
	pipe *BudgetPipeline,
	selection *SelectionDecision,
	policy *SelectionPolicy,
) (AbstractPayload, error) {
	if err := validateOutputBeforePolicy(ctx, payload, pipe, selection, policy); err != nil {
		return AbstractPayload{}, err
	}
	accepted := cloneOutputPolicyPayload(payload)
	if e.outputPolicy != nil {
		channel := finalBudgetChannel(ctx)
		var err error
		accepted, err = applyOutputPolicy(
			ctx,
			e.outputPolicy,
			OutputPolicyInput{Kind: channel.kind, Name: channel.name, Payload: payload},
		)
		if err != nil {
			return AbstractPayload{}, err
		}
		if err := e.recordOutputPolicy(ctx, payload, accepted); err != nil {
			return AbstractPayload{}, err
		}
	}
	var opaqueErr error
	accepted, opaqueErr = e.acceptOpaqueState(ctx, accepted)
	if opaqueErr != nil {
		return AbstractPayload{}, opaqueErr
	}

	after := semanticOutputMessages(ctx, accepted)
	rebased, err := acceptedSelection(ctx, selection, after)
	if err != nil {
		return AbstractPayload{}, err
	}
	if err := validateMandatorySelection(ctx, rebased, policy, after); err != nil {
		return AbstractPayload{}, err
	}
	if err := validateFinalSelectionRounds(payloadSnapshot(accepted)); err != nil {
		return AbstractPayload{}, err
	}
	if pipe != nil {
		if err := validateAcceptedRetention(ctx, pipe, after); err != nil {
			return AbstractPayload{}, err
		}
		segments := []EstimateSegment{{Name: manifestMessagesSegment, Messages: after}}
		if finalBudgetChannel(ctx).kind == ManifestMainOutput {
			segments = payloadEstimateSegments(accepted)
		}
		if err := pipe.validateSegments(ctx, segments); err != nil {
			return AbstractPayload{}, err
		}
	}
	return accepted, nil
}

func validateOutputBeforePolicy(
	ctx context.Context,
	payload AbstractPayload,
	pipe *BudgetPipeline,
	selection *SelectionDecision,
	policy *SelectionPolicy,
) error {
	messages := semanticOutputMessages(ctx, payload)
	if err := validateUniqueMessageIDs(messages); err != nil {
		return err
	}
	if err := validateMandatorySelection(ctx, selection, policy, messages); err != nil {
		return err
	}
	if err := validateFinalSelectionRounds(payloadSnapshot(payload)); err != nil {
		return err
	}
	if pipe != nil {
		return pipe.validateRecordedRetention(ctx, messages)
	}
	return nil
}

func acceptedSelection(
	ctx context.Context,
	decision *SelectionDecision,
	messages []Message,
) (*SelectionDecision, error) {
	accepted := decision.clone()
	if accepted == nil {
		return nil, nil //nolint:nilnil // no selection is a valid view-only input
	}
	byID := make(map[string]Message)
	for _, message := range messages {
		byID[message.ID] = message
	}
	for i := range accepted.Candidates {
		candidate := &accepted.Candidates[i]
		for j, ref := range candidate.Members {
			message, found := byID[ref.ID]
			if !found {
				continue
			}
			actual, err := MessageContentRef(message, selectionCodec(ctx))
			if err != nil {
				return nil, err
			}
			candidate.Members[j] = actual
		}
		for j, ref := range candidate.RoundMembers {
			message, found := byID[ref.ID]
			if !found {
				continue
			}
			actual, err := roundSemanticRef(ctx, message)
			if err != nil {
				return nil, err
			}
			candidate.RoundMembers[j] = actual
		}
	}
	return accepted, nil
}

func validateAcceptedRetention(ctx context.Context, pipe *BudgetPipeline, after []Message) error {
	decisions, _ := ctx.Value(budgetDecisionsKey{}).(map[manifestChannelKey]BudgetDecision)
	original := decisions[finalBudgetChannel(ctx)].Required
	byID := make(map[string]Message)
	for _, message := range after {
		byID[message.ID] = message
	}
	required := slices.Clone(original)
	for i, ref := range original {
		message, found := byID[ref.ID]
		if !found {
			return ErrInvalidRetention
		}
		accepted, err := MessageContentRef(message, pipe.retentionCodec(ctx))
		if err != nil {
			return err
		}
		required[i] = accepted
	}
	return pipe.validateRequiredOutput(ctx, after, required)
}

func (e *Engine) recordOutputPolicy(ctx context.Context, before, after AbstractPayload) error {
	inputs, err := manifestSnapshotSegments(payloadSnapshot(before), selectionCodec(ctx))
	if err != nil {
		return err
	}
	outputs, err := manifestSnapshotSegments(payloadSnapshot(after), selectionCodec(ctx))
	if err != nil {
		return err
	}
	decisions, _ := ctx.Value(outputPolicyDecisionsKey{}).(map[manifestChannelKey]OutputPolicyDecision)
	decisions[finalBudgetChannel(ctx)] = OutputPolicyDecision{
		Policy:  e.outputPolicy.Identity,
		Inputs:  inputs,
		Outputs: outputs,
	}
	beforeMessages := semanticOutputMessages(ctx, before)
	afterMessages := semanticOutputMessages(ctx, after)
	for i, message := range afterMessages {
		if err := recordOutputPolicyMessage(ctx, e.outputPolicy.Identity, beforeMessages[i], message); err != nil {
			return err
		}
	}
	return nil
}

func recordOutputPolicyMessage(ctx context.Context, identity Descriptor, before, after Message) error {
	if recorder := transformRecorderFrom(ctx); recorder != nil && !MessageEqual(before, after) {
		recorder.set(after.ID, ActionFormatted, ReasonOutputPolicy)
	}
	trace := traceFromContext(ctx)
	if trace == nil {
		return nil
	}
	if err := trace.profile.Labels.validateLabels(ctx, after.Extensions, true); err != nil {
		return err
	}
	input, err := trace.inputRef(before)
	if err != nil {
		return err
	}
	output, err := MessageContentRef(after, trace.profile.Codec)
	if err != nil {
		return err
	}
	output.Occurrence = trace.nextID(traceStageOutputPolicy)
	if err := trace.appendRecord(
		LineageRecord{
			ID:          output.Occurrence,
			Transform:   identity,
			Inputs:      []ContentRef{input},
			Outputs:     []ContentRef{output},
			DecisionRef: "",
			Stage:       traceStageOutputPolicy,
		},
	); err != nil {
		return err
	}
	trace.latest[baseContentRef(output)] = output
	return captureTraceMessage(ctx, after, traceStageOutputPolicy)
}

func compileOutputPolicyDecision(ctx context.Context, kind ManifestOutputKind, name string) *OutputPolicyDecision {
	decisions, _ := ctx.Value(outputPolicyDecisionsKey{}).(map[manifestChannelKey]OutputPolicyDecision)
	decision, found := decisions[manifestChannelKey{kind: kind, name: name}]
	if !found {
		return nil
	}
	return decision.clone()
}
