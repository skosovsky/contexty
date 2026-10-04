package contexty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
)

var ErrInvalidManifest = errors.New("contexty: invalid compile manifest")

// RecordProfile pins host-owned behavior. Descriptors do not store executions.
// Pipeline covers all configured hooks, policies, formatters and resolve inputs.
// Targets separately identify each target's rendering and transformation policy.
type RecordProfile struct {
	Pipeline   Descriptor            `json:"pipeline"`
	Model      Descriptor            `json:"model"`
	Prompt     Descriptor            `json:"prompt"`
	Estimator  Descriptor            `json:"estimator"`
	Rendering  Descriptor            `json:"rendering"`
	Targets    map[string]Descriptor `json:"targets"`
	Components []RecordingComponent  `json:"components"`
}

func (p RecordProfile) clone() RecordProfile {
	p.Targets = maps.Clone(p.Targets)
	p.Components = cloneRecordingComponents(p.Components)
	return p
}

func (p RecordProfile) validate() error {
	for _, descriptor := range []Descriptor{p.Pipeline, p.Model, p.Prompt, p.Estimator, p.Rendering} {
		if err := descriptor.Validate(); err != nil {
			return err
		}
	}
	for name, descriptor := range p.Targets {
		if name == "" || name != strings.TrimSpace(name) {
			return ErrInvalidDescriptor
		}
		if err := descriptor.Validate(); err != nil {
			return err
		}
	}
	return validateRecordingComponents(p.Components)
}

// WithCompileRecording opts into manifest creation. A TraceProfile is required.
func WithCompileRecording(profile RecordProfile) EngineOption {
	return func(e *Engine) {
		copyProfile := profile.clone()
		e.recording = &copyProfile
	}
}

func (e *Engine) validateCompileConfiguration(request CompileRequest) error {
	if err := e.validateOutputPolicies(request); err != nil {
		return err
	}
	if e.capture != nil && (e.recording == nil || e.capture.policy == nil) {
		return ErrMissingRecordPolicy
	}
	if e.recording == nil {
		return nil
	}
	if e.trace == nil {
		return fmt.Errorf("%w: recording requires tracing", ErrInvalidManifest)
	}
	if err := e.recording.validate(); err != nil {
		return err
	}
	if err := e.validateRecordingBudgets(request.Targets); err != nil {
		return err
	}
	if _, err := e.trace.configuration(request.RequireDurableIdentity); err != nil {
		return err
	}
	if _, err := e.deferredConfiguration(); err != nil {
		return err
	}
	if e.capture != nil {
		if err := e.capture.descriptor.Validate(); err != nil {
			return err
		}
	}
	if len(e.recording.Targets) != len(request.Targets) {
		return ErrInvalidDescriptor
	}
	for _, target := range request.Targets {
		if _, declared := e.recording.Targets[strings.TrimSpace(target.Name)]; !declared {
			return ErrInvalidDescriptor
		}
	}
	return e.validateRecordingComponentTopology(request)
}

func (e *Engine) validateOutputPolicies(request CompileRequest) error {
	if e.artifactMaterialization != nil || len(request.Artifacts) > 0 {
		if err := validateMaterializationPolicy(e.artifactMaterialization); err != nil {
			return err
		}
	}
	if err := validateOutputPolicy(e.outputPolicy); err != nil {
		return err
	}

	if e.selection != nil {
		if selectionErr := e.selection.validate(); selectionErr != nil {
			return selectionErr
		}
	}
	if err := e.validateDeferredResources(); err != nil {
		return err
	}
	if err := e.validateCompactionConfiguration(request.Targets); err != nil {
		return err
	}
	if err := e.validateInputBudgets(request.Targets); err != nil {
		return err
	}
	return nil
}

// ManifestSegment preserves input order and segment membership, including the
// distinct raw/prompt-safe/persistence projections of a current turn.
type ManifestSegment struct {
	Name     string       `json:"name"`
	Messages []ContentRef `json:"messages"`
}

