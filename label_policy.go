package contexty

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrMissingLabelPolicy  = errors.New("contexty: missing label policy")
	ErrLabelConflict       = errors.New("contexty: label projection conflict")
	ErrInvalidTrustUpgrade = errors.New("contexty: trust upgrade requires host decision")
	ErrMissingLabelCodec   = errors.New("contexty: required label codec unavailable")
)

// LabelDecision is the host's metadata projection. Upgrade is declared by the
// host; contexty does not infer trust classes from roles or define an ACL model.
type LabelDecision struct {
	Extensions  []Extension
	Upgrade     bool
	DecisionRef string
}

// LabelProjectionPolicy reconciles host-defined labels for a transform. A host
// conflict should wrap ErrLabelConflict. Arguments are defensive copies.
type LabelProjectionPolicy interface {
	ProjectLabels(ctx context.Context, inputs []Message, output Message, transform Descriptor) (LabelDecision, error)
}

// LabelProjection configures strict host-metadata transport and required codecs.
type LabelProjection struct {
	Policy        LabelProjectionPolicy
	Registry      *ExtensionRegistry
	RequiredTypes []string
}

// Project transfers host labels through a transform, validating codec presence
// and any declared trust upgrade. Content, roles and permissions aren't decided
// by this port. Source references are unioned independently of host labels.
func (p LabelProjection) Project(
	ctx context.Context, inputs []Message, output Message, transform Descriptor,
) (Message, string, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, "", err
	}
	if err := transform.Validate(); err != nil {
		return Message{}, "", err
	}
	p.RequiredTypes = slices.Clone(p.RequiredTypes)
	p.Registry = p.Registry.snapshot()
	if err := p.validateInputLabels(ctx, inputs); err != nil {
		return Message{}, "", err
	}
	result := output.Clone()
	for _, input := range inputs {
		for _, ref := range input.SourceRefs {
			if !slices.Contains(result.SourceRefs, ref) {
				result.SourceRefs = append(result.SourceRefs, ref)
			}
		}
	}
	if p.Policy == nil {
		if len(p.RequiredTypes) > 0 || messagesHaveExtensions(inputs) || len(output.Extensions) > 0 {
			return Message{}, "", ErrMissingLabelPolicy
		}
		return result, "", nil
	}
	decision, err := p.Policy.ProjectLabels(ctx, cloneMessageSlice(inputs), output.Clone(), transform)
	if canceled := ctx.Err(); canceled != nil {
		return Message{}, "", canceled
	}
	if err != nil {
		return Message{}, "", err
	}
	if decision.Upgrade && strings.TrimSpace(decision.DecisionRef) == "" {
		return Message{}, "", ErrInvalidTrustUpgrade
	}
	result.Extensions = cloneExtensions(decision.Extensions)
	if err := p.validateLabels(ctx, result.Extensions, true); err != nil {
		return Message{}, "", err
	}
	return result, decision.DecisionRef, nil
}

func (p LabelProjection) validateInputLabels(ctx context.Context, inputs []Message) error {
	for _, input := range inputs {
		if err := p.validateLabels(ctx, input.Extensions, false); err != nil {
			return err
		}
	}
	return nil
}

func (p LabelProjection) validateLabels(ctx context.Context, labels []Extension, requirePresence bool) error {
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		typeID, err := p.validateLabelCodec(ctx, label)
		if err != nil {
			return err
		}
		seen[typeID] = struct{}{}
	}
	if requirePresence {
		for _, required := range p.RequiredTypes {
			if _, present := seen[required]; !present {
				return ErrMissingLabelCodec
			}
		}
	}
	return ctx.Err()
}

func (p LabelProjection) validateLabelCodec(ctx context.Context, label Extension) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if nilInterfaceValue(label) || label.ExtensionType() == "" || p.Registry == nil {
		return "", ErrMissingLabelCodec
	}
	wire, err := EncodeExtension(label)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMissingLabelCodec, err)
	}
	if canceled := ctx.Err(); canceled != nil {
		return "", canceled
	}
	decoded, err := p.Registry.Decode(wire)
	if canceled := ctx.Err(); canceled != nil {
		return "", canceled
	}
	if err != nil || nilInterfaceValue(decoded) || decoded.ExtensionType() != label.ExtensionType() {
		return "", ErrMissingLabelCodec
	}
	if err := validateLabelRoundTrip(wire, decoded); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return label.ExtensionType(), nil
}

func validateLabelRoundTrip(wire []byte, decoded Extension) error {
	restored, err := EncodeExtension(decoded)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMissingLabelCodec, err)
	}
	originalDigest, err := canonicalJSONDigest(wire)
	if err != nil {
		return ErrMissingLabelCodec
	}
	restoredDigest, err := canonicalJSONDigest(restored)
	if err != nil || originalDigest != restoredDigest {
		return ErrMissingLabelCodec
	}
	return nil
}

func messagesHaveExtensions(msgs []Message) bool {
	for _, msg := range msgs {
		if len(msg.Extensions) > 0 {
			return true
		}
	}
	return false
}
