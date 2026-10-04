package contexty

import (
	"context"
	"fmt"
	"time"
)

// DeferredBlock resolves content lazily at compile time into a target segment.
type DeferredBlock struct {
	Name          string
	Segment       SegmentName
	MergePolicy   MergePolicy
	Resolve       func(ctx context.Context) (DeferredResult, error)
	Resources     []ResourceSelection
	ResourceCodec ResourceCodec
}

// DeferredResult is explicit lazy content. Structured resource dependency
// evidence is added through the resource integration, never inferred from text.
type DeferredResult struct {
	Messages  []Message
	Resources []ResolvedResource
}

// AbstractPayload is the provider-agnostic compiled context tree (immutable LLM-ready output).
type AbstractPayload struct {
	System  []Message `json:"system"`
	History []Message `json:"history"`
	Tools   []Message `json:"tools"`
	Memory  []Message `json:"memory"`
}

// Engine compiles conversation snapshots into CompileResult.
type Engine struct {
	selection      *SelectionPolicy
	stateStore     ConversationStateStore
	hooks          []TransformHook
	budget         *BudgetPipeline
	deferred       []DeferredBlock
	formatters     map[SegmentName]SegmentFormatter
	views          map[string]ViewConfiguration
	roleProjection RoleProjectionPolicy
	conversationID string
	observer       Observer
	trace          *TraceProfile
	recording      *RecordProfile
	capture        *recordCaptureOptions
}

// EngineOption configures the compile engine.
type EngineOption func(*Engine)

// WithConversationID sets the conversation identifier for store Load/Compile.
func WithConversationID(id string) EngineOption {
	return func(e *Engine) { e.conversationID = id }
}

// WithStateStore sets the immutable conversation state store.
func WithStateStore(store ConversationStateStore) EngineOption {
	return func(e *Engine) { e.stateStore = store }
}

// WithTransformHooks adds transform hooks applied before formatting and budgeting.
func WithTransformHooks(hooks ...TransformHook) EngineOption {
	return func(e *Engine) { e.hooks = append(e.hooks, hooks...) }
}

// WithBudgetPipeline sets the unified budget pipeline (history segment is trimmed at compile).
func WithBudgetPipeline(_ SegmentName, pipe *BudgetPipeline) EngineOption {
	return func(e *Engine) {
		e.budget = pipe
	}
}

// WithDeferredBlocks registers lazy blocks resolved at compile.
func WithDeferredBlocks(blocks ...DeferredBlock) EngineOption {
	return func(e *Engine) {
		for _, block := range blocks {
			block.Resources = cloneResourceSelections(block.Resources)
			block.ResourceCodec = block.ResourceCodec.snapshot()
			e.deferred = append(e.deferred, block)
		}
	}
}

// WithSegmentFormatter registers a host formatter for a segment (runs before budget).
func WithSegmentFormatter(seg SegmentName, fn SegmentFormatter) EngineOption {
	return func(e *Engine) {
		if e.formatters == nil {
			e.formatters = make(map[SegmentName]SegmentFormatter)
		}
		e.formatters[seg] = fn
	}
}

// WithRoleProjectionPolicy configures provider-role projection for actor-aware messages.
func WithRoleProjectionPolicy(policy RoleProjectionPolicy) EngineOption {
	return func(e *Engine) { e.roleProjection = policy }
}

