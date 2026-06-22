package contexty

import (
	"context"
	"fmt"
	"time"
)

// DeferredBlock resolves content lazily at compile time into a target segment.
type DeferredBlock struct {
	Name        string
	Segment     SegmentName
	MergePolicy MergePolicy
	Resolve     func(ctx context.Context) ([]Message, error)
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
	stateStore     ConversationStateStore
	hooks          []TransformHook
	budget         *BudgetPipeline
	deferred       []DeferredBlock
	formatters     map[SegmentName]SegmentFormatter
	views          map[string]ViewConfiguration
	roleProjection RoleProjectionPolicy
	conversationID string
	observer       Observer
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
	return func(e *Engine) { e.deferred = append(e.deferred, blocks...) }
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
		stateStore:     nil,
		hooks:          nil,
		budget:         nil,
		deferred:       nil,
		formatters:     nil,
		views:          defaultViewRegistry(),
		roleProjection: nil,
		conversationID: "",
		observer:       nil,
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
		Targets:                append([]CompileTarget(nil), req.Targets...),
	}
}

func (e *Engine) compileRequest(ctx context.Context, req CompileRequest, start time.Time) (CompileResult, error) {
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
		compiled.Context,
		compiled.Snapshot,
		req.Targets,
		frozenSource,
		compiled.Transformations,
		compiled.ActiveArtifacts,
	)
	if err != nil {
		return CompileResult{}, err
	}
	if obs := e.resolveCompileObserver(); obs != nil {
		if total, estErr := e.estimatePayloadTokens(compiled.Context, compiled.Payload); estErr == nil {
			obs.OnPipelineCompiled(compiled.Context, total, time.Since(start))
		}
	}
	return CompileResult{
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
		Projections: cloneCompileProjections(projections),
	}, nil
}

type compilePipelineResult struct {
	Context         context.Context
	Snapshot        ConversationSnapshot
	Payload         AbstractPayload
	Transformations map[string]TransformRecord
	Introduced      map[string]Message
	ActiveArtifacts []ContextArtifact
}

func (e *Engine) runCompilePipeline(ctx context.Context, req CompileRequest) (compilePipelineResult, error) {
	ctx = withCompileIdentity(ctx, req.IdentityPolicy, req.RequireDurableIdentity, req.TurnID, "")
	compileOpts := applyCompileOptions(req.Options)
	if len(compileOpts.resolveVars) > 0 {
		ctx = withCompileResolveVars(ctx, compileOpts.resolveVars)
	}

	recorder := newTransformRecorder(req.AllMessages())
	ctx = withTransformRecorder(ctx, recorder)
	compilePending := req.compilePendingMessages()
	snap, activeArtifacts := req.snapshotWithActiveArtifacts()
	var err error

	beforeDeferred := snap
	snap, err = e.applyDeferredBlocks(ctx, snap)
	if err != nil {
		return compilePipelineResult{}, err
	}
	recorder.registerDeferredMessageIDs(beforeDeferred, snap)
	if idErr := validateSnapshotUniqueIDs(snap); idErr != nil {
		return compilePipelineResult{}, fmt.Errorf("contexty: compile deferred: %w", idErr)
	}

	snap = applyEphemeralPatches(ctx, snap, compileOpts, patchPhasePreBudget)
	if idErr := validateSnapshotUniqueIDs(snap); idErr != nil {
		return compilePipelineResult{}, fmt.Errorf("contexty: compile patches: %w", idErr)
	}

	snap, err = e.applyTransformsAndBudget(ctx, req, snap, compilePending, recorder)
	if err != nil {
		return compilePipelineResult{}, err
	}
	payload := e.payloadFromSnapshot(snap, compilePending)
	return compilePipelineResult{
		Context:         ctx,
		Snapshot:        snap,
		Payload:         payload,
		Transformations: recorder.snapshot(),
		Introduced:      recorder.introducedSnapshot(),
		ActiveArtifacts: cloneArtifacts(activeArtifacts),
	}, nil
}

func (e *Engine) applyTransformsAndBudget(
	ctx context.Context,
	req CompileRequest,
	snap ConversationSnapshot,
	compilePending []Message,
	recorder *transformRecorder,
) (ConversationSnapshot, error) {
	beforeHooks := snap
	snap, err := e.applyCompileHooks(ctx, snap)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	snap, err = normalizeSnapshotMessageIDs(ctx, snap)
	if err != nil {
		return ConversationSnapshot{}, fmt.Errorf("contexty: compile hooks identity: %w", err)
	}
	recordSnapshotHookTransforms(ctx, beforeHooks, snap)
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
	snap = applyEphemeralPatches(ctx, snap, applyCompileOptions(req.Options), patchPhasePostBudget)
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

func (r CompileRequest) snapshotWithActiveArtifacts() (ConversationSnapshot, []ContextArtifact) {
	snap := r.ToSnapshot()
	activeArtifacts := activeArtifactsForTurn(r.TurnID, r.Artifacts)
	snap = snap.WithArtifacts(activeArtifacts)
	if artifactMsgs := artifactMessages(activeArtifacts); len(artifactMsgs) > 0 {
		memory := snap.Segment(SegmentMemory)
		memory = append(memory, artifactMsgs...)
		snap = snap.WithSegment(SegmentMemory, memory)
	}
	return snap, activeArtifacts
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
	totalLimit := e.budget.cfg.TokenLimit
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
	trimmed, err := e.budget.ApplyWithLimit(ctx, history, available)
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
	next := snap
	for _, seg := range snapshotSegmentOrder() {
		msgs := next.Segment(seg)
		if len(msgs) == 0 {
			continue
		}
		projected := cloneMessageSlice(msgs)
		for i := range projected {
			role, err := e.roleProjection.ProjectRole(projected[i])
			if err != nil {
				return ConversationSnapshot{}, fmt.Errorf("contexty: role projection: %w", err)
			}
			projected[i].Role = role
		}
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
		after, err := fn(ctx, cloneMessageSlice(before))
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: segment formatter %q: %w", seg, err)
		}
		after, err = ensureMessageIDsFromContext(ctx, seg, 0, after)
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: segment formatter %q identity: %w", seg, err)
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
	return estimator.Estimate(ctx, payload.FlattenMessages())
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
	next, err := TransformPipeline(ctx, snap, e.hooks...)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	return next, nil
}

func (e *Engine) applyDeferredBlocks(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error) {
	for _, block := range e.deferred {
		if block.Resolve == nil {
			continue
		}
		msgs, err := block.Resolve(ctx)
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: deferred %q: %w", block.Name, err)
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
		combined := applyMergePolicy(existing, msgs, block.MergePolicy)
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
