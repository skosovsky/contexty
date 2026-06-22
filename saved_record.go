package contexty

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

var (
	ErrReplayMismatch          = errors.New("contexty: replay expectation mismatch")
	ErrReplayContentMismatch   = errors.New("contexty: saved content digest mismatch")
	ErrMissingReplayDependency = errors.New("contexty: missing saved replay dependency")
	ErrUnsupportedReplay       = errors.New("contexty: unsupported replay")
	ErrReplayCodec             = errors.New("contexty: replay codec mismatch or missing codec")
	ErrInvalidRecordState      = errors.New("contexty: invalid saved record state")
)

type RecordState string

const (
	RecordProposed   RecordState = "proposed"
	RecordAccepted   RecordState = "accepted"
	RecordSuperseded RecordState = "superseded"
)

// SavedCompileRecord is local host-owned content, not a safe export envelope.
// Proposed records may omit content; only a complete record can be accepted.
type SavedCompileRecord struct {
	Manifest    CompileManifest `json:"manifest"`
	State       RecordState     `json:"state"`
	DecisionRef string          `json:"decision_ref,omitempty"`
	Content     []SavedContent  `json:"content"`
	Omitted     []ContentRef    `json:"omitted"`
}

func (r SavedCompileRecord) Validate() error {
	if err := r.Manifest.Validate(); err != nil {
		return err
	}
	if r.Manifest.Privacy == nil {
		return ErrMissingRecordPolicy
	}
	if err := r.Manifest.Privacy.Validate(); err != nil {
		return err
	}
	switch r.State {
	case RecordProposed:
		if r.DecisionRef != "" {
			return ErrInvalidRecordState
		}
	case RecordAccepted, RecordSuperseded:
		if r.DecisionRef == "" {
			return ErrInvalidRecordState
		}
	default:
		return ErrInvalidRecordState
	}
	index, err := r.contentIndex()
	if err != nil {
		return err
	}
	if r.State == RecordAccepted {
		return validateRequiredSavedContent(r.Manifest, index)
	}
	return nil
}

func (r SavedCompileRecord) contentIndex() (map[ContentRef]SavedContent, error) {
	index := make(map[ContentRef]SavedContent, len(r.Content))
	for _, content := range r.Content {
		if err := validateSavedContent(content, r.Manifest.Encoding); err != nil {
			return nil, err
		}
		ref := baseContentRef(content.Ref)
		if _, duplicate := index[ref]; duplicate {
			return nil, ErrReplayContentMismatch
		}
		index[ref] = content
	}
	denied := make(map[ContentRef]struct{})
	for _, ref := range r.Omitted {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		ref = baseContentRef(ref)
		if _, contradictory := index[ref]; contradictory {
			return nil, ErrReplayContentMismatch
		}
		if _, duplicate := denied[ref]; duplicate {
			return nil, ErrReplayContentMismatch
		}
		denied[ref] = struct{}{}
	}
	return index, nil
}

// Accept is an explicit host decision. Completeness is checked before issuing an
// accepted record; the original proposed record and all bytes remain unchanged.
func (r SavedCompileRecord) Accept(decisionRef string) (SavedCompileRecord, error) {
	if r.State != RecordProposed || decisionRef == "" {
		return SavedCompileRecord{}, ErrInvalidRecordState
	}
	if err := r.Validate(); err != nil {
		return SavedCompileRecord{}, err
	}
	copyRecord, err := r.Clone()
	if err != nil {
		return SavedCompileRecord{}, err
	}
	copyRecord.State, copyRecord.DecisionRef = RecordAccepted, decisionRef
	if err := copyRecord.Validate(); err != nil {
		return SavedCompileRecord{}, err
	}
	return copyRecord, nil
}

func (r SavedCompileRecord) Supersede(decisionRef string) (SavedCompileRecord, error) {
	if r.State != RecordAccepted || decisionRef == "" {
		return SavedCompileRecord{}, ErrInvalidRecordState
	}
	copyRecord, err := r.Clone()
	if err != nil {
		return SavedCompileRecord{}, err
	}
	copyRecord.State, copyRecord.DecisionRef = RecordSuperseded, decisionRef
	return copyRecord, copyRecord.Validate()
}

func (r SavedCompileRecord) Clone() (SavedCompileRecord, error) {
	wire, err := EncodeSavedRecord(r)
	if err != nil {
		return SavedCompileRecord{}, err
	}
	return DecodeSavedRecord(wire)
}

