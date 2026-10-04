package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

var ErrInvalidExportSelection = errors.New("contexty: invalid export selection")

// ExportMetadata is an explicit disclosure policy for selected content.
// An allowed extension type approves its entire host-owned payload.
type ExportMetadata struct {
	Actor                bool
	Annotations          bool
	SourceRefs           bool
	Origin               bool
	Cache                bool
	Provenance           bool
	ArtifactType         bool
	ExtensionTypes       []string
	TransformDescriptors bool
	Lineage              bool
}

// ExportSelection approves exact output message IDs and canonical artifact payload revisions.
// ArtifactPayloadRefs explicitly approve the original artifact payload, even when
// its materialized prompt message was transformed by an output policy.
// Selecting an ID does not approve historical revisions with the same ID.
// LineageRefs approve only metadata references, not their source payloads.
type ExportSelection struct {
	MessageIDs          []string
	ArtifactPayloadRefs []ContentRef
	LineageRefs         []ContentRef
	Metadata            ExportMetadata
	AllowOpaqueDigests  bool
	// OpaqueStateIDs separately approve host state carried by selected messages.
	OpaqueStateIDs []string
	// OpaqueProfile must match every approved state binding.
	OpaqueProfile Descriptor
}

// ExportLineageRecord exposes only approved references. OmittedInputs explicitly
// reports unavailable/disallowed links; OpaqueInputDigests never carry source IDs.
type ExportLineageRecord struct {
	Inputs             []ContentRef `json:"inputs,omitempty"`
	Outputs            []ContentRef `json:"outputs"`
	OpaqueInputDigests []string     `json:"opaque_input_digests,omitempty"`
	OmittedInputs      int          `json:"omitted_inputs,omitempty"`
	Transform          *Descriptor  `json:"transform,omitempty"`
}

// ExportEnvelope is the complete isolated-consumer transport. It contains no
// CompileRequest, snapshots, execution callbacks or implicit resolver handles.
// RawMessage preserves the typed wire codecs without embedding local registries.
type ExportEnvelope struct {
	Messages  []json.RawMessage     `json:"messages"`
	Artifacts []json.RawMessage     `json:"artifacts,omitempty"`
	Lineage   []ExportLineageRecord `json:"lineage,omitempty"`
}

// exportedArtifact intentionally excludes local lifecycle and persistence policy.
type exportedArtifact struct {
	ID           string          `json:"id"`
	Kind         ArtifactKind    `json:"kind"`
	Payload      ToolPayload     `json:"payload"`
	ArtifactType string          `json:"artifact_type,omitempty"`
	SourceRefs   []SourceRef     `json:"source_refs,omitempty"`
	Extensions   json.RawMessage `json:"extensions,omitempty"`
}

// ExportProjection selects content only from a named output, never from Source
// or InputSnapshot. Artifact payload refs must exactly match canonical participating
// artifacts; they are separate from permission to export accepted prompt messages.
// All selected IDs must exist and be unique. Metadata defaults
// to no disclosure; lineage digests of excluded content require separate consent.
// Text is intentionally not copied: it may render a wider context than Messages.
func ExportProjection(projection CompileProjection,
	selection ExportSelection, codec JSONSerializer,
) (ExportEnvelope, error) {
	if err := projection.Lineage.Validate(); err != nil {
		return ExportEnvelope{}, err
	}
	selected, err := exportMessages(projection.Messages, selection, codec)
	if err != nil {
		return ExportEnvelope{}, err
	}
	for _, ref := range selection.ArtifactPayloadRefs {
		if !slices.Contains(projection.ArtifactIDs, ref.ID) {
			return ExportEnvelope{}, ErrInvalidExportSelection
		}
	}
	artifactWire, err := exportArtifacts(projection.Artifacts, selection, codec.Extensions)
	if err != nil {
		return ExportEnvelope{}, err
	}
	out := ExportEnvelope{Messages: selected.wires, Artifacts: artifactWire, Lineage: nil}
	if selection.Metadata.Lineage {
		out.Lineage, err = exportLineage(projection.Lineage, selected, selection)
		if err != nil {
			return ExportEnvelope{}, err
		}
	}
	return out, nil
}

type exportedMessages struct {
	wires      []json.RawMessage
	originals  []ContentRef
	publicRefs []ContentRef
}