// NewEngine creates a compile engine.
func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{
		selection:      nil,
		stateStore:     nil,
		hooks:          nil,
		budget:         nil,
		deferred:       nil,
		formatters:     nil,
		views:          defaultViewRegistry(),
		roleProjection: nil,
		conversationID: "",
		observer:       nil,
		trace:          nil,
		recording:      nil,
		capture:        nil,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Compile loads from store when configured, merges with req, and compiles.
func (e *Engine) Compile(ctx context.Context, req CompileRequest) (CompileResult, error) {
	if err := ctx.Err(); err != nil {
		return CompileResult{}, fmt.Errorf("contexty: compile: %w", err)
	}
	if err := e.validateCompileConfiguration(req); err != nil {
		return CompileResult{}, err
	}
	merged, err := e.mergeWithStateStore(ctx, req)
	if err != nil {
		return CompileResult{}, err
	}
	return e.compileRequest(ctx, merged, time.Now())
}

// CompileSnapshot compiles req without loading from Store or conversationID.
func (e *Engine) CompileSnapshot(ctx context.Context, req CompileRequest) (CompileResult, error) {
	if err := ctx.Err(); err != nil {
		return CompileResult{}, fmt.Errorf("contexty: compile snapshot: %w", err)
	}
	return e.compileRequest(ctx, req, time.Now())
}

func (e *Engine) mergeWithStateStore(ctx context.Context, req CompileRequest) (CompileRequest, error) {
	snap, err := e.loadSnapshot(ctx)
	if err != nil {
		return CompileRequest{}, err
	}
	if e.stateStore != nil {
		req.SourceRevision = snap.Version()
	}
	return mergeCompileRequest(snap, req), nil
}

func mergeCompileRequest(storeSnap ConversationSnapshot, req CompileRequest) CompileRequest {
	pick := func(reqMsgs, storeMsgs []Message) []Message {
		if len(reqMsgs) > 0 {
			return reqMsgs
		}
		return storeMsgs
	}
	return CompileRequest{
		DeferredResources:      cloneResourceSelections(req.DeferredResources),
		TurnID:                 req.TurnID,
		System:                 pick(req.System, storeSnap.Segment(SegmentSystem)),
		History:                pick(req.History, storeSnap.Segment(SegmentHistory)),
		Memory:                 pick(req.Memory, storeSnap.Segment(SegmentMemory)),
		Tools:                  req.Tools,
		Pending:                req.Pending,
		CurrentTurn:            cloneCurrentTurnPtr(req.CurrentTurn),
		Artifacts:              append(storeSnap.Artifacts(), cloneArtifacts(req.Artifacts)...),
		Options:                req.Options,
		IdentityPolicy:         req.IdentityPolicy,
		RequireDurableIdentity: req.RequireDurableIdentity,
		Targets:                cloneCompileTargets(req.Targets),
		CompilationID:          req.CompilationID,
		Lineage:                req.Lineage.Clone(),
		Origins:                append([]ContentRef(nil), req.Origins...),
		SourceRevision:         req.SourceRevision,
		PreviousRecord:         cloneContentRef(req.PreviousRecord),
	}
}

func (e *Engine) compileRequest(ctx context.Context, req CompileRequest, start time.Time) (CompileResult, error) {
	req.DeferredResources = e.deferredResourceSelections()
	if recordErr := e.validateCompileConfiguration(req); recordErr != nil {
		return CompileResult{}, recordErr
	}
	var identityWritebacks []MessageIdentityWriteback
	var err error
	req, identityWritebacks, err = normalizeCompileRequest(req)
	if err != nil {
		return CompileResult{}, fmt.Errorf("contexty: compile normalize: %w", err)
	}
	if validationErr := req.Validate(); validationErr != nil {
		return CompileResult{}, fmt.Errorf("contexty: compile request: %w", validationErr)
	}

	frozenSource := req.Freeze()
	normalizedSnapshot := req.ToSnapshot()
	writebackSnapshot := req.WritebackSnapshot()
	compiled, err := e.runCompilePipeline(ctx, req)
	if err != nil {
		return CompileResult{}, err
	}
	projections, err := e.compileTargets(
		compiled.PreparedContext,
		compiled.PreparedSnapshot,
		req.Targets,
		frozenSource,
		compiled.PreparedTransforms,
		compiled.PreparedArtifacts,
	)
	if err != nil {
		return CompileResult{}, err
	}
	if obs := e.resolveCompileObserver(); obs != nil {
		if total, estErr := e.estimatePayloadTokens(compiled.Context, compiled.Payload); estErr == nil {
			obs.OnPipelineCompiled(compiled.Context, total, time.Since(start))
		}
	}
	result := CompileResult{
		Selection:          compiled.Selection.clone(),
		PreparedSnapshot:   compiled.PreparedSnapshot.AllSegmentsSnapshot(),
		Payload:            compiled.Payload,
		Transformations:    compiled.Transformations,
		Source:             frozenSource,
		Introduced:         compiled.Introduced,
		Artifacts:          cloneArtifacts(compiled.ActiveArtifacts),
		NormalizedSnapshot: normalizedSnapshot.AllSegmentsSnapshot(),
		Writeback: CompileWritebackIntent{
			Snapshot: writebackSnapshot,
			Messages: identityWritebacks,
		}.clone(),
		Projections:       cloneCompileProjections(projections),
		Lineage:           traceGraph(compiled.Context),
		Manifest:          nil,
		Record:            nil,
		Estimates:         nil,
		ArtifactEstimates: nil,
		Compactions:       nil,
		BudgetDecisions:   compileBudgetDecisions(compiled.Context),
	}
	result.Estimates, err = compileEstimateReports(compiled.Context)
	if err != nil {
		return CompileResult{}, err
	}
	result.ArtifactEstimates, err = compileArtifactEstimates(
		compiled.Context,
		compileArtifactEvidence(compiled.Context, result.Source.Artifacts),
	)
	if err != nil {
		return CompileResult{}, err
	}
	return e.finalizeCompileRecording(compiled.Context, result)
}

func (e *Engine) finalizeCompileRecording(ctx context.Context, result CompileResult) (CompileResult, error) {
	if e.recording != nil {
		manifest, recordErr := e.buildCompileManifest(ctx, result)
		if recordErr != nil {
			return CompileResult{}, recordErr
		}
		result.Manifest = &manifest
		if capture := contentCaptureFrom(ctx); capture != nil {
			if err := capture.captureOutputs(ctx, manifest, result); err != nil {
				return CompileResult{}, err
			}
		}
	}
	records, err := compileCompactions(ctx)
	if err != nil {
		return CompileResult{}, err
	}
	result.Compactions = records
	if result.Manifest != nil {
		if linkErr := linkCompileCompactions(ctx, result.Manifest, result.Compactions); linkErr != nil {
			return CompileResult{}, linkErr
		}
		if capture := contentCaptureFrom(ctx); capture != nil {
			record, captureErr := capture.finish(*result.Manifest)
			if captureErr != nil {
				return CompileResult{}, captureErr
			}
			result.Record = &record
		}
	}
	return result, nil
}

type preparedCompile struct {
	Context         context.Context
	Snapshot        ConversationSnapshot
	Transformations map[string]TransformChain
	Artifacts       []ContextArtifact
	Pending         []Message
	Options         compileOptions
	Recorder        *transformRecorder
}

type compilePipelineResult struct {
	Selection          *SelectionDecision
	PreparedContext    context.Context
	PreparedSnapshot   ConversationSnapshot
	PreparedTransforms map[string]TransformChain
	PreparedArtifacts  []ContextArtifact
	Context            context.Context
	Snapshot           ConversationSnapshot
	Payload            AbstractPayload
	Transformations    map[string]TransformChain
	Introduced         map[string]Message
	ActiveArtifacts    []ContextArtifact
}

func (e *Engine) startCompileEvidence(ctx context.Context, req CompileRequest) context.Context {
	ctx = startResourceCompile(ctx, req)
	ctx = context.WithValue(ctx, budgetDecisionsKey{}, make(map[manifestChannelKey]BudgetDecision))
	ctx = context.WithValue(ctx, finalEstimateReportsKey{}, make(map[manifestChannelKey]EstimateReport))
	ctx = context.WithValue(ctx, artifactExclusionsKey{}, make(map[ContentRef]string))
	ctx = context.WithValue(ctx, artifactEstimatesKey{}, make(map[ContentRef]ArtifactBudgetEstimate))
	ctx = context.WithValue(ctx, compactionCaptureKey{}, &compactionCaptureState{records: nil, channels: nil})
	if e.recording != nil {
		ctx = context.WithValue(ctx, finalBudgetEvidenceKey{}, make(map[manifestChannelKey]int))
		ctx = context.WithValue(ctx, recordingComponentsKey{}, cloneRecordingComponents(e.recording.Components))
	}
	return ctx
}

func (e *Engine) prepareCompile(ctx context.Context, req CompileRequest) (preparedCompile, error) {
	ctx = e.startCompileEvidence(ctx, req)
	ctx = context.WithValue(ctx, outputConfigurationKey{}, e.outputConfigurations(req))
	ctx = context.WithValue(ctx, sharedPreparationKey{}, true)
	ctx, compileOpts, err := e.prepareCompileHistoricalOptions(ctx, req)
	if err != nil {
		return preparedCompile{}, err
	}
	ctx, err = e.startContentCapture(ctx, req)
	if err != nil {
		return preparedCompile{}, err
	}
	ctx, err = e.startCompileTrace(ctx, req)
	if err != nil {
		return preparedCompile{}, err
	}
	ctx = withCompileIdentity(ctx, req.IdentityPolicy, req.RequireDurableIdentity, req.TurnID, "")

	recorder := newTransformRecorder(req.AllMessages())
	ctx = withTransformRecorder(ctx, recorder)
	compilePending, err := tracedCompilePending(ctx, req)
	if err != nil {
		return preparedCompile{}, err
	}
	snap, activeArtifacts, err := e.snapshotWithActiveArtifacts(ctx, req)
	if err != nil {
		return preparedCompile{}, err
	}
	if traceErr := traceArtifactSources(ctx, activeArtifacts); traceErr != nil {
		return preparedCompile{}, traceErr
	}
	snap, err = applyHistoricalArguments(ctx, snap, compileOpts.historicalArguments)
	if err != nil {
		return preparedCompile{}, err
	}

	beforeDeferred := snap
	setResourceActiveArtifacts(ctx, activeArtifacts)
	snap, err = e.applyDeferredBlocks(ctx, snap)
	if err != nil {
		return preparedCompile{}, err
	}
	activeArtifacts = activeResourceArtifacts(ctx)
	snap = snap.WithArtifacts(activeArtifacts)
	recorder.registerDeferredMessageIDs(beforeDeferred, snap)
	if idErr := validateSnapshotUniqueIDs(snap); idErr != nil {
		return preparedCompile{}, fmt.Errorf("contexty: compile deferred: %w", idErr)
	}

	preparedSnapshot := snap.AllSegmentsSnapshot()
	preparedTransforms := recorder.snapshot()
	preparedArtifacts := cloneArtifacts(activeArtifacts)
	preparedContext := context.WithValue(ctx, sharedPreparationKey{}, false)
	preparedContext = context.WithValue(
		preparedContext,
		preparedOutputKey{},
		preparedOutput{pending: cloneMessageSlice(compilePending), options: compileOpts},
	)
	return preparedCompile{
		Context:         preparedContext,
		Snapshot:        preparedSnapshot,
		Transformations: preparedTransforms,
		Artifacts:       preparedArtifacts,
		Pending:         compilePending,
		Options:         compileOpts,
		Recorder:        recorder,
	}, nil
}

func (e *Engine) runCompilePipeline(ctx context.Context, req CompileRequest) (compilePipelineResult, error) {
	prepared, err := e.prepareCompile(ctx, req)
	if err != nil {
		return compilePipelineResult{}, err
	}
	preparedContext := prepared.Context
	preparedSnapshot := prepared.Snapshot
	preparedTransforms := prepared.Transformations
	preparedArtifacts := prepared.Artifacts
	snap := prepared.Snapshot
	activeArtifacts := cloneArtifacts(prepared.Artifacts)
	compilePending := prepared.Pending
	compileOpts := prepared.Options
	recorder := prepared.Recorder

	ctx = preparedContext
	if trace := traceFromContext(ctx); trace != nil {
		ctx = context.WithValue(ctx, compileTraceKey{}, trace.mainBranch())
	}
	snap, mainSelection, err := selectOutput(ctx, snap, compilePending, e.selection, e.budget)
	if err != nil {
		return compilePipelineResult{}, err
	}
	if selectionErr := recordSelectionArtifactExclusions(ctx, mainSelection, activeArtifacts); selectionErr != nil {
		return compilePipelineResult{}, selectionErr
	}
	activeArtifacts = filterParticipatingArtifacts(activeArtifacts, snapshotAllMessages(snap))
	snap = snap.WithArtifacts(activeArtifacts)
	snap, err = applyCompileReplacements(ctx, snap, compileOpts, patchPhasePreBudget)
	if err != nil {
		return compilePipelineResult{}, err
	}
	if idErr := validateSnapshotUniqueIDs(snap); idErr != nil {
		return compilePipelineResult{}, fmt.Errorf("contexty: compile patches: %w", idErr)
	}

	snap, err = e.applyTransformsAndBudget(ctx, req, snap, compilePending, recorder, compileOpts)
	if err != nil {
		return compilePipelineResult{}, err
	}
	if roundErr := validateFinalSelectionRounds(snap); roundErr != nil {
		return compilePipelineResult{}, roundErr
	}
	payload := e.payloadFromSnapshot(snap, compilePending)
	activeArtifacts, err = finalParticipatingArtifacts(ctx, activeArtifacts, payload.FlattenMessages())
	if err != nil {
		return compilePipelineResult{}, err
	}
	snap = snap.WithArtifacts(activeArtifacts)
	if err := validateMandatorySelection(ctx, mainSelection, e.selection, payload.FlattenMessages()); err != nil {
		return compilePipelineResult{}, err
	}
	if e.budget != nil {
		if err := e.budget.validateRecordedRetention(ctx, payload.History); err != nil {
			return compilePipelineResult{}, err
		}
		if err := e.budget.validateSegments(ctx, payloadEstimateSegments(payload)); err != nil {
			return compilePipelineResult{}, fmt.Errorf("contexty: final payload: %w", err)
		}
	}
	return compilePipelineResult{
		Selection:          mainSelection,
		PreparedContext:    preparedContext,
		PreparedSnapshot:   preparedSnapshot,
		PreparedTransforms: preparedTransforms,
		PreparedArtifacts:  preparedArtifacts,
		Context:            ctx,
		Snapshot:           snap,
		Payload:            payload,
		Transformations:    recorder.snapshot(),
		Introduced:         recorder.introducedSnapshot(),
		ActiveArtifacts:    cloneArtifacts(activeArtifacts),
	}, nil
}

func (e *Engine) startCompileTrace(ctx context.Context, req CompileRequest) (context.Context, error) {
	if e.trace == nil {
		return ctx, nil
	}
	trace, err := newCompileTrace(e.trace, req)
	if err != nil {
		return ctx, err
	}
	ctx = context.WithValue(ctx, compileTraceKey{}, trace)
	_, err = traceStage(ctx, "source", req.AllMessages(), req.AllMessages(), false)
	return ctx, err
}

func tracedCompilePending(ctx context.Context, req CompileRequest) ([]Message, error) {
	pending := req.compilePendingMessages()
	if req.CurrentTurn == nil {
		return pending, nil
	}
	prompt, ok := req.CurrentTurn.promptMessage()
	if !ok {
		return pending, nil
	}
	template, err := tracePromptTemplate(ctx, req.CurrentTurn.Raw, prompt)
	if err != nil {
		return nil, err
	}
	projected, err := traceStage(ctx, "prompt", []Message{req.CurrentTurn.Raw, template}, []Message{prompt}, false)
	if err != nil {
		return nil, err
	}
	pending[len(pending)-1] = projected[0]
	return pending, nil
}

func (e *Engine) applyTransformsAndBudget(
	ctx context.Context,
	req CompileRequest,
	snap ConversationSnapshot,
	compilePending []Message,
	recorder *transformRecorder,
	opts compileOptions,
) (ConversationSnapshot, error) {
	snap, err := e.applyCompileHooks(ctx, snap)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	snap, err = normalizeSnapshotMessageIDs(ctx, snap)
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("contexty: compile hooks identity: %w", err)
	}
	snap, err = e.applyRoleProjection(ctx, snap)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	snap, err = e.applySegmentFormatters(ctx, snap)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	if idErr := validateSnapshotUniqueIDs(snap); idErr != nil {
		return ConversationSnapshot{}, fmt.Errorf("contexty: compile formatters: %w", idErr)
	}

	snap, err = e.applyBudgetHistory(ctx, snap, compilePending)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	snap, err = applyCompileReplacements(ctx, snap, opts, patchPhasePostBudget)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	if idErr := validateSnapshotUniqueIDs(snap); idErr != nil {
		return ConversationSnapshot{}, fmt.Errorf("contexty: compile post-budget patches: %w", idErr)
	}
	if req.CurrentTurn != nil {
		recordCurrentTurnProjectionCtx(ctx, *req.CurrentTurn)
	}
	recorder.markProtectedPending(messageIDs(compilePending))
	return snap, nil
}