func EncodeSavedRecord(record SavedCompileRecord) ([]byte, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func DecodeSavedRecord(wire []byte) (SavedCompileRecord, error) {
	var record SavedCompileRecord
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return SavedCompileRecord{}, fmt.Errorf("%w: %w", ErrInvalidRecordState, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return SavedCompileRecord{}, ErrInvalidRecordState
	}
	if err := record.Validate(); err != nil {
		return SavedCompileRecord{}, err
	}
	return record, nil
}

type savedRequirement struct {
	ref  ContentRef
	kind SavedContentKind
}

func requiredSavedContent(manifest CompileManifest) []savedRequirement {
	var requirements []savedRequirement
	for _, resource := range manifest.Resources {
		requirements = append(requirements, resourceSavedRequirements(resource)...)
	}
	for _, ref := range manifest.ResolvedDependencies {
		requirements = append(requirements, savedRequirement{ref: ref, kind: SavedMessage})
	}
	for _, ref := range manifest.TransformResults {
		requirements = append(
			requirements,
			savedRequirement{ref: ref, kind: manifestTransformContentKind(manifest, ref)},
		)
	}
	for _, output := range manifest.Outputs {
		for _, segment := range output.Segments {
			for _, ref := range segment.Messages {
				requirements = append(requirements, savedRequirement{ref: ref, kind: SavedMessage})
			}
		}
		if output.Text != nil {
			requirements = append(requirements, savedRequirement{ref: *output.Text, kind: SavedText})
		}
		if output.Rendered != nil {
			requirements = append(requirements, savedRequirement{ref: *output.Rendered, kind: SavedMessage})
		}
	}
	for _, ref := range manifest.Artifacts {
		requirements = append(requirements, savedRequirement{ref: ref, kind: SavedArtifact})
	}
	return requirements
}

func resourceSavedRequirements(resource ResourceResolution) []savedRequirement {
	var requirements []savedRequirement
	if resource.Merge != nil {
		for _, ref := range append(slices.Clone(resource.Merge.Inputs), resource.Merge.Artifact) {
			requirements = append(requirements, savedRequirement{ref: ref, kind: SavedArtifact})
		}
		requirements = append(requirements, savedRequirement{ref: resource.Merge.Message, kind: SavedMessage})
	}
	for _, ref := range []ContentRef{resource.Selection.Resource.Content, resource.Projected, resource.Artifact} {
		requirements = append(requirements, savedRequirement{ref: ref, kind: SavedArtifact})
	}
	return append(requirements, savedRequirement{ref: resource.Estimate.Segments[0].Messages[0], kind: SavedMessage})
}

func validateRequiredSavedContent(manifest CompileManifest, index map[ContentRef]SavedContent) error {
	for _, requirement := range requiredSavedContent(manifest) {
		content, exists := index[baseContentRef(requirement.ref)]
		if !exists {
			return fmt.Errorf("%w: %s", ErrMissingReplayDependency, requirement.ref.ID)
		}
		if content.Kind != requirement.kind {
			return ErrUnsupportedReplay
		}
	}
	return nil
}

func inheritedTransformIDs(graph Lineage) []string {
	ids := make([]string, 0, len(graph.Records))
	for _, record := range graph.Records {
		ids = append(ids, record.ID)
	}
	return ids
}

// Only current transform results are required. Raw/source pass-throughs aren't
// replay dependencies when approved transformed output has already been saved.
func manifestResultRefs(outputs []ManifestOutput, inherited []string) []ContentRef {
	prior := make(map[string]struct{}, len(inherited))
	for _, id := range inherited {
		prior[id] = struct{}{}
	}
	var refs []ContentRef
	for _, output := range outputs {
		for _, record := range output.Lineage.Records {
			if _, old := prior[record.ID]; old || record.Stage == traceStageSource {
				continue
			}
			refs = append(refs, record.Outputs...)
		}
	}
	return uniqueContentRefs(refs)
}

func validateManifestGeneratedResults(manifest CompileManifest) error {
	known := make(map[string]struct{})
	for _, output := range manifest.Outputs {
		for _, record := range output.Lineage.Records {
			known[record.ID] = struct{}{}
		}
	}
	prior := make(map[string]struct{})
	for _, id := range manifest.InheritedTransforms {
		if _, exists := known[id]; !exists {
			return ErrInvalidManifest
		}
		if _, duplicate := prior[id]; duplicate {
			return ErrInvalidManifest
		}
		prior[id] = struct{}{}
	}
	expected := manifestResultRefs(manifest.Outputs, manifest.InheritedTransforms)
	if !slices.Equal(expected, manifest.TransformResults) {
		return ErrInvalidManifest
	}
	return nil
}