// ManifestBudget records the actual input limit, not a host's descriptor claim.
type ManifestBudget struct {
	Retention        RetentionPolicy       `json:"retention"`
	CompactionPolicy *CompactionPolicy     `json:"compaction_policy,omitempty"`
	Decision         *BudgetDecision       `json:"decision,omitempty"`
	Kind             ManifestOutputKind    `json:"kind"`
	Target           string                `json:"target"`
	TokenLimit       int                   `json:"token_limit"`
	EstimatedTokens  int                   `json:"estimated_tokens"`
	Request          BudgetRequest         `json:"request"`
	ReportProfile    *EstimateProfile      `json:"report_profile,omitempty"`
	RollingSummary   *RollingSummaryPolicy `json:"rolling_summary,omitempty"`
	Compaction       *CompactionProfile    `json:"compaction,omitempty"`
	Truncation       TruncationProfile     `json:"truncation"`
	Summarizer       *Descriptor           `json:"summarizer,omitempty"`
	Estimator        EstimatorIdentity     `json:"estimator"`
}

type manifestChannelKey struct {
	kind ManifestOutputKind
	name string
}
type finalBudgetEvidenceKey struct{}

func recordFinalBudgetEstimate(ctx context.Context, tokens int) {
	counts, _ := ctx.Value(finalBudgetEvidenceKey{}).(map[manifestChannelKey]int)
	if counts == nil {
		return
	}
	counts[finalBudgetChannel(ctx)] = tokens
}

type ManifestOutputKind string

const (
	ManifestMainOutput   ManifestOutputKind = "main"
	ManifestTargetOutput ManifestOutputKind = "target"
)

// ManifestOutput identifies one compiled channel and its transformation evidence.
// Text is a reference to rendered content, never the rendered payload itself.
type ManifestOutput struct {
	OpaqueState       *OpaqueStateDecision      `json:"opaque_state,omitempty"`
	OutputPolicy      *OutputPolicyDecision     `json:"output_policy,omitempty"`
	Kind              ManifestOutputKind        `json:"kind"`
	Name              string                    `json:"name"`
	Segments          []ManifestSegment         `json:"segments"`
	Text              *ContentRef               `json:"text,omitempty"`
	Rendered          *ContentRef               `json:"rendered,omitempty"`
	Lineage           Lineage                   `json:"lineage"`
	Transformations   map[string]TransformChain `json:"transformations"`
	SourceSegments    []SegmentName             `json:"source_segments,omitempty"`
	Selection         *SelectionDecision        `json:"selection,omitempty"`
	ArtifactRefs      []ContentRef              `json:"artifact_refs,omitempty"`
	ArtifactEstimates []ArtifactBudgetEstimate  `json:"artifact_estimates,omitempty"`
	ArtifactBudgets   []ArtifactBudgetRequest   `json:"artifact_budgets,omitempty"`
	ExcludedArtifacts []ArtifactExclusion       `json:"excluded_artifacts,omitempty"`
	View              ViewType                  `json:"view,omitempty"`
}

// CompileManifest is local reproducibility metadata, not an isolated export.
// It intentionally contains no raw content, callbacks, codecs or backend handles.
type CompileManifest struct {
	Materializations     []ArtifactMaterializationDecision `json:"materializations"`
	PreparedInputs       []ManifestSegment                 `json:"prepared_inputs"`
	Resources            []ResourceResolution              `json:"resources"`
	ID                   string                            `json:"id"`
	TurnID               string                            `json:"turn_id"`
	Digest               string                            `json:"digest"`
	SourceRevision       int64                             `json:"source_revision"`
	Encoding             Descriptor                        `json:"encoding"`
	Profile              RecordProfile                     `json:"profile"`
	Stages               map[string]Descriptor             `json:"stages"`
	Inputs               []ManifestSegment                 `json:"inputs"`
	Budgets              []ManifestBudget                  `json:"budgets"`
	Outputs              []ManifestOutput                  `json:"outputs"`
	ResolvedDependencies []ContentRef                      `json:"resolved_dependencies"`
	Artifacts            []ContentRef                      `json:"artifacts"`
	Privacy              *Descriptor                       `json:"privacy,omitempty"`
	InheritedTransforms  []string                          `json:"inherited_transforms"`
	TransformResults     []ContentRef                      `json:"transform_results"`
	PreviousRecord       *ContentRef                       `json:"previous_record,omitempty"`
	Coverage             []ManifestCoverage                `json:"coverage"`
	ExcludedArtifacts    []ArtifactExclusion               `json:"excluded_artifacts"`
	EstimateReports      []ManifestEstimateReport          `json:"estimate_reports"`
	ArtifactBudgets      []ArtifactBudgetRequest           `json:"artifact_budgets"`
	ArtifactEstimates    []ArtifactBudgetEstimate          `json:"artifact_estimates"`
	Compactions          []ManifestCompaction              `json:"compactions"`
	TraceConfiguration   TraceConfiguration                `json:"trace_configuration"`
	CompileConfiguration CompileConfiguration              `json:"compile_configuration"`
}