func normalizeSnapshotMessageIDs(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error) {
	next := snap
	for _, seg := range snapshotSegmentOrder() {
		msgs := next.Segment(seg)
		if len(msgs) == 0 {
			continue
		}
		normalized, err := ensureMessageIDsFromContext(ctx, seg, 0, msgs)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		next = next.WithSegment(seg, normalized)
	}
	return next, nil
}

func (e *Engine) snapshotWithActiveArtifacts(
	ctx context.Context,
	r CompileRequest,
) (ConversationSnapshot, []ContextArtifact, error) {
	snap := r.ToSnapshot()
	activeArtifacts, err := e.selectArtifacts(ctx, r.TurnID, r.Artifacts)
	if err != nil {
		return ConversationSnapshot{}, nil, err
	}
	snap = snap.WithArtifacts(activeArtifacts)
	artifactMsgs, err := artifactMessages(activeArtifacts)
	if err != nil {
		return ConversationSnapshot{}, nil, err
	}
	if len(artifactMsgs) > 0 {
		memory := snap.Segment(SegmentMemory)
		memory = append(memory, artifactMsgs...)
		snap = snap.WithSegment(SegmentMemory, memory)
	}
	return snap, activeArtifacts, nil
}

func (r CompileRequest) compilePendingMessages() []Message {
	pending := cloneMessageSlice(r.Pending)
	if r.CurrentTurn == nil {
		return pending
	}
	msg, ok := r.CurrentTurn.promptMessage()
	if !ok {
		return pending
	}
	return append(pending, msg)
}

