package contexty

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// CompileTarget requests an additional named projection from the same compile pass.
type CompileTarget struct {
	Name string
	// View requests a built-in rendered view and is mutually exclusive with
	// Segments, ArtifactRefs, IncludeCurrentTurn, Selection, Budget, and Formatter.
	View               string
	Segments           []SegmentName
	ArtifactRefs       []ContentRef
	IncludeCurrentTurn bool
	IncludeArtifacts   bool
	Selection          *SelectionPolicy
	Budget             *BudgetPipeline
	Formatter          SegmentFormatter
}

// CompileProjection is a named compile output with traceability to the shared pass.
type CompileProjection struct {
	Name              string
	Text              string
	Messages          []Message
	Transformations   map[string]TransformChain
	Source            CompileRequest       // normalized request before pipeline mutations
	InputSnapshot     ConversationSnapshot // shared prepared snapshot before output admission
	ArtifactIDs       []string
	Lineage           Lineage
	Rendered          *RenderedOutput
	Snapshot          ConversationSnapshot
	Selection         *SelectionDecision
	Artifacts         []ContextArtifact
	ArtifactEstimates []ArtifactBudgetEstimate
	ExcludedArtifacts []ArtifactExclusion
}

// RenderedOutput is the typed content/metadata counterpart of a text view.
// Its role is a representation detail, not a trust or permission decision.
type RenderedOutput struct {
	Message  Message
	Ref      ContentRef
	Renderer Descriptor
}

func (r *RenderedOutput) clone() *RenderedOutput {
	if r == nil {
		return nil
	}
	return &RenderedOutput{Message: r.Message.Clone(), Ref: r.Ref, Renderer: r.Renderer}
}

func (p CompileProjection) clone() CompileProjection {
	return CompileProjection{
		Name:              p.Name,
		Text:              p.Text,
		Messages:          cloneMessageSlice(p.Messages),
		Transformations:   cloneTransformRecords(p.Transformations),
		Source:            p.Source.Freeze(),
		InputSnapshot:     p.InputSnapshot.AllSegmentsSnapshot(),
		ArtifactIDs:       append([]string(nil), p.ArtifactIDs...),
		Lineage:           p.Lineage.Clone(),
		Rendered:          p.Rendered.clone(),
		Snapshot:          p.Snapshot.AllSegmentsSnapshot(),
		Selection:         p.Selection.clone(),
		Artifacts:         cloneArtifacts(p.Artifacts),
		ArtifactEstimates: cloneValidArtifactEstimates(p.ArtifactEstimates),
		ExcludedArtifacts: append([]ArtifactExclusion(nil), p.ExcludedArtifacts...),
	}
}

func cloneCompileProjections(in map[string]CompileProjection) map[string]CompileProjection {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]CompileProjection, len(in))
	for k, v := range in {
		out[k] = v.clone()
	}
	return out
}

func cloneTransformRecords(in map[string]TransformChain) map[string]TransformChain {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]TransformChain, len(in))
	for id, chain := range in {
		out[id] = append(TransformChain(nil), chain...)
	}
	return out
}

func validateCompileTargets(targets []CompileTarget) error {
	if len(targets) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		name := strings.TrimSpace(target.Name)
		if name == "" {
			return errors.New("contexty: compile target name is empty")
		}
		if _, ok := seen[name]; ok {
			return ErrDuplicateCompileTarget
		}
		if err := validateCompileTarget(target); err != nil {
			return err
		}

		seen[name] = struct{}{}
	}
	return nil
}

func validateCompileTarget(target CompileTarget) error {
	if target.View != "" {
		if _, ok := builtinViewFormatter(target.View); !ok {
			return ErrUnknownCompileTargetView
		}
		if len(target.Segments) != 0 || len(target.ArtifactRefs) != 0 || target.IncludeCurrentTurn ||
			target.IncludeArtifacts ||
			target.Selection != nil ||
			target.Budget != nil ||
			target.Formatter != nil {
			return ErrConflictingCompileTargetFields
		}
	}
	seen := make(map[SegmentName]bool)
	for _, segment := range target.Segments {
		if !isKnownSegment(segment) || seen[segment] {
			return ErrInvalidCompileTargetSegment
		}
		seen[segment] = true
	}
	if target.Selection != nil {
		return target.Selection.validate()
	}
	return nil
}