func (e *Engine) buildCompileManifest(ctx context.Context, result CompileResult) (CompileManifest, error) {
	if e.trace == nil {
		return CompileManifest{}, fmt.Errorf("%w: recording requires tracing", ErrInvalidManifest)
	}
	if err := e.recording.validate(); err != nil {
		return CompileManifest{}, err
	}
	inputs, err := manifestInputs(result.Source, e.trace.Codec)
	if err != nil {
		return CompileManifest{}, err
	}
	inputs, err = appendResourceArtifactInputs(ctx, inputs)
	if err != nil {
		return CompileManifest{}, err
	}
	budgets, err := e.manifestBudgets(ctx, result.Source.Targets)
	if err != nil {
		return CompileManifest{}, err
	}
	preparedInputs, preparedErr := preparedManifestInputs(ctx, result.PreparedSnapshot, e.trace.Codec)
	if preparedErr != nil {
		return CompileManifest{}, preparedErr
	}
	outputs, err := manifestOutputs(ctx, result, e.trace.Codec)
	if err != nil {
		return CompileManifest{}, err
	}
	artifacts, err := manifestArtifactRefs(result.Artifacts)
	if err != nil {
		return CompileManifest{}, err
	}
	exclusions, err := manifestArtifactExclusions(ctx, result)
	if err != nil {
		return CompileManifest{}, err
	}
	reports, err := compileEstimateReports(ctx)
	if err != nil {
		return CompileManifest{}, err
	}
	artifactBudgets := artifactRequestsFromEstimates(result.ArtifactEstimates)
	artifactEstimates := cloneValidArtifactEstimates(result.ArtifactEstimates)
	configuration, err := e.trace.configuration(result.Source.RequireDurableIdentity)
	if err != nil {
		return CompileManifest{}, err
	}
	compileConfiguration, err := e.compileConfiguration(ctx)
	if err != nil {
		return CompileManifest{}, err
	}
	manifest := CompileManifest{
		Materializations:     materializationDecisions(ctx),
		PreparedInputs:       preparedInputs,
		Resources:            nil,
		ID:                   result.Source.CompilationID,
		TurnID:               result.Source.TurnID,
		Digest:               "",
		SourceRevision:       result.Source.SourceRevision,
		Encoding:             e.trace.Encoding,
		Profile:              e.recording.clone(),
		Stages:               maps.Clone(e.trace.Stages),
		Inputs:               inputs,
		Budgets:              budgets,
		Outputs:              outputs,
		ResolvedDependencies: resolvedManifestRefs(result.Lineage, result.Source.Lineage),
		Artifacts:            artifacts,
		Privacy:              nil,
		InheritedTransforms:  inheritedTransformIDs(result.Source.Lineage),
		TransformResults:     nil,
		PreviousRecord:       cloneContentRef(result.Source.PreviousRecord),
		Coverage:             nil,
		ExcludedArtifacts:    exclusions,
		EstimateReports:      reports,
		ArtifactBudgets:      artifactBudgets,
		ArtifactEstimates:    artifactEstimates,
		Compactions:          nil,
		TraceConfiguration:   configuration,
		CompileConfiguration: compileConfiguration,
	}
	manifest.Resources, err = manifestResourceResolutions(ctx, result.Source)
	if err != nil {
		return CompileManifest{}, err
	}
	manifest.Coverage = manifestCoverage(manifest)
	manifest.TransformResults = manifestResultRefs(manifest.Outputs, manifest.InheritedTransforms)
	if e.capture != nil {
		descriptor := e.capture.descriptor
		manifest.Privacy = &descriptor
	}
	manifest.Digest, err = manifest.contentDigest()
	if err != nil {
		return CompileManifest{}, err
	}
	// This internal draft is sealed and validated after output privacy decisions
	// and compaction links are finalized. It must not escape compilation yet.
	return manifest, nil
}