func (e *Engine) estimateSegments(
	ctx context.Context,
	est TokenEstimator,
	snap ConversationSnapshot,
	pending []Message,
) (int, error) {
	var msgs []Message
	msgs = append(msgs, snap.Segment(SegmentSystem)...)
	msgs = append(msgs, snap.Segment(SegmentMemory)...)
	msgs = append(msgs, snap.Segment(SegmentTools)...)
	msgs = append(msgs, pending...)
	return est.Estimate(ctx, msgs)
}

func (e *Engine) applyBudgetHistory(
	ctx context.Context,
	snap ConversationSnapshot,
	pending []Message,
) (ConversationSnapshot, error) {
	if e.budget == nil {
		history := snap.Segment(SegmentHistory)
		if len(pending) > 0 {
			history = append(cloneMessageSlice(history), cloneMessageSlice(pending)...)
			snap = snap.WithSegment(SegmentHistory, history)
		}
		return snap, nil
	}
	est := e.budget.estimator
	if est == nil {
		est = CharTokenEstimator{}
	}
	totalLimit, err := e.budget.cfg.Budget.Resolve()
	if err != nil {
		return ConversationSnapshot{}, err
	}
	reserved, err := e.estimateSegments(ctx, est, snap, pending)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	if overflowErr := budgetReservedOverflow(ctx, est, reserved, totalLimit, pending); overflowErr != nil {
		return ConversationSnapshot{}, overflowErr
	}
	available := totalLimit - reserved

	history := snap.Segment(SegmentHistory)
	ctx = withBudgetIdentitySegment(ctx, SegmentHistory)
	ctx = withBudgetObservation(ctx, e.resolveBudgetObserver(), string(SegmentHistory))
	budgetResult, err := e.budget.ApplyWithLimit(ctx, history, available)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	trimmed, err := traceStage(ctx, "budget", history, budgetResult.Messages, false)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	if len(pending) > 0 {
		trimmed = append(trimmed, cloneMessageSlice(pending)...)
	}
	return snap.WithSegment(SegmentHistory, trimmed), nil
}

