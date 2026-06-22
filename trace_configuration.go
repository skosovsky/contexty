package contexty

import (
	"slices"
	"strings"
)

type CodecRegistryKind string

const (
	CodecExtension  CodecRegistryKind = "extension"
	CodecLabel      CodecRegistryKind = "label"
	CodecProvenance CodecRegistryKind = "provenance"
)

// CodecBinding names a configured decoder, never its function or registry handle.
type CodecBinding struct {
	Kind       CodecRegistryKind `json:"kind"`
	Type       string            `json:"type"`
	Descriptor Descriptor        `json:"descriptor"`
}

type TraceConfiguration struct {
	RequireOrigins         bool           `json:"require_origins"`
	RequireDurableIdentity bool           `json:"require_durable_identity"`
	RequiredLabelTypes     []string       `json:"required_label_types"`
	Codecs                 []CodecBinding `json:"codecs"`
}

func snapshotJSONSerializer(codec JSONSerializer) JSONSerializer {
	return JSONSerializer{Provenance: codec.Provenance.snapshot(), Extensions: codec.Extensions.snapshot()}
}

func canonicalLabelTypes(types []string) []string {
	copyTypes := slices.Clone(types)
	slices.Sort(copyTypes)
	return slices.Compact(copyTypes)
}

func cloneCodecBindings(bindings []CodecBinding) []CodecBinding {
	copyBindings := slices.Clone(bindings)
	slices.SortFunc(copyBindings, func(a, b CodecBinding) int {
		if order := strings.Compare(string(a.Kind), string(b.Kind)); order != 0 {
			return order
		}
		return strings.Compare(a.Type, b.Type)
	})
	return copyBindings
}

func (c TraceConfiguration) clone() TraceConfiguration {
	c.RequiredLabelTypes = canonicalLabelTypes(c.RequiredLabelTypes)
	c.Codecs = cloneCodecBindings(c.Codecs)
	return c
}

func (c TraceConfiguration) validate() error {
	if !slices.Equal(c.RequiredLabelTypes, canonicalLabelTypes(c.RequiredLabelTypes)) ||
		!slices.Equal(c.Codecs, cloneCodecBindings(c.Codecs)) {
		return ErrInvalidRecordingComponent
	}
	seen := make(map[CodecBinding]bool)
	for _, binding := range c.Codecs {
		key := CodecBinding{Kind: binding.Kind, Type: binding.Type, Descriptor: Descriptor{ID: "", Revision: ""}}
		if seen[key] || binding.Type == "" || binding.Type != strings.TrimSpace(binding.Type) {
			return ErrInvalidRecordingComponent
		}
		seen[key] = true
		switch binding.Kind {
		case CodecExtension, CodecLabel, CodecProvenance:
		default:
			return ErrInvalidRecordingComponent
		}
		if err := binding.Descriptor.Validate(); err != nil {
			return err
		}
	}
	for _, required := range c.RequiredLabelTypes {
		if required == "" || required != strings.TrimSpace(required) {
			return ErrMissingLabelCodec
		}
		key := CodecBinding{Kind: CodecLabel, Type: required, Descriptor: Descriptor{ID: "", Revision: ""}}
		if !seen[key] {
			return ErrMissingLabelCodec
		}
	}
	return nil
}

func (p TraceProfile) configuration(durable bool) (TraceConfiguration, error) {
	config := TraceConfiguration{RequireOrigins: p.RequireOrigins, RequireDurableIdentity: durable,
		RequiredLabelTypes: canonicalLabelTypes(p.Labels.RequiredTypes), Codecs: nil}
	declared := TraceConfiguration{RequireOrigins: p.RequireOrigins, RequireDurableIdentity: durable,
		RequiredLabelTypes: nil, Codecs: p.Codecs}
	if err := declared.validate(); err != nil {
		return config, err
	}
	expected, err := p.codecTopology()
	if err != nil {
		return config, err
	}
	for _, binding := range p.Codecs {
		key := CodecBinding{Kind: binding.Kind, Type: binding.Type, Descriptor: Descriptor{ID: "", Revision: ""}}
		if _, exists := expected[key]; !exists {
			return config, ErrInvalidRecordingComponent
		}
		if expected[key] != nil {
			return config, ErrInvalidRecordingComponent
		}
		descriptor := binding.Descriptor
		expected[key] = &descriptor
	}
	for key, descriptor := range expected {
		if descriptor == nil {
			return config, ErrInvalidRecordingComponent
		}
		key.Descriptor = *descriptor
		config.Codecs = append(config.Codecs, key)
	}
	config.Codecs = cloneCodecBindings(config.Codecs)
	return config, config.validate()
}

func (p TraceProfile) codecTopology() (map[CodecBinding]*Descriptor, error) {
	expected := make(map[CodecBinding]*Descriptor)
	provenance := p.Codec.Provenance.snapshot()
	for typeID, decoder := range provenance.decoders {
		if decoder == nil {
			return nil, ErrInvalidRecordingComponent
		}
		key := CodecBinding{Kind: CodecProvenance, Type: typeID, Descriptor: Descriptor{ID: "", Revision: ""}}
		expected[key] = nil
		if descriptor, intrinsic := provenance.intrinsic[typeID]; intrinsic {
			expected[key] = &descriptor
		}
	}
	for _, registry := range []struct {
		kind  CodecRegistryKind
		value *ExtensionRegistry
	}{
		{kind: CodecExtension, value: p.Codec.Extensions}, {kind: CodecLabel, value: p.Labels.Registry},
	} {
		copyRegistry := registry.value.snapshot()
		if copyRegistry == nil {
			continue
		}
		for typeID, decoder := range copyRegistry.decoders {
			if decoder == nil {
				return nil, ErrMissingLabelCodec
			}
			key := CodecBinding{Kind: registry.kind, Type: typeID, Descriptor: Descriptor{ID: "", Revision: ""}}
			expected[key] = nil
		}
	}
	return expected, nil
}
