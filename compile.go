package contexty

import (
	"context"
	"fmt"
	"maps"
	"time"
)

// DeferredBlock resolves content lazily at compile time into a target segment.
type DeferredBlock struct {
	Name    string
	Segment SegmentName
	Resolve func(ctx context.Context) ([]Message, error)
}

// Overlay holds ephemeral compile-time variables (not persisted).
type Overlay map[string]string

// AbstractPayload is the provider-agnostic compiled context tree (immutable LLM-ready output).
type AbstractPayload struct {
	System  []Message `json:"system"`
	History []Message `json:"history"`
	Tools   []Message `json:"tools"`
	Memory  []Message `json:"memory"`
}

// Engine compiles conversation snapshots into CompileResult.
type Engine struct {
	store          ConversationStore
	hooks          []TransformHook
	budget         *BudgetPipeline
	deferred       []DeferredBlock
	formatters     map[SegmentName]SegmentFormatter
	conversationID string
	overlay        Overlay
	observer       Observer
}

// EngineOption configures the compile engine.
type EngineOption func(*Engine)

// WithConversationID sets the conversation identifier for store Load/Compile.
func WithConversationID(id string) EngineOption {
	return func(e *Engine) { e.conversationID = id }
}

// WithStore sets the conversation store.
func WithStore(store ConversationStore) EngineOption {
	return func(e *Engine) { e.store = store }
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

// NewEngine creates a compile engine.
func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{
		store:          nil,
		hooks:          nil,
		budget:         nil,
		deferred:       nil,
		formatters:     nil,
		conversationID: "",
		overlay:        make(Overlay),
		observer:       nil,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// WithOverlay returns a shallow copy of the engine with ephemeral overlay vars for deferred resolve.
func (e *Engine) WithOverlay(vars Overlay) *Engine {
	clone := *e
	clone.overlay = make(Overlay, len(vars))
	maps.Copy(clone.overlay, vars)
	return &clone
}

// Compile loads from store when configured, merges with req, and compiles.
func (e *Engine) Compile(ctx context.Context, req CompileRequest) (CompileResult, error) {
	if err := ctx.Err(); err != nil {
		return CompileResult{}, fmt.Errorf("contexty: compile: %w", err)
	}
	merged, err := e.mergeWithStore(ctx, req)
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

func (e *Engine) mergeWithStore(ctx context.Context, req CompileRequest) (CompileRequest, error) {
	snap, err := e.loadSnapshot(ctx)
	if err != nil {
		return CompileRequest{}, err
	}
	merged := mergeCompileRequest(snap, req)
	merged = merged.Normalize()
	if err := merged.Validate(); err != nil {
		return CompileRequest{}, fmt.Errorf("contexty: compile merge: %w", err)
	}
	return merged, nil
}

func mergeCompileRequest(storeSnap ConversationSnapshot, req CompileRequest) CompileRequest {
	pick := func(reqMsgs, storeMsgs []Message) []Message {
		if len(reqMsgs) > 0 {
			return reqMsgs
		}
		return storeMsgs
	}
	return CompileRequest{
		System:  pick(req.System, storeSnap.Segment(SegmentSystem)),
		History: pick(req.History, storeSnap.Segment(SegmentHistory)),
		Memory:  pick(req.Memory, storeSnap.Segment(SegmentMemory)),
		Tools:   req.Tools,
		Pending: req.Pending,
	}
}

func (e *Engine) compileRequest(ctx context.Context, req CompileRequest, start time.Time) (CompileResult, error) {
	req = req.Normalize()
	if err := req.Validate(); err != nil {
		return CompileResult{}, fmt.Errorf("contexty: compile request: %w", err)
	}
	recorder := newTransformRecorder(req.AllMessages())
	ctx = withTransformRecorder(ctx, recorder)

	snap := req.ToSnapshot()
	var err error

	beforeDeferred := snap
	snap, err = e.applyDeferredBlocks(ctx, snap)
	if err != nil {
		return CompileResult{}, err
	}
	recorder.registerDeferredMessageIDs(beforeDeferred, snap)
	if idErr := validateSnapshotUniqueIDs(snap); idErr != nil {
		return CompileResult{}, fmt.Errorf("contexty: compile deferred: %w", idErr)
	}

	beforeHooks := snap
	snap, err = e.applyCompileHooks(ctx, snap)
	if err != nil {
		return CompileResult{}, err
	}
	recordSnapshotHookTransforms(ctx, beforeHooks, snap)
	snap = e.applySegmentFormatters(ctx, snap)
	if idErr := validateSnapshotUniqueIDs(snap); idErr != nil {
		return CompileResult{}, fmt.Errorf("contexty: compile formatters: %w", idErr)
	}

	snap, err = e.applyBudgetHistory(ctx, snap, req.Pending)
	if err != nil {
		return CompileResult{}, err
	}

	pendingIDs := messageIDs(req.Pending)
	recorder.markProtectedPending(pendingIDs)

	payload := e.payloadFromSnapshot(snap, req.Pending)
	if obs := e.resolveCompileObserver(); obs != nil {
		if total, estErr := e.estimatePayloadTokens(ctx, payload); estErr == nil {
			obs.OnPipelineCompiled(ctx, total, time.Since(start))
		}
	}
	return CompileResult{
		Payload:         payload,
		Transformations: recorder.snapshot(),
	}, nil
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

func (e *Engine) applySegmentFormatters(ctx context.Context, snap ConversationSnapshot) ConversationSnapshot {
	if len(e.formatters) == 0 {
		return snap
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
		after := EnsureMessageIDs(fn(cloneMessageSlice(before)))
		recordFormatterTransformCtx(ctx, before, after)
		next = next.WithSegment(seg, after)
	}
	return next
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
	if e.store != nil && e.conversationID != "" {
		snap, err := e.store.Load(ctx, e.conversationID)
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: compile load: %w", err)
		}
		return snap, nil
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
	if len(e.overlay) > 0 {
		ctx = WithCompileOverlay(ctx, e.overlay)
	}
	for _, block := range e.deferred {
		if block.Resolve == nil {
			continue
		}
		msgs, err := block.Resolve(ctx)
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: deferred %q: %w", block.Name, err)
		}
		msgs = EnsureMessageIDs(msgs)
		seg := block.Segment
		if seg == "" {
			seg = SegmentMemory
		}
		existing := snap.Segment(seg)
		combined := make([]Message, len(existing)+len(msgs))
		copy(combined, existing)
		copy(combined[len(existing):], msgs)
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