func exportMessages(messages []Message, selection ExportSelection, codec JSONSerializer) (exportedMessages, error) {
	if err := validateExportIDs(selection.MessageIDs); err != nil {
		return exportedMessages{}, err
	}
	if err := validateUniqueMessageIDs(messages); err != nil {
		return exportedMessages{}, err
	}
	result := exportedMessages{wires: nil, originals: nil, publicRefs: nil}
	var publicMessages []Message
	for _, id := range selection.MessageIDs {
		msg := findMessageByID(messages, id)
		if msg.ID == "" {
			return exportedMessages{}, fmt.Errorf("%w: output message %q missing", ErrInvalidExportSelection, id)
		}
		original, err := MessageContentRef(msg, codec)
		if err != nil {
			return exportedMessages{}, err
		}
		public, err := exportMessageMetadata(msg, selection.Metadata, codec.Extensions)
		if err != nil {
			return exportedMessages{}, err
		}
		publicMessages = append(publicMessages, public)
		wire, err := codec.Marshal(public)
		if err != nil {
			return exportedMessages{}, err
		}
		ref, err := MessageContentRef(public, codec)
		if err != nil {
			return exportedMessages{}, err
		}
		result.wires = append(result.wires, wire)
		result.originals = append(result.originals, original)
		result.publicRefs = append(result.publicRefs, ref)
	}
	return exportOpaqueStates(messages, publicMessages, result, selection, codec)
}

func exportMessageMetadata(msg Message, policy ExportMetadata, registry *ExtensionRegistry) (Message, error) {
	msg.Parts = exportMessageParts(msg.Parts)
	msg = msg.Clone()
	if !policy.Actor {
		msg.Actor = nil
	} else if msg.Actor != nil && !policy.SourceRefs {
		msg.Actor.SourceRefs = nil
	}
	if !policy.Annotations {
		msg.Annotations = Annotations{} //nolint:exhaustruct_v5 // omit all metadata by default
	}
	if !policy.SourceRefs {
		msg.SourceRefs = nil
	}
	if !policy.Origin {
		msg.Origin = nil
	}
	if !policy.Cache {
		msg.LLMCache = nil
	}
	if !policy.Provenance {
		msg.Provenance = nil
	}
	var selected []Extension
	for _, ext := range msg.Extensions {
		if !nilInterfaceValue(ext) && ext.ExtensionType() != OpaqueStateExtensionType &&
			slices.Contains(policy.ExtensionTypes, ext.ExtensionType()) {
			selected = append(selected, ext)
		}
	}
	validator := LabelProjection{Policy: nil, Registry: registry, RequiredTypes: nil}
	// ExportProjection is a synchronous codec operation without a cancellation port.
	if err := validator.validateLabels(context.Background(), selected, false); err != nil {
		return Message{}, err
	}
	msg.Extensions = selected
	return msg, nil
}

func exportMessageParts(parts []ContentPart) []ContentPart {
	var exported []ContentPart
	for _, part := range parts {
		if call, ok := part.(ToolCallPart); ok {
			call.ArgumentsBlob = nil
			part = call
		}
		exported = append(exported, part.clonePart())
	}
	return exported
}

func exportArtifacts(
	artifacts []ContextArtifact,
	selection ExportSelection,
	registry *ExtensionRegistry,
) ([]json.RawMessage, error) {
	if err := validateExportArtifactRefs(selection.ArtifactPayloadRefs); err != nil {
		return nil, err
	}
	if err := validateUniqueArtifactIDs(artifacts); err != nil {
		return nil, err
	}
	var result []json.RawMessage
	for _, ref := range selection.ArtifactPayloadRefs {
		found := false
		for _, artifact := range artifacts {
			if artifact.ID != ref.ID {
				continue
			}
			canonical, err := ArtifactContentRef(artifact)
			if err != nil {
				return nil, err
			}
			if ref != canonical {
				return nil, fmt.Errorf("%w: artifact %q revision mismatch", ErrInvalidExportSelection, ref.ID)
			}
			found = true
			wire, err := exportArtifactWire(artifact, selection.Metadata, registry)
			if err != nil {
				return nil, err
			}
			result = append(result, wire)
		}
		if !found {
			return nil, fmt.Errorf("%w: artifact %q missing", ErrInvalidExportSelection, ref.ID)
		}
	}
	return result, nil
}

func validateExportArtifactRefs(refs []ContentRef) error {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil || ref.Occurrence != "" {
			return ErrInvalidExportSelection
		}
		ids = append(ids, ref.ID)
	}
	return validateExportIDs(ids)
}

