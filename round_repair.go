package contexty

import (
	"context"
	"errors"
	"fmt"
	"maps"
)

var ErrInvalidRoundRepair = errors.New("contexty: invalid interrupted round repair")

// InterruptedRoundRepairPolicy pins projection behavior, not external execution.
// Decisions are scoped by assistant message identity.
type InterruptedRoundRepairPolicy struct {
	Descriptor Descriptor
	Encoding   Descriptor
	Decisions  map[string]string
}

// SyntheticRoundRepair accompanies a synthetic message and its lineage edge.
type SyntheticRoundRepair struct {
	Assistant      ContentRef `json:"assistant"`
	Output         ContentRef `json:"output"`
	MissingCallIDs []string   `json:"missing_call_ids"`
	Policy         Descriptor `json:"policy"`
	Encoding       Descriptor `json:"encoding"`
	DecisionRef    string     `json:"decision_ref"`
}

// InterruptedRoundProjection is independent of the source history.
type InterruptedRoundProjection struct {
	Messages []Message
	Repairs  []SyntheticRoundRepair
	Lineage  Lineage
}

type syntheticRoundMarker struct {
	Kind        string     `json:"kind"`
	Assistant   ContentRef `json:"assistant"`
	CallID      string     `json:"call_id"`
	Policy      Descriptor `json:"policy"`
	Encoding    Descriptor `json:"encoding"`
	DecisionRef string     `json:"decision_ref"`
}

// RepairInterruptedToolRounds creates deterministic projection-only results.
// It never resolves pending calls or asserts an external action's outcome.
func RepairInterruptedToolRounds(ctx context.Context, messages []Message,
	declarations map[string]ToolRoundState, policy InterruptedRoundRepairPolicy,
	codec JSONSerializer,
) (InterruptedRoundProjection, error) {
	if err := ctx.Err(); err != nil {
		return InterruptedRoundProjection{}, err
	}
	if err := policy.Descriptor.Validate(); err != nil {
		return InterruptedRoundProjection{}, fmt.Errorf("%w: %w", ErrInvalidRoundRepair, err)
	}
	if err := policy.Encoding.Validate(); err != nil {
		return InterruptedRoundProjection{}, fmt.Errorf("%w: %w", ErrInvalidRoundRepair, err)
	}
	policy.Decisions = maps.Clone(policy.Decisions)
	declarations = maps.Clone(declarations)
	frozen := cloneMessageSlice(messages)
	observations, err := InspectToolRoundStates(frozen, declarations)
	if err != nil {
		return InterruptedRoundProjection{}, err
	}
	if err := validateRepairDecisions(observations, policy.Decisions); err != nil {
		return InterruptedRoundProjection{}, err
	}
	out := InterruptedRoundProjection{Messages: nil, Repairs: nil, Lineage: Lineage{Records: nil, Unresolved: nil}}
	usedIDs := make(map[string]bool, len(frozen))
	for _, message := range frozen {
		usedIDs[message.ID] = true
	}
	cursor := 0
	for _, observation := range observations {
		if err := ctx.Err(); err != nil {
			return InterruptedRoundProjection{}, err
		}
		out.Messages = append(out.Messages, frozen[cursor:observation.End+1]...)
		cursor = observation.End + 1
		if observation.State != ToolRoundInterrupted {
			continue
		}
		message, evidence, edge, err := repairInterruptedRound(
			frozen[observation.Start:observation.End+1], observation, policy, codec,
		)
		if err != nil {
			return InterruptedRoundProjection{}, err
		}
		if usedIDs[message.ID] {
			return InterruptedRoundProjection{}, ErrInvalidRoundRepair
		}
		usedIDs[message.ID] = true
		out.Messages = append(out.Messages, message)
		out.Repairs = append(out.Repairs, evidence)
		out.Lineage.Records = append(out.Lineage.Records, edge)
	}
	out.Messages = append(out.Messages, frozen[cursor:]...)
	if err := out.Lineage.Validate(); err != nil {
		return InterruptedRoundProjection{}, err
	}
	if err := ctx.Err(); err != nil {
		return InterruptedRoundProjection{}, err
	}
	return out, nil
}