func (m CompileManifest) Clone() (CompileManifest, error) {
	wire, err := EncodeManifest(m)
	if err != nil {
		return CompileManifest{}, err
	}
	return DecodeManifest(wire)
}

func manifestInputs(source CompileRequest, codec JSONSerializer) ([]ManifestSegment, error) {
	inputs, err := manifestSnapshotSegments(source.ToSnapshot(), codec)
	if err != nil {
		return nil, err
	}
	pending, err := manifestSegment("pending", source.Pending, codec)
	if err != nil {
		return nil, err
	}
	inputs = append(inputs, pending)
	artifacts, err := manifestArtifactRefs(source.Artifacts)
	if err != nil {
		return nil, err
	}
	inputs = append(inputs, ManifestSegment{Name: manifestArtifactsSegment, Messages: artifacts})
	if source.CurrentTurn == nil {
		return inputs, nil
	}
	turn := source.CurrentTurn
	projections := []struct {
		name    string
		message Message
		present bool
	}{{name: "current/raw", message: turn.Raw, present: true}}
	prompt, hasPrompt := turn.promptMessage()
	persisted, hasPersisted := turn.persistedMessage()
	projections = append(projections,
		struct {
			name    string
			message Message
			present bool
		}{name: "current/prompt", message: prompt, present: hasPrompt},
		struct {
			name    string
			message Message
			present bool
		}{name: "current/persistence", message: persisted, present: hasPersisted})
	for _, projection := range projections {
		if !projection.present {
			continue
		}
		segment, segmentErr := manifestSegment(projection.name, []Message{projection.message}, codec)
		if segmentErr != nil {
			return nil, segmentErr
		}
		inputs = append(inputs, segment)
	}
	return inputs, nil
}

// ArtifactContentRef hashes the complete typed artifact wire representation.
// Like a message digest, it grants neither access nor permission to dereference.
func ArtifactContentRef(artifact ContextArtifact) (ContentRef, error) {
	if artifact.ID == "" {
		return ContentRef{}, ErrInvalidContentRef
	}
	if err := validateArtifactBlob(artifact); err != nil {
		return ContentRef{}, err
	}
	wire, err := json.Marshal(artifact)
	if err != nil {
		return ContentRef{}, err
	}
	digest, err := canonicalJSONDigest(wire)
	return ContentRef{ID: artifact.ID, Digest: digest, Occurrence: ""}, err
}