func isKnownSegment(seg SegmentName) bool {
	switch seg {
	case SegmentSystem, SegmentHistory, SegmentTools, SegmentMemory:
		return true
	default:
		return false
	}
}

func (e *Engine) compileTargets(
	ctx context.Context,
	snap ConversationSnapshot,
	targets []CompileTarget,
	source CompileRequest,
	transforms map[string]TransformChain,
	artifacts []ContextArtifact,
) (map[string]CompileProjection, error) {
	if len(targets) == 0 {
		return map[string]CompileProjection{}, nil
	}
	out := make(map[string]CompileProjection, len(targets))
	for _, target := range targets {
		proj, err := e.compileTarget(ctx, snap, target, source, transforms, artifacts)
		if err != nil {
			return nil, err
		}
		proj.Source = source.Freeze()
		out[proj.Name] = proj
	}
	return out, nil
}

func (e *Engine) compileTarget(
	ctx context.Context,
	snap ConversationSnapshot,
	target CompileTarget,
	source CompileRequest,
	transforms map[string]TransformChain,
	artifacts []ContextArtifact,
) (CompileProjection, error) {
	if err := ctx.Err(); err != nil {
		return CompileProjection{}, fmt.Errorf("contexty: compile target %q: %w", target.Name, err)
	}
	name := strings.TrimSpace(target.Name)
	ctx = preparedTargetContext(ctx, source, snap, name, transforms)
	if target.View != "" {
		return e.compileTextViewTarget(ctx, snap, target)
	}
	input := snap.AllSegmentsSnapshot()
	scoped, selectedArtifacts, err := scopeTargetSnapshot(snap, target, artifacts)
	if err != nil {
		return CompileProjection{}, err
	}
	if exclusionErr := initializeTargetArtifactExclusions(ctx, artifacts); exclusionErr != nil {
		return CompileProjection{}, exclusionErr
	}
	prepared, _ := ctx.Value(preparedOutputKey{}).(preparedOutput)
	var pending []Message
	if target.IncludeCurrentTurn {
		pending = cloneMessageSlice(prepared.pending)
	}
	recorder := transformRecorderFrom(ctx)
	scoped, selection, err := selectOutput(ctx, scoped, pending, target.Selection, target.Budget)
	if err != nil {
		return CompileProjection{}, err
	}
	if selectionErr := recordSelectionArtifactExclusions(ctx, selection, selectedArtifacts); selectionErr != nil {
		return CompileProjection{}, selectionErr
	}
	selectedArtifacts = filterParticipatingArtifacts(selectedArtifacts, snapshotAllMessages(scoped))
	scoped = scoped.WithArtifacts(selectedArtifacts)
	localOptions := outputOptions(
		prepared.options,
		scoped.WithSegment(SegmentHistory, append(scoped.Segment(SegmentHistory), pending...)),
	)
	scoped, err = applyCompileReplacements(ctx, scoped, localOptions, patchPhasePreBudget)
	if err != nil {
		return CompileProjection{}, err
	}
	scoped, err = e.applyTargetStages(ctx, source, target, scoped, pending, localOptions)
	if err != nil {
		return CompileProjection{}, err
	}
	working := snapshotAllMessages(scoped)
	localTransforms := recorder.snapshot()
	estimates, err := compileArtifactEstimates(ctx, artifacts)
	if err != nil {
		return CompileProjection{}, err
	}
	working, err = finishTargetMessages(ctx, target, working, localTransforms)
	if err != nil {
		return CompileProjection{}, err
	}

	scoped, working, localTransforms, err = e.acceptTargetSemantic(
		ctx,
		scoped,
		target,
		selection,
		working,
		localTransforms,
	)
	if err != nil {
		return CompileProjection{}, err
	}
	scoped, selectedArtifacts, exclusions, err := finishTargetSnapshot(
		ctx,
		scoped,
		target,
		selectedArtifacts,
		artifacts,
		working,
	)
	if err != nil {
		return CompileProjection{}, err
	}
	return CompileProjection{
		Name:              name,
		Text:              plainMessagesText(working),
		Messages:          cloneMessageSlice(working),
		Transformations:   localTransforms,
		Source:            emptyCompileRequest(),
		InputSnapshot:     input,
		ArtifactIDs:       artifactIDs(selectedArtifacts),
		Selection:         selection,
		Artifacts:         cloneArtifacts(selectedArtifacts),
		ArtifactEstimates: estimates,
		ExcludedArtifacts: exclusions,
		Snapshot:          scoped,
		Lineage:           traceGraph(ctx),
		Rendered:          nil,
	}, nil
}