func validateRepairDecisions(observations []ToolRoundObservation, decisions map[string]string) error {
	interrupted := make(map[string]bool)
	for _, observation := range observations {
		if observation.State == ToolRoundInterrupted {
			interrupted[observation.AssistantID] = true
			if observation.AssistantID == "" || decisions[observation.AssistantID] == "" {
				return ErrInvalidRoundRepair
			}
		}
	}
	for id := range decisions {
		if !interrupted[id] {
			return ErrInvalidRoundRepair
		}
	}
	return nil
}

func repairInterruptedRound(messages []Message, observation ToolRoundObservation,
	policy InterruptedRoundRepairPolicy, codec JSONSerializer,
) (Message, SyntheticRoundRepair, LineageRecord, error) {
	inputs := make([]ContentRef, len(messages))
	for i, message := range messages {
		ref, err := roundRepairContentRef(message, codec)
		if err != nil {
			return Message{}, SyntheticRoundRepair{}, LineageRecord{}, err
		}
		inputs[i] = ref
	}
	evidence := SyntheticRoundRepair{
		Assistant: inputs[0], MissingCallIDs: append([]string(nil), observation.MissingCallIDs...),
		Policy: policy.Descriptor, Encoding: policy.Encoding,
		DecisionRef: policy.Decisions[observation.AssistantID], Output: ContentRef{ID: "", Digest: "", Occurrence: ""},
	}
	digest, err := estimateDigest(struct {
		Inputs   []ContentRef
		Evidence SyntheticRoundRepair
	}{Inputs: inputs, Evidence: evidence})
	if err != nil {
		return Message{}, SyntheticRoundRepair{}, LineageRecord{}, err
	}
	message := messages[0].Clone()
	message.ID = "synthetic-round/" + digest
	message.Role = RoleTool
	message.Parts = nil
	// A repaired projection is not eligible for the original message's cache hint.
	message.LLMCache = nil
	for _, callID := range observation.MissingCallIDs {
		payload, payloadErr := StructuredPayload(syntheticRoundMarker{
			Kind: "interrupted_projection", Assistant: inputs[0], CallID: callID,
			Policy: policy.Descriptor, DecisionRef: evidence.DecisionRef,
			Encoding: policy.Encoding,
		})
		if payloadErr != nil {
			return Message{}, SyntheticRoundRepair{}, LineageRecord{}, payloadErr
		}
		payload.Text = "Synthetic projection marker: no result was recorded; external outcome is unknown."
		message.Parts = append(
			message.Parts,
			ToolResultPart{ToolCallID: callID, Payload: payload, Name: "", IsError: false},
		)
	}
	output, err := roundRepairContentRef(message, codec)
	if err != nil {
		return Message{}, SyntheticRoundRepair{}, LineageRecord{}, err
	}
	evidence.Output = output
	edge := LineageRecord{
		ID: "round-repair/" + digest, Transform: policy.Descriptor,
		Inputs: inputs, Outputs: []ContentRef{output}, DecisionRef: evidence.DecisionRef, Stage: "round_repair",
	}
	return message, evidence, edge, nil
}

func roundRepairContentRef(message Message, codec JSONSerializer) (ContentRef, error) {
	ref, err := MessageContentRef(message, codec)
	if err != nil {
		return ContentRef{}, err
	}
	wire, err := codec.Marshal(message)
	if err != nil {
		return ContentRef{}, err
	}
	var restored Message
	if err = codec.Unmarshal(wire, &restored); err != nil {
		return ContentRef{}, err
	}
	restoredRef, err := MessageContentRef(restored, codec)
	if err != nil {
		return ContentRef{}, err
	}
	if ref != restoredRef {
		return ContentRef{}, ErrInvalidRoundRepair
	}
	return ref, nil
}