func manifestArtifactRefs(artifacts []ContextArtifact) ([]ContentRef, error) {
	refs := make([]ContentRef, 0, len(artifacts))
	for _, artifact := range artifacts {
		ref, err := ArtifactContentRef(artifact)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func manifestSnapshotSegments(snap ConversationSnapshot, codec JSONSerializer) ([]ManifestSegment, error) {
	var result []ManifestSegment
	for _, name := range snapshotSegmentOrder() {
		segment, err := manifestSegment(string(name), snap.Segment(name), codec)
		if err != nil {
			return nil, err
		}
		result = append(result, segment)
	}
	return result, nil
}

func manifestSegment(name string, messages []Message, codec JSONSerializer) (ManifestSegment, error) {
	refs := make([]ContentRef, 0, len(messages))
	for _, message := range messages {
		ref, err := MessageContentRef(message, codec)
		if err != nil {
			return ManifestSegment{}, err
		}
		refs = append(refs, ref)
	}
	return ManifestSegment{Name: name, Messages: refs}, nil
}

func preparedManifestInputs(
	ctx context.Context,
	snap ConversationSnapshot,
	codec JSONSerializer,
) ([]ManifestSegment, error) {
	inputs, err := manifestSnapshotSegments(snap, codec)
	if err != nil {
		return nil, err
	}
	prepared, _ := ctx.Value(preparedOutputKey{}).(preparedOutput)
	pending, err := manifestSegment("current/prompt", prepared.pending, codec)
	if err != nil {
		return nil, err
	}
	return append(inputs, pending), nil
}

func manifestOutputs(ctx context.Context, result CompileResult, codec JSONSerializer) ([]ManifestOutput, error) {
	snap := EmptySnapshot().WithSegment(SegmentSystem, result.Payload.System).
		WithSegment(SegmentHistory, result.Payload.History).WithSegment(SegmentTools, result.Payload.Tools).
		WithSegment(SegmentMemory, result.Payload.Memory)
	segments, err := manifestSnapshotSegments(snap, codec)
	if err != nil {
		return nil, err
	}
	outputs := []ManifestOutput{
		{
			Selection:    result.Selection.clone(),
			OutputPolicy: compileOutputPolicyDecision(ctx, ManifestMainOutput, string(ManifestMainOutput)),
			OpaqueState:  compileOpaqueStateDecision(ctx, ManifestMainOutput, string(ManifestMainOutput)),
			ArtifactRefs: nil, ArtifactEstimates: nil, ArtifactBudgets: nil, ExcludedArtifacts: nil,
			Kind:            ManifestMainOutput,
			Name:            string(ManifestMainOutput),
			Segments:        segments,
			Text:            nil,
			Rendered:        nil,
			Lineage:         result.Lineage.Clone(),
			Transformations: cloneTransformRecords(result.Transformations),
			SourceSegments:  nil,
			View:            "",
		},
	}
	names := make([]string, 0, len(result.Projections))
	for name := range result.Projections {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		projection := result.Projections[name]
		segment, segmentErr := manifestSegment("messages", projection.Messages, codec)
		if segmentErr != nil {
			return nil, segmentErr
		}
		textRef, textErr := renderedTextRef(name, projection.Text)
		if textErr != nil {
			return nil, textErr
		}
		artifactRefs, artifactErr := manifestArtifactRefs(projection.Artifacts)
		if artifactErr != nil {
			return nil, artifactErr
		}
		output := ManifestOutput{
			Selection:         projection.Selection.clone(),
			OutputPolicy:      compileOutputPolicyDecision(ctx, ManifestTargetOutput, name),
			OpaqueState:       compileOpaqueStateDecision(ctx, ManifestTargetOutput, name),
			ArtifactRefs:      append([]ContentRef(nil), artifactRefs...),
			ArtifactEstimates: cloneValidArtifactEstimates(projection.ArtifactEstimates),
			ArtifactBudgets:   artifactRequestsFromEstimates(projection.ArtifactEstimates),
			ExcludedArtifacts: append([]ArtifactExclusion(nil), projection.ExcludedArtifacts...),
			Kind:              ManifestTargetOutput,
			Name:              name,
			Segments:          []ManifestSegment{segment},
			Text:              &textRef,
			Rendered:          nil,
			Lineage:           projection.Lineage.Clone(),
			Transformations:   cloneTransformRecords(projection.Transformations),
			SourceSegments:    nil,
			View:              "",
		}
		for _, target := range result.Source.Targets {
			if target.Name != name {
				continue
			}
			output.View = ViewType(target.View)
			output.SourceSegments = append([]SegmentName(nil), target.Segments...)
		}
		if projection.Rendered != nil {
			ref := projection.Rendered.Ref
			output.Rendered = &ref
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func renderedTextRef(name, text string) (ContentRef, error) {
	wire, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: text})
	if err != nil {
		return ContentRef{}, err
	}
	digest, err := canonicalJSONDigest(wire)
	return ContentRef{ID: "target/" + name + "/text", Digest: digest, Occurrence: ""}, err
}

func (e *Engine) manifestBudgets(ctx context.Context, targets []CompileTarget) ([]ManifestBudget, error) {
	var budgets []ManifestBudget
	if e.budget != nil {
		budget, err := e.budget.manifestBudget(ManifestMainOutput, string(ManifestMainOutput))
		if err != nil {
			return nil, err
		}
		budgets = append(budgets, budget)
	}
	for _, target := range targets {
		if target.Budget != nil {
			budget, err := target.Budget.manifestBudget(ManifestTargetOutput, target.Name)
			if err != nil {
				return nil, err
			}
			budgets = append(budgets, budget)
		}
	}
	slices.SortFunc(budgets, func(a, b ManifestBudget) int {
		if a.Target < b.Target {
			return -1
		}
		if a.Target > b.Target {
			return 1
		}
		return 0
	})
	counts, _ := ctx.Value(finalBudgetEvidenceKey{}).(map[manifestChannelKey]int)
	for i, budget := range budgets {
		count, found := counts[manifestChannelKey{kind: budget.Kind, name: budget.Target}]
		if !found {
			return nil, ErrInvalidManifest
		}
		budgets[i].EstimatedTokens = count
		decisions, _ := ctx.Value(budgetDecisionsKey{}).(map[manifestChannelKey]BudgetDecision)
		if decision, exists := decisions[manifestChannelKey{kind: budget.Kind, name: budget.Target}]; exists {
			copyDecision := decision.clone()
			budgets[i].Decision = &copyDecision
		}
	}
	return budgets, nil
}

func newManifestBudget(kind ManifestOutputKind, name string, request BudgetRequest) (ManifestBudget, error) {
	limit, err := request.Resolve()
	if err != nil {
		return ManifestBudget{}, err
	}
	return ManifestBudget{
		Kind:             kind,
		Target:           name,
		TokenLimit:       limit,
		EstimatedTokens:  0,
		Request:          request,
		Retention:        RetentionPolicy{MessageIDs: nil, ContentRefs: nil, Roles: nil},
		CompactionPolicy: nil, Decision: nil,
		ReportProfile:  nil,
		RollingSummary: nil,
		Compaction:     nil,
		Truncation:     TruncationProfile{Descriptor: Descriptor{ID: "", Revision: ""}, DropHead: nil},
		Summarizer:     nil,
		Estimator:      EstimatorIdentity{Descriptor: Descriptor{ID: "", Revision: ""}, Fixed: nil, Character: nil},
	}, nil
}

func (p *BudgetPipeline) manifestBudget(kind ManifestOutputKind, name string) (ManifestBudget, error) {
	budget, err := newManifestBudget(kind, name, p.cfg.Budget)
	if err != nil {
		return ManifestBudget{}, err
	}
	budget.ReportProfile = p.reportProfile()
	budget.Estimator, err = p.estimatorIdentity()
	if err != nil {
		return ManifestBudget{}, err
	}
	budget.RollingSummary = cloneRollingSummary(p.rolling)
	budget.Retention = p.cfg.Retention.clone()
	budget.CompactionPolicy = cloneCompactionPolicy(p.cfg.Compaction)
	budget.Compaction = cloneCompactionProfile(p.compaction)
	budget.Summarizer, err = p.summarizerDescriptor()
	if err != nil {
		return ManifestBudget{}, err
	}
	budget.Truncation, err = p.truncationProfile()
	return budget, err
}

func resolvedManifestRefs(graph, inherited Lineage) []ContentRef {
	prior := make(map[string]struct{}, len(inherited.Records))
	for _, record := range inherited.Records {
		prior[record.ID] = struct{}{}
	}
	var refs []ContentRef
	for _, record := range graph.Records {
		_, alreadyRecorded := prior[record.ID]
		if record.Stage == traceStageDeferred && !alreadyRecorded {
			refs = append(refs, record.Outputs...)
		}
	}
	return uniqueContentRefs(refs)
}

func (m CompileManifest) contentDigest() (string, error) {
	m.Digest = ""
	wire, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return canonicalJSONDigest(wire)
}

// EncodeManifest validates before serialization; DecodeManifest checks the full
// self-digest so altered budgets/content/metadata cannot pass unnoticed.
func EncodeManifest(manifest CompileManifest) ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(manifest)
}

func DecodeManifest(wire []byte) (CompileManifest, error) {
	var manifest CompileManifest
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return CompileManifest{}, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return CompileManifest{}, ErrInvalidManifest
	}
	if err := manifest.Validate(); err != nil {
		return CompileManifest{}, err
	}
	return manifest, nil
}

func (m CompileManifest) Validate() error {
	if err := m.validateConfiguration(); err != nil {
		return err
	}
	if m.ID == "" || m.SourceRevision < 0 {
		return ErrInvalidManifest
	}
	if err := m.Encoding.Validate(); err != nil {
		return err
	}
	if err := m.Profile.validate(); err != nil {
		return err
	}
	if err := validateManifestStages(m.Stages); err != nil {
		return err
	}
	if err := m.validateInputsAndOutputs(); err != nil {
		return err
	}
	if err := validateManifestBudgets(m.Budgets, m.Outputs); err != nil {
		return err
	}
	if err := validateManifestRefs(m.ResolvedDependencies); err != nil {
		return err
	}
	if err := validateManifestRefs(m.Artifacts); err != nil {
		return err
	}
	if err := validateManifestOptionalRefs(m); err != nil {
		return err
	}
	if err := validateManifestGeneratedResults(m); err != nil {
		return err
	}
	if err := validateManifestCoverage(m); err != nil {
		return err
	}
	if err := validateManifestEstimateReports(m); err != nil {
		return err
	}
	if err := validateArtifactEstimates(m); err != nil {
		return err
	}
	if err := validateManifestCompactions(m); err != nil {
		return err
	}
	if err := validateManifestSummarizers(m); err != nil {
		return err
	}
	digest, err := m.contentDigest()
	if err != nil || digest != m.Digest {
		return ErrInvalidManifest
	}
	return nil
}

func (m CompileManifest) validateInputsAndOutputs() error {
	if err := validateManifestSegments(m.Inputs); err != nil {
		return err
	}
	if err := validateManifestSegments(m.PreparedInputs); err != nil {
		return err
	}
	if err := validateManifestOutputs(m.Outputs, m.Profile.Targets); err != nil {
		return err
	}
	return nil
}

func (m CompileManifest) validateConfiguration() error {
	if err := validateManifestResources(m); err != nil {
		return err
	}
	if err := m.CompileConfiguration.validate(m.Profile); err != nil {
		return err
	}
	if err := validateManifestPolicyEvidence(m); err != nil {
		return err
	}
	if err := validateManifestOpaqueEvidence(m); err != nil {
		return err
	}
	return m.TraceConfiguration.validate()
}

func validateManifestOptionalRefs(manifest CompileManifest) error {
	if manifest.Privacy != nil {
		if err := manifest.Privacy.Validate(); err != nil {
			return err
		}
	}
	return validateEstimateRefs(manifest.PreviousRecord)
}

func validateManifestBudgets(budgets []ManifestBudget, outputs []ManifestOutput) error {
	seen := make(map[manifestChannelKey]struct{})
	for _, budget := range budgets {
		if err := budget.validateComponents(); err != nil {
			return err
		}
		limit, err := budget.Request.Resolve()
		if err != nil {
			return err
		}
		if limit != budget.TokenLimit {
			return ErrInvalidBudgetRequest
		}
		if budget.TokenLimit < 0 || budget.EstimatedTokens < 0 ||
			!manifestHasOutput(outputs, budget.Kind, budget.Target) {
			return ErrInvalidManifest
		}
		key := manifestChannelKey{kind: budget.Kind, name: budget.Target}
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidManifest
		}
		seen[key] = struct{}{}
		if budget.EstimatedTokens > budget.TokenLimit {
			return ErrBudgetExceeded
		}
	}
	return nil
}

