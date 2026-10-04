package contexty

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// ReplayExpectation is caller-owned current intent, not execution config.
// Copy a saved manifest only when its identities are still the desired ones.
type ReplayExpectation struct {
	ManifestDigest       string
	SourceRevision       int64
	Encoding             Descriptor
	Profile              RecordProfile
	Stages               map[string]Descriptor
	Inputs               []ManifestSegment
	Budgets              []ManifestBudget
	ArtifactBudgets      []ArtifactBudgetRequest
	TraceConfiguration   TraceConfiguration
	CompileConfiguration CompileConfiguration
}

func ReplayExpectationFor(manifest CompileManifest) (ReplayExpectation, error) {
	if err := manifest.Validate(); err != nil {
		return ReplayExpectation{}, err
	}
	inputs := make([]ManifestSegment, len(manifest.Inputs))
	for i, segment := range manifest.Inputs {
		inputs[i] = ManifestSegment{Name: segment.Name, Messages: slices.Clone(segment.Messages)}
	}
	return ReplayExpectation{
		ManifestDigest: manifest.Digest,
		SourceRevision: manifest.SourceRevision,
		Encoding:       manifest.Encoding,
		Profile:        manifest.Profile.clone(),
		Stages:         maps.Clone(manifest.Stages),
		Inputs:         inputs,
		Budgets:        cloneManifestBudgets(manifest.Budgets),
		ArtifactBudgets: slices.Clone(
			manifest.ArtifactBudgets,
		),
		TraceConfiguration:   manifest.TraceConfiguration.clone(),
		CompileConfiguration: manifest.CompileConfiguration.clone(),
	}, nil
}

// ReplayedOutput has no executable Source or misleading reconstructed snapshot.
// Its bytes and metadata derive exclusively from accepted saved content.
type ReplayedOutput struct {
	Artifacts       []ContextArtifact
	Selection       *SelectionDecision
	Kind            ManifestOutputKind
	Name            string
	Segments        map[string][]Message
	WireSegments    map[string][]json.RawMessage
	Text            string
	Rendered        *Message
	Lineage         Lineage
	Transformations map[string]TransformChain
}

type ReplayResult struct {
	Manifest      CompileManifest
	Outputs       []ReplayedOutput
	Artifacts     []ContextArtifact
	WireArtifacts []json.RawMessage
}

// Replay never executes compilation or fetches content. Missing/deleted records
// fail before any output is issued. Host must supply currently authorized bytes.
// Blob-bearing outputs additionally require WithReplayBlobAvailability: live
// metadata is checked under a fresh host scope without Get or resource refetch.
func Replay(
	ctx context.Context,
	record SavedCompileRecord,
	expected ReplayExpectation,
	codec JSONSerializer,
	options ...ReplayOption,
) (ReplayResult, error) {
	if err := ctx.Err(); err != nil {
		return ReplayResult{}, err
	}
	availability, optionErr := prepareReplayOptions(options)
	if optionErr != nil {
		return ReplayResult{}, optionErr
	}
	if record.State != RecordAccepted {
		return ReplayResult{}, ErrUnsupportedReplay
	}
	if err := record.Validate(); err != nil {
		return ReplayResult{}, err
	}
	if err := validateReplayExpectation(record.Manifest, expected); err != nil {
		return ReplayResult{}, err
	}
	if err := validateReplayResourceCodecs(record.Manifest, availability.resources); err != nil {
		return ReplayResult{}, err
	}
	copyRecord, err := record.Clone()
	if err != nil {
		return ReplayResult{}, err
	}
	index, err := copyRecord.contentIndex()
	if err != nil {
		return ReplayResult{}, err
	}
	if err = validateReplayResources(ctx, copyRecord.Manifest, index, availability.resources); err != nil {
		return ReplayResult{}, err
	}
	if err = validateReplayResourceAppends(ctx, copyRecord.Manifest, index, codec, availability.resources); err != nil {
		return ReplayResult{}, err
	}
	if err = validateReplayMessageCodecs(ctx, copyRecord.Manifest, index, codec); err != nil {
		return ReplayResult{}, err
	}
	result := ReplayResult{Manifest: copyRecord.Manifest, Outputs: nil, Artifacts: nil, WireArtifacts: nil}
	for _, output := range copyRecord.Manifest.Outputs {
		if cancelErr := ctx.Err(); cancelErr != nil {
			return ReplayResult{}, cancelErr
		}
		decoded, decodeErr := replayOutput(ctx, output, index, codec)
		if decodeErr != nil {
			return ReplayResult{}, decodeErr
		}

		result.Outputs = append(result.Outputs, decoded)
	}
	result.Artifacts, result.WireArtifacts, err = replayArtifacts(ctx, copyRecord.Manifest.Artifacts, index, codec)
	if err != nil {
		return ReplayResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReplayResult{}, err
	}
	if err := checkReplayBlobs(ctx, result, availability); err != nil {
		return ReplayResult{}, err
	}
	return result, nil
}

func replayArtifacts(ctx context.Context, refs []ContentRef, index map[ContentRef]SavedContent,
	codec JSONSerializer,
) ([]ContextArtifact, []json.RawMessage, error) {
	var artifacts []ContextArtifact
	var wires []json.RawMessage
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		wire := index[baseContentRef(ref)].Wire
		artifact, err := UnmarshalArtifactJSON(wire, codec.Extensions)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrReplayCodec, err)
		}
		decodedRef, err := ArtifactContentRef(artifact)
		if err != nil || decodedRef != baseContentRef(ref) {
			return nil, nil, ErrReplayContentMismatch
		}
		artifacts = append(artifacts, artifact.Clone())
		wires = append(wires, slices.Clone(wire))
	}
	return artifacts, wires, nil
}