func (e *Engine) applyTargetStages(
	ctx context.Context,
	source CompileRequest,
	target CompileTarget,
	scoped ConversationSnapshot,
	pending []Message,
	options compileOptions,
) (ConversationSnapshot, error) {
	localEngine := *e
	localEngine.budget = target.Budget
	return localEngine.applyTransformsAndBudget(ctx, source, scoped, pending, transformRecorderFrom(ctx), options)
}

func (e *Engine) acceptTargetSemantic(
	ctx context.Context,
	scoped ConversationSnapshot,
	target CompileTarget,
	selection *SelectionDecision,
	working []Message,
	localTransforms map[string]TransformChain,
) (ConversationSnapshot, []Message, map[string]TransformChain, error) {
	recorder := transformRecorderFrom(ctx)
	selectedArtifacts := scoped.Artifacts()
	semantic := snapshotWithFinalMessages(scoped, working)
	if target.Formatter != nil {
		semantic = EmptySnapshot().WithSegment(SegmentHistory, working)
	}
	recorder.records = cloneTransformRecords(localTransforms)
	if recorder.records == nil {
		recorder.records = make(map[string]TransformChain)
	}
	accepted, policyErr := e.acceptSemanticOutput(
		ctx,
		snapshotPayload(semantic),
		target.Budget,
		selection,
		target.Selection,
	)
	if policyErr != nil {
		return ConversationSnapshot{}, nil, nil, policyErr
	}
	scoped = payloadSnapshot(accepted).WithVersion(scoped.Version()).WithArtifacts(selectedArtifacts)
	working = snapshotAllMessages(scoped)
	localTransforms = recorder.snapshot()
	return scoped, working, localTransforms, nil
}

func initializeTargetArtifactExclusions(ctx context.Context, artifacts []ContextArtifact) error {
	exclusionsMap, _ := ctx.Value(artifactExclusionsKey{}).(map[ContentRef]string)
	for _, artifact := range artifacts {
		ref, refErr := ArtifactContentRef(artifact)
		if refErr != nil {
			return refErr
		}
		exclusionsMap[ref] = coverageNotSelected
	}
	return nil
}

func finishTargetSnapshot(
	ctx context.Context,
	scoped ConversationSnapshot,
	target CompileTarget,
	selectedArtifacts, artifacts []ContextArtifact,
	working []Message,
) (ConversationSnapshot, []ContextArtifact, []ArtifactExclusion, error) {
	selectedArtifacts, err := finalParticipatingArtifacts(ctx, selectedArtifacts, working)
	if err != nil {
		return ConversationSnapshot{}, nil, nil, err
	}
	exclusions, err := artifactExclusionsForOutput(ctx, artifacts, selectedArtifacts)
	if err != nil {
		return ConversationSnapshot{}, nil, nil, err
	}
	scoped = snapshotWithFinalMessages(scoped, working).WithArtifacts(selectedArtifacts)
	if target.Formatter != nil {
		scoped = EmptySnapshot().WithVersion(scoped.Version()).
			WithSegment(SegmentHistory, working).
			WithArtifacts(selectedArtifacts)
	}
	if err := validateFinalSelectionRounds(scoped); err != nil {
		return ConversationSnapshot{}, nil, nil, err
	}
	if err := validateUniqueMessageIDs(working); err != nil {
		return ConversationSnapshot{}, nil, nil, fmt.Errorf(
			"contexty: compile target %q identity: %w",
			target.Name,
			err,
		)
	}
	return scoped, selectedArtifacts, exclusions, nil
}

func preparedTargetContext(
	ctx context.Context,
	source CompileRequest,
	snap ConversationSnapshot,
	name string,
	transforms map[string]TransformChain,
) context.Context {
	ctx = withCompileIdentity(ctx, source.IdentityPolicy, source.RequireDurableIdentity, source.TurnID, name)
	if trace := traceFromContext(ctx); trace != nil {
		ctx = context.WithValue(ctx, compileTraceKey{}, trace.branch(name))
	}
	// Target-local pipeline events cannot mutate the recorder of the shared pass.
	targetRecorder := newTransformRecorder(source.AllMessages())
	for id, chain := range transforms {
		targetRecorder.records[id] = append(TransformChain(nil), chain...)
	}
	targetRecorder.registerDeferredMessageIDs(source.ToSnapshot(), snap)
	ctx = withTransformRecorder(ctx, targetRecorder)
	ctx = context.WithValue(ctx, artifactExclusionsKey{}, make(map[ContentRef]string))
	ctx = context.WithValue(ctx, artifactEstimatesKey{}, make(map[ContentRef]ArtifactBudgetEstimate))
	return ctx
}