func (b ManifestBudget) validateComponents() error {
	if err := b.Retention.validate(); err != nil {
		return err
	}
	if b.CompactionPolicy != nil {
		if err := b.CompactionPolicy.Validate(); err != nil {
			return err
		}
	}
	if err := b.validateDecision(); err != nil {
		return err
	}
	if err := b.Estimator.validate(); err != nil {
		return err
	}
	if b.ReportProfile != nil && b.Estimator.Descriptor != b.ReportProfile.Estimator {
		return ErrStaleEstimate
	}
	if b.Summarizer != nil {
		if err := b.Summarizer.Validate(); err != nil {
			return err
		}
	}
	if err := b.Truncation.validate(); err != nil {
		return err
	}
	if b.RollingSummary != nil {
		return b.RollingSummary.Validate()
	}
	return nil
}

func (b ManifestBudget) validateDecision() error {
	if b.Decision != nil {
		if err := b.Decision.validate(); err != nil {
			return err
		}
		if b.Decision.HardLimit > b.TokenLimit {
			return ErrInvalidBudgetRequest
		}
		trigger, target := b.Decision.HardLimit, b.Decision.HardLimit
		if b.CompactionPolicy != nil {
			trigger = budgetPercent(b.Decision.HardLimit, b.CompactionPolicy.TriggerPercent)
			target = budgetPercent(b.Decision.HardLimit, b.CompactionPolicy.TargetPercent)
		}
		if b.Decision.TriggerTokens != trigger || b.Decision.TargetTokens != target {
			return ErrInvalidBudgetRequest
		}
	}
	return nil
}

