package contexty

import (
	"context"
	"errors"
	"slices"
)

// OpaqueStateConfiguration pins host interpretation and the invalidation outcome.
// It contains identities only; replay never executes an adapter policy.
type OpaqueStateConfiguration struct {
	Policy      Descriptor             `json:"policy"`
	Profile     Descriptor             `json:"profile"`
	Invalidated OpaqueInvalidationMode `json:"invalidated"`
}

func (c *OpaqueStateConfiguration) clone() *OpaqueStateConfiguration {
	if c == nil {
		return nil
	}
	copyConfig := *c
	return &copyConfig
}

func (c *OpaqueStateConfiguration) Validate() error {
	if c == nil {
		return ErrInvalidOpaqueState
	}
	if c.Invalidated != OpaqueFailClosed && c.Invalidated != OpaqueDropInvalid {
		return ErrInvalidOpaqueState
	}
	return (OpaqueStatePolicy{Identity: c.Policy, Profile: c.Profile, Invalidated: c.Invalidated}).validate()
}

// OpaqueStateDrop identifies the exact message revision whose named state was
// removed. It never exposes opaque bytes or the referenced dependency content.
type OpaqueStateDrop struct {
	Message ContentRef `json:"message"`
	StateID string     `json:"state_id"`
}

// OpaqueStateDecision records the checked semantic scope and every explicit drop.
type OpaqueStateDecision struct {
	Configuration OpaqueStateConfiguration `json:"configuration"`
	Inputs        []ManifestSegment        `json:"inputs"`
	Outputs       []ManifestSegment        `json:"outputs"`
	Dropped       []OpaqueStateDrop        `json:"dropped"`
}

func (d *OpaqueStateDecision) clone() *OpaqueStateDecision {
	if d == nil {
		return nil
	}
	return &OpaqueStateDecision{
		Configuration: d.Configuration,
		Inputs:        clonePolicySegments(d.Inputs),
		Outputs:       clonePolicySegments(d.Outputs),
		Dropped:       slices.Clone(d.Dropped),
	}
}

func (d *OpaqueStateDecision) Validate() error {
	if d == nil {
		return ErrInvalidOpaqueState
	}
	if err := d.Configuration.Validate(); err != nil {
		return err
	}
	if err := validatePolicySegments(d.Inputs); err != nil {
		return err
	}
	if err := validatePolicySegments(d.Outputs); err != nil {
		return err
	}

	for i, input := range d.Inputs {
		output := d.Outputs[i]
		if len(input.Messages) != len(output.Messages) {
			return ErrInvalidOpaqueState
		}
		for j, ref := range input.Messages {
			if ref.ID != output.Messages[j].ID {
				return ErrInvalidOpaqueState
			}
		}
	}
	if len(d.Dropped) > 0 && d.Configuration.Invalidated != OpaqueDropInvalid {
		return ErrInvalidOpaqueState
	}
	seen := make(map[OpaqueStateDrop]bool)
	for _, drop := range d.Dropped {
		if drop.StateID == "" || drop.Message.Validate() != nil || drop.Message.Occurrence != "" || seen[drop] {
			return ErrInvalidOpaqueState
		}
		seen[drop] = true
	}
	return nil
}

func validateManifestOpaqueEvidence(manifest CompileManifest) error {
	for _, output := range manifest.Outputs {
		if err := validateOpaqueOutputEvidence(manifest.CompileConfiguration.OpaqueState, output); err != nil {
			return err
		}
	}
	return nil
}
func validateOpaqueOutputEvidence(config *OpaqueStateConfiguration, output ManifestOutput) error {
	decision := output.OpaqueState
	if config == nil {
		if decision != nil {
			return ErrInvalidOpaqueState
		}
		return nil
	}
	if decision == nil || decision.Configuration != *config {
		return ErrInvalidOpaqueState
	}
	if err := decision.Validate(); err != nil {
		return err
	}
	if err := validatePolicyFinalRefs(output, decision.Outputs); err != nil {
		return err
	}
	for i, segment := range decision.Inputs {
		for j, input := range segment.Messages {
			accepted := decision.Outputs[i].Messages[j]
			if input != accepted && !opaqueLineageBinding(output.Lineage, config.Policy, input, accepted) {
				return ErrInvalidLineage
			}
		}
	}
	return nil
}