func budgetReservedOverflow(
	ctx context.Context,
	est TokenEstimator,
	reserved, totalLimit int,
	pending []Message,
) error {
	if reserved <= totalLimit {
		return nil
	}
	if len(pending) > 0 {
		pendingOnly, err := est.Estimate(ctx, pending)
		if err != nil {
			return fmt.Errorf("contexty: budget preflight: %w", err)
		}
		if pendingOnly > totalLimit {
			return ErrPendingExceedsBudget
		}
	}
	return ErrBudgetExceeded
}

func (e *Engine) applyRoleProjection(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error) {
	if e.roleProjection == nil {
		return snap, nil
	}
	ctx = withRecordingComponent(ctx, recordingKey(RecordingRolePolicy, "", "", 0), "role")
	next := snap
	for _, seg := range snapshotSegmentOrder() {
		msgs := next.Segment(seg)
		if len(msgs) == 0 {
			continue
		}
		projected := cloneMessageSlice(msgs)
		for i := range projected {
			if err := ctx.Err(); err != nil {
				return ConversationSnapshot{}, err
			}
			role, err := e.roleProjection.ProjectRole(projected[i].Clone())
			if canceled := ctx.Err(); canceled != nil {
				return ConversationSnapshot{}, canceled
			}
			if err != nil {
				return ConversationSnapshot{}, fmt.Errorf("contexty: role projection: %w", err)
			}
			projected[i].Role = role
		}
		projected, err := traceStage(ctx, "role", msgs, projected, false)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		recordContentTransformCtx(ctx, msgs, projected, ReasonRoleProjection, "", "")
		next = next.WithSegment(seg, projected)
	}
	if err := ctx.Err(); err != nil {
		return ConversationSnapshot{}, fmt.Errorf("contexty: role projection: %w", err)
	}
	return next, nil
}