func validateManifestStages(stages map[string]Descriptor) error {
	for name, descriptor := range stages {
		if name == "" {
			return ErrInvalidDescriptor
		}
		if err := descriptor.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateManifestRefs(refs []ContentRef) error {
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateManifestSegments(segments []ManifestSegment) error {
	seen := make(map[string]struct{})
	for _, segment := range segments {
		if segment.Name == "" {
			return ErrInvalidManifest
		}
		if _, duplicate := seen[segment.Name]; duplicate {
			return ErrInvalidManifest
		}
		seen[segment.Name] = struct{}{}
		validateRefs := validateManifestSegmentRefs
		if segment.Name == manifestArtifactsSegment {
			validateRefs = validateManifestArtifactInputs
		}
		if err := validateRefs(segment.Messages); err != nil {
			return err
		}
	}
	return nil
}

// Artifact inputs include successive revisions under one occurrence identity.
// Message segments remain unique by ID; artifact evidence is unique by full ref.
func validateManifestArtifactInputs(refs []ContentRef) error {
	seen := make(map[ContentRef]bool)
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return err
		}
		if ref.Occurrence != "" || seen[ref] {
			return ErrInvalidManifest
		}
		seen[ref] = true
	}
	return nil
}

func validateManifestSegmentRefs(refs []ContentRef) error {
	seen := make(map[string]struct{})
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[ref.ID]; duplicate {
			return ErrInvalidManifest
		}
		seen[ref.ID] = struct{}{}
	}
	return nil
}