func exportArtifactWire(artifact ContextArtifact, policy ExportMetadata, registry *ExtensionRegistry) ([]byte, error) {
	copyArtifact := exportedArtifact{ID: artifact.ID, Kind: artifact.Kind,
		Payload: artifact.Payload.Clone(), ArtifactType: "", SourceRefs: nil, Extensions: nil}
	labels, err := exportArtifactLabels(artifact, policy, registry)
	if err != nil {
		return nil, err
	}
	copyArtifact.Extensions = labels
	if policy.SourceRefs {
		copyArtifact.SourceRefs = cloneSourceRefs(artifact.SourceRefs)
	}
	if policy.ArtifactType {
		copyArtifact.ArtifactType = artifact.ArtifactType
	}
	return json.Marshal(copyArtifact)
}

func exportArtifactLabels(
	artifact ContextArtifact,
	policy ExportMetadata,
	registry *ExtensionRegistry,
) (json.RawMessage, error) {
	message := Message{
		Extensions: cloneExtensions(artifact.Extensions),
	}
	selected, err := exportMessageMetadata(message, policy, registry)
	if err != nil {
		return nil, err
	}
	return encodeExtensions(selected.Extensions)
}

func validateExportIDs(ids []string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return ErrInvalidExportSelection
		}
		if _, duplicate := seen[id]; duplicate {
			return ErrInvalidExportSelection
		}
		seen[id] = struct{}{}
	}
	return nil
}

func exportLineage(graph Lineage, messages exportedMessages, policy ExportSelection) ([]ExportLineageRecord, error) {
	allowed := make(map[ContentRef]struct{})
	for _, ref := range policy.LineageRefs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		allowed[baseContentRef(ref)] = struct{}{}
	}
	for i, ref := range messages.originals {
		if ref == messages.publicRefs[i] {
			allowed[ref] = struct{}{}
		}
	}
	// Follow only ancestors of selected outputs, even if more metadata is approved.
	needed := exportAncestors(graph, messages.originals)
	occurrences := make(map[string]string)
	var result []ExportLineageRecord
	for _, record := range graph.Records {
		outputs := approvedExportOutputs(record, needed, allowed, occurrences)
		if len(outputs) == 0 {
			continue
		}
		entry := ExportLineageRecord{ //nolint:exhaustruct_v5 // undisclosed metadata omitted
			Outputs: outputs,
		}
		if policy.Metadata.TransformDescriptors {
			descriptor := record.Transform
			entry.Transform = &descriptor
		}
		for _, input := range record.Inputs {
			appendExportInput(&entry, input, allowed, policy.AllowOpaqueDigests, occurrences)
		}
		result = append(result, entry)
	}
	return append(result, exportSanitizedRecords(messages, allowed, policy, occurrences)...), nil
}

func approvedExportOutputs(record LineageRecord, needed, allowed map[ContentRef]struct{},
	occurrences map[string]string,
) []ContentRef {
	var outputs []ContentRef
	for _, output := range record.Outputs {
		_, wanted := needed[output]
		_, approved := allowed[baseContentRef(output)]
		if wanted && approved {
			outputs = append(outputs, exportedContentRef(output, occurrences))
		}
	}
	return outputs
}

func appendExportInput(entry *ExportLineageRecord, input ContentRef, allowed map[ContentRef]struct{},
	opaque bool, occurrences map[string]string,
) {
	if _, approved := allowed[baseContentRef(input)]; approved {
		entry.Inputs = append(entry.Inputs, exportedContentRef(input, occurrences))
	} else if opaque {
		entry.OpaqueInputDigests = append(entry.OpaqueInputDigests, input.Digest)
	} else {
		entry.OmittedInputs++
	}
}

func exportSanitizedRecords(messages exportedMessages, allowed map[ContentRef]struct{},
	policy ExportSelection, occurrences map[string]string,
) []ExportLineageRecord {
	var result []ExportLineageRecord
	// Metadata filtering itself creates a new, public content revision. Do not
	// silently assert the original transform produced these sanitized bytes.
	for i, original := range messages.originals {
		if original == messages.publicRefs[i] {
			continue
		}
		entry := ExportLineageRecord{ //nolint:exhaustruct_v5 // export filtering has no host transform descriptor
			Outputs: []ContentRef{messages.publicRefs[i]},
		}
		appendExportInput(&entry, original, allowed, policy.AllowOpaqueDigests, occurrences)
		result = append(result, entry)
	}
	return result
}

func exportedContentRef(ref ContentRef, occurrences map[string]string) ContentRef {
	if ref.Occurrence != "" {
		name, ok := occurrences[ref.Occurrence]
		if !ok {
			name = "export/" + strconv.Itoa(len(occurrences)+1)
			occurrences[ref.Occurrence] = name
		}
		ref.Occurrence = name
	}
	return ref
}