func (e *Engine) applySegmentFormatters(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error) {
	if len(e.formatters) == 0 {
		return snap, nil
	}
	next := snap
	for _, seg := range snapshotSegmentOrder() {
		fn := e.formatters[seg]
		if fn == nil {
			continue
		}
		before := next.Segment(seg)
		if len(before) == 0 {
			continue
		}
		stageCtx := withRecordingComponent(ctx, recordingKey(RecordingSegmentFormatter, "", seg, 0), "format")
		if err := ctx.Err(); err != nil {
			return ConversationSnapshot{}, err
		}
		after, err := fn(stageCtx, cloneMessageSlice(before))
		if canceled := ctx.Err(); canceled != nil {
			return ConversationSnapshot{}, canceled
		}
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: segment formatter %q: %w", seg, err)
		}
		after, err = ensureMessageIDsFromContext(ctx, seg, 0, after)
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: segment formatter %q identity: %w", seg, err)
		}
		after, err = traceStage(stageCtx, "format", before, after, false)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		recordFormatterTransformCtx(ctx, before, after)
		next = next.WithSegment(seg, after)
	}
	return next, nil
}

func (e *Engine) resolveCompileObserver() Observer {
	return e.observer
}

func (e *Engine) resolveBudgetObserver() Observer {
	if e.budget == nil {
		return nil
	}
	return e.budget.observer
}

