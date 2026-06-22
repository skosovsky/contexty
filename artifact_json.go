package contexty

import (
	"context"
	"encoding/json"
	"fmt"
)

// artifactJSON removes methods, retaining the first-class artifact fields.
type artifactJSON ContextArtifact

type artifactWire struct {
	artifactJSON

	Extensions json.RawMessage `json:"extensions,omitempty"`
}

// MarshalJSON retains extension type identities. Registry-bound checkpoint
// helpers additionally validate lossless host codecs before writing.
func (a ContextArtifact) MarshalJSON() ([]byte, error) {
	if err := validateArtifactBlob(a); err != nil {
		return nil, err
	}
	for _, extension := range a.Extensions {
		if nilInterfaceValue(extension) || extension.ExtensionType() == "" {
			return nil, ErrMissingLabelCodec
		}
	}
	extensions, err := encodeExtensions(a.Extensions)
	if err != nil {
		return nil, err
	}
	return json.Marshal(artifactWire{artifactJSON: artifactJSON(a), Extensions: extensions})
}

// UnmarshalJSON has no host registry. Labeled artifacts require the explicit
// UnmarshalArtifactJSON or conversation checkpoint codec, never untyped fallback.
func (a *ContextArtifact) UnmarshalJSON(data []byte) error {
	decoded, err := UnmarshalArtifactJSON(data, nil)
	if err != nil {
		return err
	}
	*a = decoded
	return nil
}

// UnmarshalArtifactJSON restores host-owned extensions with a pinned registry.
// Like other synchronous codecs this API does not invoke storage or label policy.
func UnmarshalArtifactJSON(data []byte, registry *ExtensionRegistry) (ContextArtifact, error) {
	var wire artifactWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return ContextArtifact{}, err
	}
	registry = registry.snapshot()
	labels, err := decodeExtensions(wire.Extensions, registry)
	if err != nil {
		return ContextArtifact{}, fmt.Errorf("%w: %w", ErrMissingLabelCodec, err)
	}
	if err := validateArtifactDecodedLabels(wire.Extensions, labels); err != nil {
		return ContextArtifact{}, err
	}
	artifact := ContextArtifact(wire.artifactJSON)
	artifact.Extensions = labels
	if err := validateArtifactLabels(artifact, registry); err != nil {
		return ContextArtifact{}, err
	}
	if err := validateArtifactBlob(artifact); err != nil {
		return ContextArtifact{}, err
	}
	return artifact.Clone(), nil
}

func validateArtifactDecodedLabels(wire json.RawMessage, labels []Extension) error {
	if len(labels) == 0 {
		return nil
	}
	restored, err := encodeExtensions(labels)
	if err != nil {
		return ErrMissingLabelCodec
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

func validateArtifactLabels(artifact ContextArtifact, registry *ExtensionRegistry) error {
	projection := LabelProjection{Policy: nil, Registry: registry, RequiredTypes: nil}
	return projection.validateLabels(context.Background(), artifact.Extensions, false)
}

func marshalArtifactJSON(artifact ContextArtifact, registry *ExtensionRegistry) ([]byte, error) {
	if err := validateArtifactLabels(artifact, registry); err != nil {
		return nil, err
	}
	return json.Marshal(artifact)
}

func marshalArtifactList(artifacts []ContextArtifact, registry *ExtensionRegistry) ([]json.RawMessage, error) {
	registry = registry.snapshot()
	var wires []json.RawMessage
	for _, artifact := range artifacts {
		wire, err := marshalArtifactJSON(artifact, registry)
		if err != nil {
			return nil, err
		}
		wires = append(wires, wire)
	}
	return wires, nil
}

func unmarshalArtifactList(wires []json.RawMessage, registry *ExtensionRegistry) ([]ContextArtifact, error) {
	registry = registry.snapshot()
	var artifacts []ContextArtifact
	for _, wire := range wires {
		artifact, err := UnmarshalArtifactJSON(wire, registry)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}
