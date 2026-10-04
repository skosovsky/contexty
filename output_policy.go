package contexty

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrMissingOutputPolicy = errors.New("contexty: missing output policy callback")
	ErrInvalidOutputPolicy = errors.New("contexty: invalid output policy")
)

// OutputPolicyInput contains owned semantic content for one output.
type OutputPolicyInput struct {
	Kind    ManifestOutputKind
	Name    string
	Payload AbstractPayload
}

// OutputPolicy is a host-owned final prompt projection or validation boundary.
// It cannot select messages or alter tool topology. No policy implies no sanitization.
type OutputPolicy struct {
	Identity Descriptor
	Project  func(context.Context, OutputPolicyInput) (AbstractPayload, error)
}

func validateOutputPolicy(policy *OutputPolicy) error {
	if policy == nil {
		return nil
	}
	if err := policy.Identity.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOutputPolicy, err)
	}
	if policy.Project == nil {
		return ErrMissingOutputPolicy
	}
	return nil
}

func cloneOutputPolicyPayload(payload AbstractPayload) AbstractPayload {
	return AbstractPayload{System: cloneMessageSlice(payload.System), History: cloneMessageSlice(payload.History),
		Tools: cloneMessageSlice(payload.Tools), Memory: cloneMessageSlice(payload.Memory)}
}

func applyOutputPolicy(ctx context.Context, policy *OutputPolicy, input OutputPolicyInput) (AbstractPayload, error) {
	if err := ctx.Err(); err != nil {
		return AbstractPayload{}, err
	}
	if err := validateOutputPolicy(policy); err != nil {
		return AbstractPayload{}, err
	}
	if policy == nil {
		return cloneOutputPolicyPayload(input.Payload), nil
	}
	if err := validateOutputPolicyPayload(ctx, input.Payload); err != nil {
		return AbstractPayload{}, err
	}
	owned := input
	owned.Payload = cloneOutputPolicyPayload(input.Payload)
	output, err := policy.Project(ctx, owned)
	if canceled := ctx.Err(); canceled != nil {
		return AbstractPayload{}, canceled
	}
	if err != nil {
		return AbstractPayload{}, err
	}
	if err := validateOutputPolicyPayload(ctx, output); err != nil {
		return AbstractPayload{}, err
	}
	before := outputPolicySegments(input.Payload)
	for i, after := range outputPolicySegments(output) {
		if err := validateOutputPolicySegment(before[i], after); err != nil {
			return AbstractPayload{}, err
		}
	}
	return cloneOutputPolicyPayload(output), nil
}

func outputPolicySegments(payload AbstractPayload) [][]Message {
	return [][]Message{payload.System, payload.History, payload.Tools, payload.Memory}
}

func validateOutputPolicyPayload(ctx context.Context, payload AbstractPayload) error {
	codec := selectionCodec(ctx)
	for _, segment := range outputPolicySegments(payload) {
		for _, message := range segment {
			if !outputPolicyKnownRole(message.Role) {
				return ErrInvalidOutputPolicy
			}
			for _, part := range message.Parts {
				if nilInterfaceValue(part) {
					return ErrInvalidOutputPolicy
				}
			}
			if err := validateOutputPolicyMessageCodec(ctx, message, codec); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOutputPolicyMessageCodec(ctx context.Context, message Message, codec JSONSerializer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	wire, err := codec.Marshal(message)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOutputPolicy, err)
	}
	var restored Message
	err = codec.Unmarshal(wire, &restored)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOutputPolicy, err)
	}
	restoredWire, err := codec.Marshal(restored)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOutputPolicy, err)
	}
	originalDigest, err := canonicalJSONDigest(wire)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOutputPolicy, err)
	}
	restoredDigest, err := canonicalJSONDigest(restoredWire)
	if err != nil || originalDigest != restoredDigest {
		return ErrInvalidOutputPolicy
	}
	return nil
}

func outputPolicyKnownRole(role Role) bool {
	switch role {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

func validateOutputPolicySegment(before, after []Message) error {
	if len(before) != len(after) {
		return ErrInvalidOutputPolicy
	}
	for i, original := range before {
		accepted := after[i]
		if original.ID != accepted.ID {
			return ErrInvalidOutputPolicy
		}
		if err := validateOutputPolicyToolParts(original, accepted); err != nil {
			return err
		}
	}
	return nil
}

func validateOutputPolicyToolParts(before, after Message) error {
	for i, part := range before.Parts {
		if part.partKind() != PartKindToolCall && part.partKind() != PartKindToolResult {
			continue
		}
		if before.Role != after.Role || i >= len(after.Parts) {
			return ErrInvalidOutputPolicy
		}
		if !sameOutputPolicyToolIdentity(part, after.Parts[i]) {
			return ErrInvalidOutputPolicy
		}
	}
	for i, part := range after.Parts {
		if part.partKind() != PartKindToolCall && part.partKind() != PartKindToolResult {
			continue
		}
		if i >= len(before.Parts) || !sameOutputPolicyToolIdentity(before.Parts[i], part) {
			return ErrInvalidOutputPolicy
		}
	}
	return nil
}

func sameOutputPolicyToolIdentity(before, after ContentPart) bool {
	switch original := before.(type) {
	case ToolCallPart:
		accepted, ok := after.(ToolCallPart)
		return ok && original.ID == accepted.ID && original.Name == accepted.Name
	case ToolResultPart:
		accepted, ok := after.(ToolResultPart)
		return ok && original.ToolCallID == accepted.ToolCallID && original.Name == accepted.Name &&
			original.IsError == accepted.IsError
	default:
		return false
	}
}