func validateReplayMessageCodecs(
	ctx context.Context, manifest CompileManifest,
	index map[ContentRef]SavedContent,
	codec JSONSerializer,
) error {
	checked := make(map[ContentRef]struct{})
	for _, requirement := range requiredSavedContent(manifest) {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref := baseContentRef(requirement.ref)
		if requirement.kind != SavedMessage || isResourceMessage(manifest, ref) {
			continue
		}
		if _, duplicate := checked[ref]; duplicate {
			continue
		}
		if _, err := replayMessage(ref, index, codec); err != nil {
			return err
		}
		checked[ref] = struct{}{}
	}
	return nil
}

func validateReplayExpectation(manifest CompileManifest, expected ReplayExpectation) error {
	if manifest.Digest != expected.ManifestDigest || manifest.SourceRevision != expected.SourceRevision ||
		manifest.Encoding != expected.Encoding || !maps.Equal(manifest.Stages, expected.Stages) {
		return ErrReplayMismatch
	}
	actualFields := struct {
		Profile              RecordProfile           `json:"profile"`
		Inputs               []ManifestSegment       `json:"inputs"`
		Budgets              []ManifestBudget        `json:"budgets"`
		ArtifactBudgets      []ArtifactBudgetRequest `json:"artifact_budgets"`
		TraceConfiguration   TraceConfiguration      `json:"trace_configuration"`
		CompileConfiguration CompileConfiguration    `json:"compile_configuration"`
	}{
		Profile: manifest.Profile, Inputs: manifest.Inputs, Budgets: manifest.Budgets,
		ArtifactBudgets: manifest.ArtifactBudgets, TraceConfiguration: manifest.TraceConfiguration,
		CompileConfiguration: manifest.CompileConfiguration}
	expectedFields := struct {
		Profile              RecordProfile           `json:"profile"`
		Inputs               []ManifestSegment       `json:"inputs"`
		Budgets              []ManifestBudget        `json:"budgets"`
		ArtifactBudgets      []ArtifactBudgetRequest `json:"artifact_budgets"`
		TraceConfiguration   TraceConfiguration      `json:"trace_configuration"`
		CompileConfiguration CompileConfiguration    `json:"compile_configuration"`
	}{
		Profile: expected.Profile.clone(), Inputs: expected.Inputs, Budgets: expected.Budgets,
		ArtifactBudgets: expected.ArtifactBudgets, TraceConfiguration: expected.TraceConfiguration.clone(),
		CompileConfiguration: expected.CompileConfiguration.clone()}
	actualWire, err := json.Marshal(actualFields)
	if err != nil {
		return err
	}
	expectedWire, err := json.Marshal(expectedFields)
	if err != nil {
		return err
	}
	actualDigest, err := canonicalJSONDigest(actualWire)
	if err != nil {
		return err
	}
	expectedDigest, err := canonicalJSONDigest(expectedWire)
	if err != nil {
		return err
	}
	if actualDigest != expectedDigest {
		return ErrReplayMismatch
	}
	return nil
}

func replayOutput(
	ctx context.Context,
	output ManifestOutput,
	index map[ContentRef]SavedContent,
	codec JSONSerializer,
) (ReplayedOutput, error) {
	result := ReplayedOutput{
		Artifacts:       nil,
		Selection:       output.Selection.clone(),
		Kind:            output.Kind,
		Name:            output.Name,
		Segments:        make(map[string][]Message),
		WireSegments:    make(map[string][]json.RawMessage),
		Text:            "",
		Rendered:        nil,
		Lineage:         output.Lineage.Clone(),
		Transformations: cloneTransformRecords(output.Transformations),
	}
	for _, segment := range output.Segments {
		messages := make([]Message, 0, len(segment.Messages))
		wireMessages := make([]json.RawMessage, 0, len(segment.Messages))
		for _, ref := range segment.Messages {
			message, err := replayMessage(ref, index, codec)
			if err != nil {
				return ReplayedOutput{}, err
			}
			messages = append(messages, message)
			wireMessages = append(wireMessages, slices.Clone(index[baseContentRef(ref)].Wire))
		}
		result.Segments[segment.Name] = messages
		result.WireSegments[segment.Name] = wireMessages
	}
	if output.Rendered != nil {
		message, err := replayMessage(*output.Rendered, index, codec)
		if err != nil {
			return ReplayedOutput{}, err
		}
		result.Rendered = &message
	}
	if output.Text != nil {
		var rendered struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(index[baseContentRef(*output.Text)].Wire, &rendered); err != nil {
			return ReplayedOutput{}, fmt.Errorf("%w: %w", ErrReplayCodec, err)
		}
		result.Text = rendered.Text
	}
	artifacts, _, artifactErr := replayArtifacts(ctx, output.ArtifactRefs, index, codec)
	if artifactErr != nil {
		return ReplayedOutput{}, artifactErr
	}
	result.Artifacts = artifacts
	return result, nil
}

func replayMessage(ref ContentRef, index map[ContentRef]SavedContent, codec JSONSerializer) (Message, error) {
	var message Message
	if err := codec.Unmarshal(index[baseContentRef(ref)].Wire, &message); err != nil {
		return Message{}, fmt.Errorf("%w: %w", ErrReplayCodec, err)
	}
	decoded, err := MessageContentRef(message, codec)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %w", ErrReplayCodec, err)
	}
	if decoded != baseContentRef(ref) {
		return Message{}, ErrReplayContentMismatch
	}
	return message.Clone(), nil
}
