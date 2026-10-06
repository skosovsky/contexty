package contexty

import (
	"context"
	"errors"
	"slices"
	"sort"
)

var (
	ErrUnavailableCandidate = errors.New("contexty: selection candidate missing or stale")
	ErrDuplicateSelection   = errors.New("contexty: duplicate selection candidate")
	ErrMandatorySelection   = errors.New("contexty: mandatory selection candidate omitted")
	ErrSelectionRound       = errors.New("contexty: selection must address a complete tool round")
	ErrInvalidSelection     = errors.New("contexty: invalid selection policy or plan")
)

// ContextCandidate is an owned atomic unit. Ref addresses its first message;
// Messages contains the complete round where applicable, never raw current turn.
type ContextCandidate struct {
	Artifact *ContextArtifact
	Members  []ContentRef
	Ref      ContentRef  `json:"ref"`
	Segment  SegmentName `json:"segment"`
	Messages []Message   `json:"-"`
	Required bool        `json:"required"`
	Ordinal  int         `json:"ordinal"`
}

// SelectionChoice selects an exact prepared unit. Higher priorities admit first;
// equal priorities use preparation ordinal. Output chronology never changes.
type SelectionChoice struct {
	Ref      ContentRef `json:"ref"`
	Priority int        `json:"priority"`
}

type SelectionPolicy struct {
	Required []ContentRef
	Identity Descriptor
	Select   func(context.Context, []ContextCandidate) ([]SelectionChoice, error)
}

func (p SelectionPolicy) validate() error {
	if p.Select == nil {
		return ErrInvalidSelection
	}
	return p.Identity.Validate()
}

type CandidateDecision struct {
	RoundMembers []ContentRef `json:"round_members,omitempty"`
	Members      []ContentRef `json:"members"`
	Ref          ContentRef   `json:"ref"`
	Segment      SegmentName  `json:"segment"`
	Ordinal      int          `json:"ordinal"`
	Required     bool         `json:"required"`
	Selected     bool         `json:"selected"`
	Reason       string       `json:"reason"`
}

// SelectionDecision is local evidence, not an export allowlist.
type SelectionDecision struct {
	Policy     *Descriptor         `json:"policy,omitempty"`
	Plan       []SelectionChoice   `json:"plan"`
	Candidates []CandidateDecision `json:"candidates"`
}

func (d *SelectionDecision) clone() *SelectionDecision {
	if d == nil {
		return nil
	}
	cp := *d
	cp.Plan = slices.Clone(d.Plan)
	cp.Candidates = slices.Clone(d.Candidates)
	for i := range cp.Candidates {
		cp.Candidates[i].Members = slices.Clone(cp.Candidates[i].Members)
		cp.Candidates[i].RoundMembers = slices.Clone(cp.Candidates[i].RoundMembers)
	}
	if d.Policy != nil {
		identity := *d.Policy
		cp.Policy = &identity
	}
	return &cp
}

// WithSelectionPolicy configures main output selection. Retrieval remains host-owned.
func WithSelectionPolicy(policy SelectionPolicy) EngineOption {
	return func(e *Engine) { cp := policy; cp.Required = slices.Clone(policy.Required); e.selection = &cp }
}

func selectionCodec(ctx context.Context) JSONSerializer {
	if trace := traceFromContext(ctx); trace != nil {
		return trace.profile.Codec
	}
	return DefaultJSONSerializer()
}