func validateManifestOutputs(outputs []ManifestOutput, targets map[string]Descriptor) error {
	type outputKey struct {
		kind ManifestOutputKind
		name string
	}
	seen := make(map[outputKey]struct{})
	for _, output := range outputs {
		if output.Name == "" {
			return ErrInvalidManifest
		}
		key := outputKey{kind: output.Kind, name: output.Name}
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidManifest
		}
		seen[key] = struct{}{}
		if err := validateManifestOutput(output, targets); err != nil {
			return err
		}
	}
	if _, main := seen[outputKey{kind: ManifestMainOutput, name: string(ManifestMainOutput)}]; !main {
		return ErrInvalidManifest
	}
	for target := range targets {
		if _, present := seen[outputKey{kind: ManifestTargetOutput, name: target}]; !present {
			return ErrInvalidManifest
		}
	}
	return nil
}

func validateManifestOutput(output ManifestOutput, targets map[string]Descriptor) error {
	if output.Kind == ManifestTargetOutput {
		if _, present := targets[output.Name]; !present {
			return ErrInvalidDescriptor
		}
	} else if output.Kind != ManifestMainOutput || output.Name != string(ManifestMainOutput) {
		return ErrInvalidManifest
	}
	if err := validateManifestSegments(output.Segments); err != nil {
		return err
	}
	if err := output.Lineage.Validate(); err != nil {
		return err
	}
	for _, ref := range []*ContentRef{output.Text, output.Rendered} {
		if ref != nil {
			if err := ref.Validate(); err != nil {
				return err
			}
		}
	}
	if err := validateSelectionDecision(output.Selection); err != nil {
		return err
	}
	return validateManifestOutputLineage(output)
}

func validateManifestOutputLineage(output ManifestOutput) error {
	produced := make(map[ContentRef]struct{})
	for _, record := range output.Lineage.Records {
		for _, ref := range record.Outputs {
			produced[baseContentRef(ref)] = struct{}{}
		}
	}
	var required []ContentRef
	for _, segment := range output.Segments {
		required = append(required, segment.Messages...)
	}
	if output.Rendered != nil {
		required = append(required, *output.Rendered)
	}
	for _, ref := range required {
		if _, exists := produced[baseContentRef(ref)]; !exists {
			return fmt.Errorf("%w: output %s/%s", ErrMissingLineage, output.Name, ref.ID)
		}
	}
	return nil
}

func manifestHasOutput(outputs []ManifestOutput, kind ManifestOutputKind, name string) bool {
	return slices.ContainsFunc(
		outputs,
		func(output ManifestOutput) bool { return output.Kind == kind && output.Name == name },
	)
}