func opaqueLineageBinding(graph Lineage, identity Descriptor, input, output ContentRef) bool {
	for _, record := range graph.Records {
		if record.Stage == "opaque-invalidation" && record.Transform == identity && len(record.Inputs) == 1 &&
			len(record.Outputs) == 1 &&
			baseContentRef(record.Inputs[0]) == input &&
			baseContentRef(record.Outputs[0]) == output {
			return true
		}
	}
	return false
}

type opaqueStateDecisionsKey struct{}

func initializeOpaqueEvidenceContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, opaqueStateDecisionsKey{}, make(map[manifestChannelKey]OpaqueStateDecision))
}

func recordOpaqueStateDecision(
	ctx context.Context,
	policy *OpaqueStatePolicy,
	before, after AbstractPayload,
	dropped []OpaqueStateDrop,
) error {
	if policy == nil {
		return nil
	}
	inputs, err := manifestSnapshotSegments(payloadSnapshot(before), selectionCodec(ctx))
	if err != nil {
		return err
	}
	outputs, err := manifestSnapshotSegments(payloadSnapshot(after), selectionCodec(ctx))
	if err != nil {
		return err
	}
	decision := OpaqueStateDecision{
		Configuration: OpaqueStateConfiguration{
			Policy:      policy.Identity,
			Profile:     policy.Profile,
			Invalidated: policy.Invalidated,
		},
		Inputs:  inputs,
		Outputs: outputs,
		Dropped: slices.Clone(dropped),
	}
	if err := decision.Validate(); err != nil {
		return err
	}
	decisions, _ := ctx.Value(opaqueStateDecisionsKey{}).(map[manifestChannelKey]OpaqueStateDecision)
	if decisions == nil {
		return ErrInvalidOpaqueState
	}
	decisions[finalBudgetChannel(ctx)] = decision
	return nil
}

func compileOpaqueStateDecision(ctx context.Context, kind ManifestOutputKind, name string) *OpaqueStateDecision {
	decisions, _ := ctx.Value(opaqueStateDecisionsKey{}).(map[manifestChannelKey]OpaqueStateDecision)
	decision, found := decisions[manifestChannelKey{kind: kind, name: name}]
	if !found {
		return nil
	}
	return decision.clone()
}

func validateReplayOpaqueStates(
	ctx context.Context,
	manifest CompileManifest,
	index map[ContentRef]SavedContent,
	codec JSONSerializer,
) error {
	for _, output := range manifest.Outputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateReplayOpaqueOutput(output, index, codec); err != nil {
			return err
		}
	}
	return nil
}
func validateReplayOpaqueOutput(output ManifestOutput, index map[ContentRef]SavedContent, codec JSONSerializer) error {
	segments := output.Segments
	order := snapshotSegmentOrder()
	if output.Kind == ManifestMainOutput {
		order = []SegmentName{SegmentSystem, SegmentHistory, SegmentTools, SegmentMemory}
	}
	if output.OpaqueState != nil {
		segments = output.OpaqueState.Outputs
	}
	messages, err := replayOpaqueScope(segments, order, index, codec)
	if err != nil {
		return err
	}
	if output.OpaqueState == nil {
		for _, message := range messages {
			for _, extension := range message.Extensions {
				if _, ok := opaqueStateFromExtension(extension); ok {
					return ErrMissingOpaqueStatePolicy
				}
			}
		}
		return nil
	}
	if err = ValidateOpaqueState(messages, codec, output.OpaqueState.Configuration.Profile); err != nil {
		return err
	}
	return validateReplayOpaqueTransition(output.OpaqueState, index, codec, messages)
}

func replayOpaqueScope(
	segments []ManifestSegment,
	order []SegmentName,
	index map[ContentRef]SavedContent,
	codec JSONSerializer,
) ([]Message, error) {
	var messages []Message
	for _, name := range order {
		for _, segment := range segments {
			if segment.Name != string(name) && segment.Name != "messages" {
				continue
			}
			for _, ref := range segment.Messages {
				message, err := replayMessage(ref, index, codec)
				if err != nil {
					return nil, err
				}
				messages = append(messages, message)
			}
			if segment.Name == "messages" {
				return messages, nil
			}
		}
	}
	return messages, nil
}