func selectionCandidates(
	ctx context.Context,
	snap ConversationSnapshot,
	pending []Message,
	pipe *BudgetPipeline,
) ([]ContextCandidate, error) {
	var candidates []ContextCandidate
	for _, segment := range snapshotSegmentOrder() {
		messages := snap.Segment(segment)
		if segment == SegmentHistory {
			messages = append(messages, cloneMessageSlice(pending)...)
		}
		units, err := segmentCandidates(ctx, segment, messages, len(pending), pipe)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, units...)
	}
	for i := range candidates {
		candidates[i].Ordinal = i
		if err := attachCandidateArtifact(&candidates[i], snap.Artifacts()); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

func segmentCandidates(
	ctx context.Context,
	segment SegmentName,
	messages []Message,
	pendingCount int,
	pipe *BudgetPipeline,
) ([]ContextCandidate, error) {
	rounds, required, err := segmentRequired(ctx, segment, messages, pendingCount, pipe)
	if err != nil {
		return nil, err
	}
	ends := make(map[int]int)
	for _, round := range rounds {
		ends[round.Start] = round.End
	}
	var candidates []ContextCandidate
	for i := 0; i < len(messages); i++ {
		end := i
		if roundEnd, ok := ends[i]; ok {
			end = roundEnd
		}
		members, refErr := messageContentRefs(ctx, messages[i:end+1])
		if refErr != nil {
			return nil, refErr
		}
		candidates = append(
			candidates,
			ContextCandidate{
				Ref:      members[0],
				Segment:  segment,
				Messages: cloneMessageSlice(messages[i : end+1]),
				Required: required[i],
				Ordinal:  0,
				Artifact: nil,
				Members:  members,
			},
		)
		i = end
	}
	return candidates, nil
}

func segmentRequired(
	ctx context.Context,
	segment SegmentName,
	messages []Message,
	pendingCount int,
	pipe *BudgetPipeline,
) ([]ToolRoundObservation, []bool, error) {
	required := make([]bool, len(messages))
	if segment != SegmentHistory && !hasRoundParts(messages) {
		return nil, required, nil
	}
	rounds, err := segmentToolRounds(segment, messages)
	if err != nil {
		return nil, nil, err
	}
	if segment != SegmentHistory {
		protectBudgetSuffix(required, rounds, nil)
		return rounds, required, nil
	}
	if pipe != nil {
		required, err = pipe.selectRequired(ctx, messages, rounds)
		if err != nil {
			return nil, nil, err
		}
		protectBudgetSuffix(required, rounds, pipe.rolling)
	} else {
		protectBudgetSuffix(required, rounds, nil)
	}
	for i := len(messages) - pendingCount; i < len(messages); i++ {
		required[i] = true
	}
	expandRequiredRounds(required, rounds)
	return rounds, required, nil
}

func messageContentRefs(ctx context.Context, messages []Message) ([]ContentRef, error) {
	var refs []ContentRef
	for _, message := range messages {
		ref, err := MessageContentRef(message, selectionCodec(ctx))
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func attachCandidateArtifact(candidate *ContextCandidate, artifacts []ContextArtifact) error {
	for _, artifact := range artifacts {
		if candidate.Messages[0].ID != "artifact:"+artifact.ID {
			continue
		}
		cp := artifact.Clone()
		candidate.Artifact = &cp
		ref, err := ArtifactContentRef(cp)
		if err != nil {
			return err
		}
		candidate.Ref = ref
		break
	}
	return nil
}

func cloneCandidates(candidates []ContextCandidate) []ContextCandidate {
	out := slices.Clone(candidates)
	for i := range out {
		out[i].Messages = cloneMessageSlice(out[i].Messages)
		out[i].Members = slices.Clone(out[i].Members)
		if out[i].Artifact != nil {
			artifact := out[i].Artifact.Clone()
			out[i].Artifact = &artifact
		}
	}
	return out
}

func selectionPlan(
	ctx context.Context,
	candidates []ContextCandidate,
	policy *SelectionPolicy,
) ([]SelectionChoice, error) {
	if policy == nil {
		plan := make([]SelectionChoice, 0, len(candidates))
		for _, candidate := range candidates {
			plan = append(plan, SelectionChoice{Ref: candidate.Ref, Priority: 0})
		}
		return plan, nil
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := policy.Select(ctx, cloneCandidates(candidates))
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	return slices.Clone(plan), err
}

func validateSelectionPlan(
	ctx context.Context,
	candidates []ContextCandidate,
	plan []SelectionChoice,
) (map[ContentRef]int, error) {
	allowed := make(map[ContentRef]bool)
	members := make(map[ContentRef]bool)
	for _, candidate := range candidates {
		allowed[candidate.Ref] = true
		for _, message := range candidate.Messages[1:] {
			ref, err := MessageContentRef(message, selectionCodec(ctx))
			if err != nil {
				return nil, err
			}
			members[ref] = true
		}
	}
	priorities := make(map[ContentRef]int)
	for _, choice := range plan {
		if members[choice.Ref] {
			return nil, ErrSelectionRound
		}
		if !allowed[choice.Ref] {
			return nil, ErrUnavailableCandidate
		}
		if _, dup := priorities[choice.Ref]; dup {
			return nil, ErrDuplicateSelection
		}
		priorities[choice.Ref] = choice.Priority
	}
	for _, candidate := range candidates {
		if _, found := priorities[candidate.Ref]; candidate.Required && !found {
			return nil, ErrMandatorySelection
		}
	}
	return priorities, nil
}

func selectOutput(
	ctx context.Context,
	snap ConversationSnapshot,
	pending []Message,
	policy *SelectionPolicy,
	pipe *BudgetPipeline,
) (ConversationSnapshot, *SelectionDecision, error) {
	candidates, err := selectionCandidates(ctx, snap, pending, pipe)
	if err != nil {
		return ConversationSnapshot{}, nil, err
	}
	if requiredErr := markSelectionRequired(candidates, policy); requiredErr != nil {
		return ConversationSnapshot{}, nil, requiredErr
	}
	plan, err := selectionPlan(ctx, candidates, policy)
	if err != nil {
		return ConversationSnapshot{}, nil, err
	}
	priorities, err := validateSelectionPlan(ctx, candidates, plan)
	if err != nil {
		return ConversationSnapshot{}, nil, err
	}
	admitted, err := admitSelection(ctx, candidates, priorities, policy != nil, pipe)
	if err != nil {
		return ConversationSnapshot{}, nil, err
	}
	decision := &SelectionDecision{Policy: nil, Plan: plan, Candidates: nil}
	if policy != nil {
		identity := policy.Identity
		decision.Policy = &identity
	}
	out := EmptySnapshot().WithVersion(snap.Version()).WithArtifacts(snap.Artifacts())
	pendingIDs := messageIDSet(pending)
	for _, candidate := range candidates {
		reason := coverageNotSelected
		if admitted[candidate.Ref] {
			reason = "selected"
		} else if _, planned := priorities[candidate.Ref]; planned {
			reason = ReasonTokenBudgetExceeded
		}
		roundMembers, roundErr := candidateRoundRefs(ctx, candidate)
		if roundErr != nil {
			return ConversationSnapshot{}, nil, roundErr
		}
		decision.Candidates = append(
			decision.Candidates,
			CandidateDecision{
				Members:      slices.Clone(candidate.Members),
				RoundMembers: roundMembers,
				Ref:          candidate.Ref,
				Segment:      candidate.Segment,
				Ordinal:      candidate.Ordinal,
				Required:     candidate.Required,
				Selected:     admitted[candidate.Ref],
				Reason:       reason,
			},
		)
		if !admitted[candidate.Ref] {
			continue
		}
		for _, message := range candidate.Messages {
			if _, isPending := pendingIDs[message.ID]; !isPending {
				out = out.WithSegment(candidate.Segment, append(out.Segment(candidate.Segment), message))
			}
		}
	}
	return out, decision, nil
}

func admitSelection(
	ctx context.Context,
	candidates []ContextCandidate,
	priorities map[ContentRef]int,
	pack bool,
	pipe *BudgetPipeline,
) (map[ContentRef]bool, error) {
	admitted := make(map[ContentRef]bool)
	ordered := slices.Clone(candidates)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Required != ordered[j].Required {
			return ordered[i].Required
		}
		return priorities[ordered[i].Ref] > priorities[ordered[j].Ref]
	})
	for _, candidate := range ordered {
		if _, selected := priorities[candidate.Ref]; !selected {
			continue
		}
		eligible, err := candidateLocalAdmission(ctx, candidate, pipe)
		if err != nil {
			return nil, err
		}
		if !eligible {
			continue
		}
		admitted[candidate.Ref] = true
		fits, fitErr := selectionFits(ctx, candidates, admitted, pack, pipe)
		if fitErr != nil {
			return nil, fitErr
		}
		if !fits {
			if candidate.Required {
				return nil, ErrBudgetExceeded
			}
			delete(admitted, candidate.Ref)
		}
	}
	return admitted, nil
}

func candidateLocalAdmission(ctx context.Context, candidate ContextCandidate, pipe *BudgetPipeline) (bool, error) {
	if candidate.Artifact == nil {
		return true, nil
	}
	estimator := TokenEstimator(CharTokenEstimator{})
	if pipe != nil {
		estimator = pipe.estimator
	}
	reason, err := artifactSelectionReason(ctx, estimator, candidate.Artifact.BoundTurnID, *candidate.Artifact)
	if err != nil {
		return false, err
	}
	if reason != "" && candidate.Required {
		return false, ErrBudgetExceeded
	}
	return reason == "", nil
}

func selectionFits(
	ctx context.Context,
	candidates []ContextCandidate,
	admitted map[ContentRef]bool,
	pack bool,
	pipe *BudgetPipeline,
) (bool, error) {
	if !pack || pipe == nil {
		return true, nil
	}
	tokens, err := estimateSelection(ctx, candidates, admitted, pipe)
	if canceled := ctx.Err(); canceled != nil {
		return false, canceled
	}
	if err != nil {
		return false, err
	}
	if tokens < 0 {
		return false, ErrInconsistentEstimate
	}
	limit, err := pipe.cfg.Budget.Resolve()
	if err != nil {
		return false, err
	}
	return tokens <= limit, nil
}

func estimateSelection(
	ctx context.Context,
	candidates []ContextCandidate,
	admitted map[ContentRef]bool,
	pipe *BudgetPipeline,
) (int, error) {
	reporter, ok := pipe.estimator.(*EstimateReporter)
	if !ok {
		return estimateOwned(ctx, pipe.estimator, selectionMessages(candidates, admitted))
	}
	segments := []EstimateSegment{{Name: manifestMessagesSegment, Messages: selectionMessages(candidates, admitted)}}
	if finalBudgetChannel(ctx).kind == ManifestMainOutput {
		snap := EmptySnapshot()
		for _, candidate := range candidates {
			if admitted[candidate.Ref] {
				snap = snap.WithSegment(
					candidate.Segment,
					append(snap.Segment(candidate.Segment), candidate.Messages...),
				)
			}
		}
		segments = payloadEstimateSegments(
			AbstractPayload{
				System:  snap.Segment(SegmentSystem),
				History: snap.Segment(SegmentHistory),
				Memory:  snap.Segment(SegmentMemory),
				Tools:   snap.Segment(SegmentTools),
			},
		)
	}
	report, err := reporter.Report(
		ctx,
		EstimateRequest{Segments: segments, Budget: pipe.cfg.Budget, ManifestRef: nil, WireRef: nil},
	)
	return report.Total, err
}

func selectionMessages(candidates []ContextCandidate, admitted map[ContentRef]bool) []Message {
	var messages []Message
	for _, candidate := range candidates {
		if admitted[candidate.Ref] {
			messages = append(messages, cloneMessageSlice(candidate.Messages)...)
		}
	}
	return messages
}

// OutputConfiguration pins composition and policy identity without executable callbacks.
type OutputConfiguration struct {
	Kind               ManifestOutputKind `json:"kind"`
	Name               string             `json:"name"`
	Segments           []SegmentName      `json:"segments"`
	ArtifactRefs       []ContentRef       `json:"artifact_refs"`
	IncludeArtifacts   bool               `json:"include_artifacts"`
	IncludeCurrentTurn bool               `json:"include_current_turn"`
	View               string             `json:"view"`
	Selection          *Descriptor        `json:"selection,omitempty"`
	Required           []ContentRef       `json:"required"`
}

type outputConfigurationKey struct{}

func (e *Engine) outputConfigurations(request CompileRequest) []OutputConfiguration {
	main := OutputConfiguration{
		Kind:               ManifestMainOutput,
		Name:               string(ManifestMainOutput),
		Segments:           snapshotSegmentOrder(),
		ArtifactRefs:       nil,
		IncludeArtifacts:   true,
		IncludeCurrentTurn: true,
		Required:           nil,
		View:               "",
		Selection:          nil,
	}
	if e.selection != nil {
		identity := e.selection.Identity
		main.Selection = &identity
		main.Required = slices.Clone(e.selection.Required)
	}
	out := []OutputConfiguration{main}
	for _, target := range request.Targets {
		config := OutputConfiguration{
			Kind:               ManifestTargetOutput,
			Name:               target.Name,
			Segments:           slices.Clone(target.Segments),
			ArtifactRefs:       slices.Clone(target.ArtifactRefs),
			IncludeArtifacts:   target.IncludeArtifacts,
			IncludeCurrentTurn: target.IncludeCurrentTurn,
			Required:           nil,
			View:               target.View,
			Selection:          nil,
		}
		if target.Selection != nil {
			identity := target.Selection.Identity
			config.Selection = &identity
			config.Required = slices.Clone(target.Selection.Required)
		}
		out = append(out, config)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func cloneOutputConfigurations(in []OutputConfiguration) []OutputConfiguration {
	out := slices.Clone(in)
	for i := range out {
		out[i].Required = slices.Clone(out[i].Required)
		out[i].Segments = slices.Clone(out[i].Segments)
		out[i].ArtifactRefs = slices.Clone(out[i].ArtifactRefs)
		if out[i].Selection != nil {
			identity := *out[i].Selection
			out[i].Selection = &identity
		}
	}
	return out
}

func validateSelectionDecision(decision *SelectionDecision) error {
	if decision == nil {
		return nil
	}
	if decision.Policy != nil {
		if err := decision.Policy.Validate(); err != nil {
			return err
		}
	}
	candidates := make(map[ContentRef]CandidateDecision)
	for i, candidate := range decision.Candidates {
		if err := validateCandidateDecision(candidate, i); err != nil {
			return err
		}
		if _, dup := candidates[candidate.Ref]; dup {
			return ErrDuplicateSelection
		}
		candidates[candidate.Ref] = candidate
	}
	planned := make(map[ContentRef]bool)
	for _, choice := range decision.Plan {
		if _, found := candidates[choice.Ref]; !found {
			return ErrUnavailableCandidate
		}
		if planned[choice.Ref] {
			return ErrDuplicateSelection
		}
		planned[choice.Ref] = true
	}
	for _, candidate := range decision.Candidates {
		if candidate.Selected && !planned[candidate.Ref] {
			return ErrInvalidSelection
		}
	}
	return nil
}

func validateCandidateDecision(candidate CandidateDecision, ordinal int) error {
	if candidate.Ordinal != ordinal || !isKnownSegment(candidate.Segment) || candidate.Ref.Validate() != nil ||
		len(candidate.Members) == 0 {
		return ErrInvalidSelection
	}
	if err := validateManifestRefs(candidate.Members); err != nil {
		return err
	}
	if len(candidate.RoundMembers) > 0 {
		if len(candidate.RoundMembers) != len(candidate.Members) {
			return ErrInvalidSelection
		}
		if err := validateManifestRefs(candidate.RoundMembers); err != nil {
			return err
		}
		for i, ref := range candidate.RoundMembers {
			if ref.ID != candidate.Members[i].ID {
				return ErrInvalidSelection
			}
		}
	}
	if candidate.Required && !candidate.Selected {
		return ErrMandatorySelection
	}
	if candidate.Selected {
		if candidate.Reason != "selected" {
			return ErrInvalidSelection
		}
		return nil
	}
	if candidate.Reason != coverageNotSelected && candidate.Reason != ReasonTokenBudgetExceeded {
		return ErrInvalidSelection
	}
	return nil
}

func filterParticipatingArtifacts(artifacts []ContextArtifact, messages []Message) []ContextArtifact {
	ids := participatingArtifactIDs(artifacts, messages)
	var out []ContextArtifact
	for _, artifact := range artifacts {
		if slices.Contains(ids, artifact.ID) {
			out = append(out, artifact.Clone())
		}
	}
	return out
}

func recordSelectionArtifactExclusions(
	ctx context.Context,
	decision *SelectionDecision,
	artifacts []ContextArtifact,
) error {
	decisions, _ := ctx.Value(artifactExclusionsKey{}).(map[ContentRef]string)
	for _, candidate := range decision.Candidates {
		if candidate.Selected {
			continue
		}
		for _, artifact := range artifacts {
			if candidate.Ref.ID == artifact.ID {
				ref, err := ArtifactContentRef(artifact)
				if err != nil {
					return err
				}
				decisions[ref] = candidate.Reason
			}
		}
	}
	return nil
}

func validateOutputConfigurations(configs []OutputConfiguration, profile RecordProfile) error {
	if len(configs) != len(profile.Targets)+1 {
		return ErrInvalidSelection
	}
	seen := make(map[manifestChannelKey]bool)
	prior := ""
	for _, config := range configs {
		key := manifestChannelKey{kind: config.Kind, name: config.Name}
		order := string(config.Kind) + "/" + config.Name
		if config.Name == "" || seen[key] || (prior != "" && prior >= order) {
			return ErrInvalidSelection
		}
		if err := validateOutputConfiguration(config, profile); err != nil {
			return err
		}
		seen[key] = true
		prior = order
	}
	if !seen[manifestChannelKey{kind: ManifestMainOutput, name: string(ManifestMainOutput)}] {
		return ErrInvalidSelection
	}
	return nil
}

func validateOutputConfiguration(config OutputConfiguration, profile RecordProfile) error {
	switch config.Kind {
	case ManifestMainOutput:
		if config.Name != string(ManifestMainOutput) {
			return ErrInvalidSelection
		}
	case ManifestTargetOutput:
		if _, found := profile.Targets[config.Name]; !found {
			return ErrInvalidSelection
		}
	default:
		return ErrInvalidSelection
	}
	if config.Selection != nil {
		if err := config.Selection.Validate(); err != nil {
			return err
		}
	}
	if config.Selection == nil && len(config.Required) > 0 {
		return ErrInvalidSelection
	}
	if err := validateManifestRefs(config.Required); err != nil {
		return err
	}
	segments := make(map[SegmentName]bool)
	for _, segment := range config.Segments {
		if !isKnownSegment(segment) || segments[segment] {
			return ErrInvalidSelection
		}
		segments[segment] = true
	}
	if config.IncludeArtifacts && len(config.ArtifactRefs) != 0 {
		return ErrInvalidSelection
	}
	return validateManifestRefs(config.ArtifactRefs)
}

func markSelectionRequired(candidates []ContextCandidate, policy *SelectionPolicy) error {
	if policy == nil {
		return nil
	}
	seen := make(map[ContentRef]bool)
	for _, ref := range policy.Required {
		if seen[ref] {
			return ErrDuplicateSelection
		}
		seen[ref] = true
		found := false
		for i := range candidates {
			if candidates[i].Ref == ref {
				candidates[i].Required = true
				found = true
				break
			}
		}
		if !found {
			return ErrUnavailableCandidate
		}
	}
	return nil
}

func hasRoundParts(messages []Message) bool {
	for _, message := range messages {
		if len(message.ToolCallParts()) != 0 || len(message.ToolResultParts()) != 0 {
			return true
		}
	}
	return false
}

func validateMandatorySelection(
	ctx context.Context,
	decision *SelectionDecision,
	policy *SelectionPolicy,
	after []Message,
) error {
	if decision == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	indexes := make(map[string]int)
	for i, message := range after {
		indexes[message.ID] = i
	}
	exact := make(map[ContentRef]bool)
	if policy != nil {
		for _, ref := range policy.Required {
			exact[ref] = true
		}
	}
	priorBySegment := make(map[SegmentName]int)
	for _, candidate := range decision.Candidates {
		if len(candidate.RoundMembers) > 0 && candidate.Selected {
			if err := validateAdmittedRound(ctx, candidate, indexes, after); err != nil {
				return err
			}
		}
		if !candidate.Required {
			continue
		}
		first, last, err := validateMandatoryUnit(ctx, candidate, indexes, after, exact[candidate.Ref])
		if err != nil {
			return err
		}
		if prior, found := priorBySegment[candidate.Segment]; found && first <= prior {
			return ErrMandatorySelection
		}
		priorBySegment[candidate.Segment] = last
	}
	return nil
}

func validateMandatoryUnit(
	ctx context.Context,
	candidate CandidateDecision,
	indexes map[string]int,
	after []Message,
	exact bool,
) (int, int, error) {
	first, prior := -1, -1
	for _, ref := range candidate.Members {
		index, found := indexes[ref.ID]
		if !found || (prior >= 0 && index != prior+1) {
			return 0, 0, ErrMandatorySelection
		}
		if exact {
			actual, err := MessageContentRef(after[index], selectionCodec(ctx))
			if err != nil {
				return 0, 0, err
			}
			if actual != ref {
				return 0, 0, ErrMandatorySelection
			}
		}
		if first < 0 {
			first = index
		}
		prior = index
	}
	return first, prior, nil
}

// finalParticipatingArtifacts follows materialized content into the final output.
// Without lineage only a surviving materialized identity proves participation.
func finalParticipatingArtifacts(
	ctx context.Context,
	artifacts []ContextArtifact,
	messages []Message,
) ([]ContextArtifact, error) {
	segment, err := manifestSegment(manifestMessagesSegment, messages, selectionCodec(ctx))
	if err != nil {
		return nil, err
	}
	output := ManifestOutput{
		OpaqueState: nil, OutputPolicy: nil, Kind: "",
		Name:              "",
		Segments:          []ManifestSegment{segment},
		Lineage:           traceGraph(ctx),
		Text:              nil,
		Rendered:          nil,
		Transformations:   nil,
		SourceSegments:    nil,
		Selection:         nil,
		ArtifactRefs:      nil,
		ArtifactEstimates: nil,
		ArtifactBudgets:   nil,
		ExcludedArtifacts: nil,
		View:              "",
	}

	reach := coverageAncestry(output, nil)
	direct := participatingArtifactIDs(artifacts, messages)
	var selected []ContextArtifact
	for _, artifact := range artifacts {
		ref, refErr := ArtifactContentRef(artifact)
		if refErr != nil {
			return nil, refErr
		}
		present := slices.Contains(direct, artifact.ID)
		for _, anchor := range coverageAnchors(output.Lineage, nil, manifestArtifactsSegment, ref) {
			present = present || len(reach[anchor]) > 0
		}
		if present {
			selected = append(selected, artifact.Clone())
		}
	}
	return selected, nil
}

func snapshotWithFinalMessages(snap ConversationSnapshot, messages []Message) ConversationSnapshot {
	byID := make(map[string]Message)
	for _, message := range messages {
		byID[message.ID] = message
	}
	for _, segment := range snapshotSegmentOrder() {
		var projected []Message
		for _, message := range snap.Segment(segment) {
			if final, found := byID[message.ID]; found {
				projected = append(projected, final.Clone())
			}
		}
		snap = snap.WithSegment(segment, projected)
	}
	return snap
}

func segmentToolRounds(segment SegmentName, messages []Message) ([]ToolRoundObservation, error) {
	if segment == SegmentHistory {
		return InspectToolRoundStates(messages, nil)
	}
	var rounds []ToolRoundObservation
	for i := 0; i < len(messages); i++ {
		if len(messages[i].ToolResultParts()) > 0 {
			return nil, ErrInvalidToolRound
		}
		if !messages[i].HasToolCalls() {
			continue
		}
		end := contiguousToolBlockEnd(messages, i)
		unit, err := InspectToolRoundStates(messages[i:end+1], nil)
		if err != nil {
			return nil, err
		}
		for _, round := range unit {
			round.Start += i
			round.End += i
			rounds = append(rounds, round)
		}
		i = end
	}
	return rounds, nil
}

func validateFinalSelectionRounds(snap ConversationSnapshot) error {
	for _, segment := range snapshotSegmentOrder() {
		messages := snap.Segment(segment)
		if !hasRoundParts(messages) {
			continue
		}
		if _, err := segmentToolRounds(segment, messages); err != nil {
			return err
		}
	}
	return nil
}

func candidateRoundRefs(ctx context.Context, candidate ContextCandidate) ([]ContentRef, error) {
	if !hasRoundParts(candidate.Messages) {
		return nil, nil
	}
	var refs []ContentRef
	for _, message := range candidate.Messages {
		ref, err := roundSemanticRef(ctx, message)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func roundSemanticRef(ctx context.Context, message Message) (ContentRef, error) {
	var projection Message
	projection.ID = message.ID
	projection.Role = message.Role
	for _, part := range message.Parts {
		switch canonicalPartValue(part).(type) {
		case ToolCallPart, ToolResultPart:
			projection.Parts = append(projection.Parts, part)
		}
	}
	return MessageContentRef(projection, selectionCodec(ctx))
}

func validateAdmittedRound(
	ctx context.Context,
	candidate CandidateDecision,
	indexes map[string]int,
	after []Message,
) error {
	present, prior := 0, -1
	for _, ref := range candidate.RoundMembers {
		index, found := indexes[ref.ID]
		if !found {
			continue
		}
		actual, err := roundSemanticRef(ctx, after[index])
		if err != nil {
			return err
		}
		if actual != ref || (prior >= 0 && index != prior+1) {
			return errors.Join(ErrSelectionRound, ErrInvalidToolRound)
		}
		present++
		prior = index
	}
	if present != 0 && present != len(candidate.RoundMembers) {
		return errors.Join(ErrSelectionRound, ErrInvalidToolRound)
	}
	return nil
}