func (e *Engine) compileTextViewTarget(ctx context.Context, snap ConversationSnapshot, target CompileTarget,
) (CompileProjection, error) {
	input := snap.AllSegmentsSnapshot()
	accepted, policyErr := e.acceptSemanticOutput(ctx, snapshotPayload(snap), nil, nil, nil)
	if policyErr != nil {
		return CompileProjection{}, policyErr
	}
	snap = payloadSnapshot(accepted).WithVersion(snap.Version()).WithArtifacts(snap.Artifacts())
	transforms := transformRecorderFrom(ctx).snapshot()
	ctx = withRecordingComponent(
		ctx,
		recordingKey(RecordingViewRenderer, strings.TrimSpace(target.Name), "", 0),
		"render",
	)
	f, _ := builtinViewFormatter(target.View)
	text, err := f.Format(ctx, snap)
	if err != nil {
		return CompileProjection{}, fmt.Errorf("contexty: compile target %q: %w", target.Name, err)
	}
	var rendered *RenderedOutput
	if trace := traceFromContext(ctx); trace != nil {
		output, renderErr := trace.captureRendering(ctx, snap, text)
		if renderErr != nil {
			return CompileProjection{}, renderErr
		}
		rendered = &output
	}
	return CompileProjection{
		Name: strings.TrimSpace(target.Name), Text: text, Messages: nil,
		Transformations: transforms, Source: emptyCompileRequest(),
		InputSnapshot: input, ArtifactIDs: artifactIDs(snap.Artifacts()),
		Lineage: traceGraph(ctx), Rendered: rendered,
		Snapshot: snap.AllSegmentsSnapshot(), Selection: nil, Artifacts: cloneArtifacts(snap.Artifacts()),
		ArtifactEstimates: nil, ExcludedArtifacts: nil,
	}, nil
}

func formatTargetMessages(ctx context.Context, target CompileTarget, seg SegmentName,
	working []Message,
) ([]Message, error) {
	ctx = withRecordingComponent(
		ctx,
		recordingKey(RecordingTargetFormatter, strings.TrimSpace(target.Name), "", 0),
		"format",
	)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	formatted, err := target.Formatter(ctx, cloneMessageSlice(working))
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, fmt.Errorf("contexty: compile target %q formatter: %w", target.Name, err)
	}
	formatted, err = ensureMessageIDsFromContext(ctx, seg, 0, formatted)
	if err != nil {
		return nil, fmt.Errorf("contexty: compile target %q identity: %w", target.Name, err)
	}
	return traceStage(ctx, "format", working, formatted, false)
}

func emptyCompileRequest() CompileRequest {
	return CompileRequest{
		DeferredResources:      nil,
		TurnID:                 "",
		System:                 nil,
		History:                nil,
		Memory:                 nil,
		Tools:                  nil,
		Pending:                nil,
		CurrentTurn:            nil,
		Artifacts:              nil,
		Options:                nil,
		IdentityPolicy:         nil,
		RequireDurableIdentity: false,
		Targets:                nil,
		CompilationID:          "",
		Lineage:                Lineage{Records: nil, Unresolved: nil},
		Origins:                nil,
		SourceRevision:         0,
		PreviousRecord:         nil,
	}
}

func plainMessagesText(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(formatPartsPlain(m.Parts))
	}
	return b.String()
}