func exportAncestors(graph Lineage, selected []ContentRef) map[ContentRef]struct{} {
	needed := make(map[ContentRef]struct{})
	producers := make(map[ContentRef][]ContentRef)
	for _, record := range graph.Records {
		for _, output := range record.Outputs {
			producers[output] = append(producers[output], record.Inputs...)
		}
	}
	var queue []ContentRef
	for _, ref := range selected {
		// The last output occurrence is the latest revision in this branch.
		for _, record := range slices.Backward(graph.Records) {
			found := false
			for _, output := range record.Outputs {
				if baseContentRef(output) == ref {
					queue = append(queue, output)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	// Imported graphs need not be topologically ordered. Traverse by identity,
	// not slice order, and tolerate pass-through edges without revisiting them.
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if _, visited := needed[ref]; visited {
			continue
		}
		needed[ref] = struct{}{}
		queue = append(queue, producers[ref]...)
	}
	return needed
}

// exportOpaqueStates validates dependencies against the actual disclosed semantic
// revisions. Metadata approvals never grant permission to copy host opaque state.
func exportOpaqueStates(originals, public []Message, result exportedMessages,
	selection ExportSelection, codec JSONSerializer,
) (exportedMessages, error) {
	if len(selection.OpaqueStateIDs) == 0 {
		return result, nil
	}
	if err := validateExportIDs(selection.OpaqueStateIDs); err != nil {
		return exportedMessages{}, err
	}
	if err := selection.OpaqueProfile.Validate(); err != nil {
		return exportedMessages{}, fmt.Errorf("%w: opaque profile required", ErrInvalidExportSelection)
	}
	if err := approveExportOpaqueStates(originals, public, selection); err != nil {
		return exportedMessages{}, err
	}
	if err := ValidateOpaqueState(public, codec, selection.OpaqueProfile); err != nil {
		return exportedMessages{}, fmt.Errorf("%w: %w", ErrInvalidExportSelection, err)
	}
	return encodeExportOpaqueStates(public, result, selection.OpaqueProfile, codec)
}

func approveExportOpaqueStates(originals, public []Message, selection ExportSelection) error {
	found := make(map[string]bool, len(selection.OpaqueStateIDs))
	for i := range public {
		public[i].Extensions = nil
		for _, extension := range findMessageByID(originals, public[i].ID).Extensions {
			state, ok := opaqueStateFromExtension(extension)
			if !ok {
				if !nilInterfaceValue(extension) && extension.ExtensionType() != OpaqueStateExtensionType &&
					slices.Contains(selection.Metadata.ExtensionTypes, extension.ExtensionType()) {
					public[i].Extensions = append(public[i].Extensions, extension.CloneExtension())
				}
				continue
			}
			if !slices.Contains(selection.OpaqueStateIDs, state.ID) {
				continue
			}
			if found[state.ID] {
				return fmt.Errorf("%w: duplicate opaque state %q", ErrInvalidExportSelection, state.ID)
			}
			found[state.ID] = true
			public[i].Extensions = append(public[i].Extensions, state.CloneExtension())
		}
	}
	if len(found) != len(selection.OpaqueStateIDs) {
		return fmt.Errorf("%w: opaque state missing from selected messages", ErrInvalidExportSelection)
	}
	return nil
}

func encodeExportOpaqueStates(public []Message, result exportedMessages, profile Descriptor,
	codec JSONSerializer,
) (exportedMessages, error) {
	// Codec round-trip is part of the isolated transport guarantee, including
	// the host payload decoder and its pinned identity.
	for i, message := range public {
		wire, err := codec.Marshal(message)
		if err != nil {
			return exportedMessages{}, err
		}
		var decoded Message
		if err = codec.Unmarshal(wire, &decoded); err != nil {
			return exportedMessages{}, err
		}
		ref, err := MessageContentRef(decoded, codec)
		if err != nil {
			return exportedMessages{}, err
		}
		expected, err := MessageContentRef(message, codec)
		if err != nil {
			return exportedMessages{}, err
		}
		if ref != expected {
			return exportedMessages{}, fmt.Errorf(
				"%w: opaque codec changed exported revision",
				ErrInvalidExportSelection,
			)
		}
		public[i] = decoded
		result.wires[i] = wire
		result.publicRefs[i] = ref
	}
	if err := ValidateOpaqueState(public, codec, profile); err != nil {
		return exportedMessages{}, fmt.Errorf("%w: %w", ErrInvalidExportSelection, err)
	}
	return result, nil
}