func opaqueAcceptedRef(decision *OpaqueStateDecision, ref ContentRef) ContentRef {
	if decision == nil {
		return baseContentRef(ref)
	}
	for i, segment := range decision.Inputs {
		for j, input := range segment.Messages {
			if input == baseContentRef(ref) {
				return decision.Outputs[i].Messages[j]
			}
		}
	}
	return baseContentRef(ref)
}

func opaqueAcceptedSegments(decision *OpaqueStateDecision, segments []ManifestSegment) []ManifestSegment {
	result := clonePolicySegments(segments)
	for i, segment := range result {
		for j, ref := range segment.Messages {
			result[i].Messages[j] = opaqueAcceptedRef(decision, ref)
		}
	}
	return result
}

func validateReplayOpaqueTransition(
	decision *OpaqueStateDecision,
	index map[ContentRef]SavedContent,
	codec JSONSerializer,
	final []Message,
) error {
	drops := make(map[string]OpaqueStateDrop)
	for _, drop := range decision.Dropped {
		if _, duplicate := drops[drop.StateID]; duplicate {
			return ErrInvalidOpaqueState
		}
		drops[drop.StateID] = drop
		if err := validateOpaqueDropProof(drop, index, codec, final, decision.Configuration.Profile); err != nil {
			return err
		}
	}
	for i, segment := range decision.Inputs {
		for j, ref := range segment.Messages {
			if err := validateOpaqueMessageTransition(
				ref,
				decision.Outputs[i].Messages[j],
				drops,
				index,
				codec,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOpaqueDropProof(
	drop OpaqueStateDrop,
	index map[ContentRef]SavedContent,
	codec JSONSerializer,
	final []Message,
	profile Descriptor,
) error {
	original, err := replayMessage(drop.Message, index, codec)
	if err != nil {
		return err
	}
	for _, extension := range original.Extensions {
		state, ok := opaqueStateFromExtension(extension)
		if !ok || state.ID != drop.StateID {
			continue
		}
		if err = state.validate(); err != nil {
			return err
		}
		if err = validateOpaquePayloadCodec(state, codec.Extensions); err != nil {
			return err
		}
		if state.Binding.Profile != profile {
			return ErrOpaqueStateInvalidated
		}
		if err = validateDroppedStateAbsent(final, state.ID); err != nil {
			return err
		}
		if !errors.Is(validateOneOpaqueState(final, original.ID, state, codec, profile), ErrOpaqueStateInvalidated) {
			return ErrInvalidOpaqueState
		}
		return nil
	}
	return ErrInvalidOpaqueState
}
func validateDroppedStateAbsent(messages []Message, id string) error {
	for _, message := range messages {
		for _, extension := range message.Extensions {
			if state, ok := opaqueStateFromExtension(extension); ok && state.ID == id {
				return ErrInvalidOpaqueState
			}
		}
	}
	return nil
}

func validateOpaqueMessageTransition(
	beforeRef, afterRef ContentRef,
	drops map[string]OpaqueStateDrop,
	index map[ContentRef]SavedContent,
	codec JSONSerializer,
) error {
	before, err := replayMessage(beforeRef, index, codec)
	if err != nil {
		return err
	}
	after, err := replayMessage(afterRef, index, codec)
	if err != nil {
		return err
	}
	expected := before.Clone()
	kept := make([]Extension, 0, len(expected.Extensions))
	for _, extension := range expected.Extensions {
		state, ok := opaqueStateFromExtension(extension)
		drop, remove := drops[state.ID]
		if !ok || !remove {
			kept = append(kept, extension)
			continue
		}
		if drop.Message != beforeRef {
			return ErrInvalidOpaqueState
		}
	}
	expected.Extensions = kept
	if !MessageEqual(expected, after) {
		return ErrInvalidOpaqueState
	}
	return nil
}