func recordProjectionFormatterTransforms(records map[string]TransformChain, before, after []Message) {
	beforeSet := messageIDSet(before)
	afterSet := messageIDSet(after)
	for _, msg := range before {
		if msg.ID == "" {
			continue
		}
		if _, kept := afterSet[msg.ID]; !kept {
			records[msg.ID] = append(
				records[msg.ID],
				TransformRecord{Action: ActionFormatted, Reason: ReasonReplacedByFormatter},
			)
			continue
		}
		formatted := findMessageByID(after, msg.ID)
		if !MessageEqual(msg, formatted) {
			records[msg.ID] = append(
				records[msg.ID],
				TransformRecord{Action: ActionFormatted, Reason: ReasonSegmentFormatter},
			)
		}
	}
	for _, msg := range after {
		if msg.ID == "" {
			continue
		}
		if _, existed := beforeSet[msg.ID]; !existed {
			records[msg.ID] = append(
				records[msg.ID],
				TransformRecord{Action: ActionFormatted, Reason: ReasonSegmentFormatter},
			)
		}
	}
}

func cloneCompileTargets(targets []CompileTarget) []CompileTarget {
	out := append([]CompileTarget(nil), targets...)
	for i := range out {
		out[i].Segments = append([]SegmentName(nil), out[i].Segments...)
		out[i].ArtifactRefs = append([]ContentRef(nil), out[i].ArtifactRefs...)
		if out[i].Selection != nil {
			cp := *out[i].Selection
			cp.Required = append([]ContentRef(nil), cp.Required...)
			out[i].Selection = &cp
		}
	}
	return out
}

func scopeTargetSnapshot(
	snap ConversationSnapshot,
	target CompileTarget,
	artifacts []ContextArtifact,
) (ConversationSnapshot, []ContextArtifact, error) {
	selected, err := targetArtifacts(target, artifacts)
	if err != nil {
		return ConversationSnapshot{}, nil, err
	}
	allIDs := make(map[string]bool)
	selectedIDs := make(map[string]bool)
	for _, artifact := range artifacts {
		allIDs["artifact:"+artifact.ID] = true
	}
	for _, artifact := range selected {
		selectedIDs["artifact:"+artifact.ID] = true
	}
	out := EmptySnapshot().WithVersion(snap.Version()).WithArtifacts(selected)
	for _, segment := range snapshotSegmentOrder() {
		var messages []Message
		for _, message := range snap.Segment(segment) {
			if selectedIDs[message.ID] || (!allIDs[message.ID] && containsSegment(target.Segments, segment)) {
				messages = append(messages, message)
			}
		}
		out = out.WithSegment(segment, messages)
	}
	return out, selected, nil
}

func containsSegment(segments []SegmentName, name SegmentName) bool {
	return slices.Contains(segments, name)
}

func targetArtifacts(target CompileTarget, artifacts []ContextArtifact) ([]ContextArtifact, error) {
	if target.IncludeArtifacts {
		if len(target.ArtifactRefs) != 0 {
			return nil, ErrInvalidSelection
		}
		return cloneArtifacts(artifacts), nil
	}
	requested := make(map[ContentRef]bool)
	for _, ref := range target.ArtifactRefs {
		if requested[ref] {
			return nil, ErrDuplicateSelection
		}
		requested[ref] = true
	}
	var out []ContextArtifact
	for _, artifact := range artifacts {
		ref, err := ArtifactContentRef(artifact)
		if err != nil {
			return nil, err
		}
		if requested[ref] {
			out = append(out, artifact.Clone())
			delete(requested, ref)
		}
	}
	if len(requested) > 0 {
		return nil, ErrUnavailableCandidate
	}
	return out, nil
}

func participatingArtifactIDs(artifacts []ContextArtifact, messages []Message) []string {
	var ids []string
	for _, artifact := range artifacts {
		for _, message := range messages {
			if message.ID == "artifact:"+artifact.ID {
				ids = append(ids, artifact.ID)
				break
			}
		}
	}
	return ids
}

func artifactIDs(artifacts []ContextArtifact) []string {
	var ids []string
	for _, artifact := range artifacts {
		ids = append(ids, artifact.ID)
	}
	return ids
}

func finishTargetMessages(
	ctx context.Context,
	target CompileTarget,
	working []Message,
	localTransforms map[string]TransformChain,
) ([]Message, error) {
	if target.Formatter != nil {
		before := cloneMessageSlice(working)
		formatted, formatErr := formatTargetMessages(ctx, target, SegmentHistory, working)
		if formatErr != nil {
			return nil, formatErr
		}
		working = formatted
		recordProjectionFormatterTransforms(localTransforms, before, working)
	}
	projected, err := traceStage(ctx, "project", working, working, false)
	if err != nil {
		return nil, err
	}
	working = projected

	return working, nil
}