func (e *Engine) estimatePayloadTokens(ctx context.Context, payload AbstractPayload) (int, error) {
	estimator := TokenEstimator(CharTokenEstimator{})
	if e.budget != nil && e.budget.estimator != nil {
		estimator = e.budget.estimator
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	tokens, err := estimator.Estimate(ctx, estimatorCallbackInput(estimator, payload.FlattenMessages()))
	if canceled := ctx.Err(); canceled != nil {
		return 0, canceled
	}
	return tokens, err
}

func (e *Engine) loadSnapshot(ctx context.Context) (ConversationSnapshot, error) {
	if e.stateStore != nil && e.conversationID != "" {
		state, err := e.stateStore.LoadState(ctx, e.conversationID)
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: compile load state: %w", err)
		}
		return state.AllSegmentsSnapshot(), nil
	}
	return EmptySnapshot(), nil
}

func (e *Engine) applyCompileHooks(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error) {
	if len(e.hooks) == 0 {
		return snap, nil
	}
	return TransformPipeline(ctx, snap, e.hooks...)
}

func (e *Engine) applyDeferredBlocks(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error) {
	for index, block := range e.deferred {
		if block.Resolve == nil {
			continue
		}
		stageCtx := withRecordingComponent(ctx, recordingKey(RecordingResolver, "", "", index), "deferred")
		resolved, err := block.Resolve(stageCtx)
		if canceled := ctx.Err(); canceled != nil {
			return ConversationSnapshot{}, canceled
		}
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: deferred %q: %w", block.Name, err)
		}
		msgs, err := e.resolvedDeferredMessages(stageCtx, block, resolved)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		snap, err = removeReplacedResourceMessages(ctx, snap)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		seg := block.Segment
		if seg == "" {
			seg = SegmentMemory
		}
		existing := snap.Segment(seg)
		msgs, err = ensureMessageIDsFromContext(ctx, seg, len(existing), msgs)
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: deferred %q identity: %w", block.Name, err)
		}
		msgs, err = traceStage(stageCtx, "deferred", msgs, msgs, false)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		combined := applyMergePolicy(existing, msgs, block.MergePolicy)
		inputs := append(cloneMessageSlice(existing), msgs...)
		combined, err = traceStage(ctx, "merge", inputs, combined, false)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		recordMergeRemovalsCtx(ctx, existing, combined)
		snap = snap.WithSegment(seg, combined)
	}
	return snap, nil
}

func (e *Engine) payloadFromSnapshot(snap ConversationSnapshot, pending []Message) AbstractPayload {
	history := snap.Segment(SegmentHistory)
	_ = pending // pending already merged into history segment
	return AbstractPayload{
		System:  snap.Segment(SegmentSystem),
		History: history,
		Tools:   snap.Segment(SegmentTools),
		Memory:  snap.Segment(SegmentMemory),
	}
}

func messageIDs(msgs []Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// FlattenMessages returns all payload sections in compile order.
func (p AbstractPayload) FlattenMessages() []Message {
	var out []Message
	out = append(out, p.System...)
	out = append(out, p.History...)
	out = append(out, p.Tools...)
	out = append(out, p.Memory...)
	return out
}
