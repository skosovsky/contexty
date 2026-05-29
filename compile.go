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

// AbstractPayload is the provider-agnostic compiled context tree.
type AbstractPayload struct {
	System  []Message `json:"system"`
	History []Message `json:"history"`
	Tools   []Message `json:"tools"`
	Memory  []Message `json:"memory"`
	Overlay Overlay   `json:"overlay,omitempty"`
}

// Engine compiles conversation snapshots into AbstractPayload.
type Engine struct {
	store          ConversationStore
	hooks          []TransformHook
	budget         *BudgetPipeline
	deferred       []DeferredBlock
	conversationID string
	overlay        Overlay
	budgetSeg      SegmentName
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

// WithTransformHooks adds transform hooks applied before budgeting.
func WithTransformHooks(hooks ...TransformHook) EngineOption {
	return func(e *Engine) { e.hooks = append(e.hooks, hooks...) }
}

// WithBudgetPipeline sets the unified budget pipeline for a segment.
func WithBudgetPipeline(seg SegmentName, pipe *BudgetPipeline) EngineOption {
	return func(e *Engine) {
		e.budget = pipe
		e.budgetSeg = seg
	}
}

// WithDeferredBlocks registers lazy blocks resolved at compile.
func WithDeferredBlocks(blocks ...DeferredBlock) EngineOption {
	return func(e *Engine) { e.deferred = append(e.deferred, blocks...) }
}

// NewEngine creates a compile engine.
func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{
		store:          nil,
		hooks:          nil,
		budget:         nil,
		deferred:       nil,
		conversationID: "",
		overlay:        make(Overlay),
		budgetSeg:      SegmentHistory,
		observer:       nil,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// WithOverlay returns a shallow copy of the engine with ephemeral overlay vars.
func (e *Engine) WithOverlay(vars Overlay) *Engine {
	clone := *e
	clone.overlay = make(Overlay, len(vars))
	maps.Copy(clone.overlay, vars)
	return &clone
}

// Compile loads snapshot, resolves deferred blocks, applies hooks, budgeting, and returns payload.
func (e *Engine) Compile(ctx context.Context) (AbstractPayload, error) {
	if err := ctx.Err(); err != nil {
		return AbstractPayload{}, fmt.Errorf("contexty: compile: %w", err)
	}
	start := time.Now()
	snap, err := e.loadSnapshot(ctx)
	if err != nil {
		return AbstractPayload{}, err
	}
	return e.compileFromSnapshot(ctx, snap, start)
}

// CompileSnapshot compiles an in-memory snapshot without loading from Store or conversationID.
// Deferred blocks, transform hooks, budgeting, and observer telemetry run identically to Compile().
func (e *Engine) CompileSnapshot(ctx context.Context, snap ConversationSnapshot) (AbstractPayload, error) {
	if err := ctx.Err(); err != nil {
		return AbstractPayload{}, fmt.Errorf("contexty: compile snapshot: %w", err)
	}
	return e.compileFromSnapshot(ctx, snap, time.Now())
}

func (e *Engine) compileFromSnapshot(
	ctx context.Context,
	snap ConversationSnapshot,
	start time.Time,
) (AbstractPayload, error) {
	snap, err := e.applyDeferredBlocks(ctx, snap)
	if err != nil {
		return AbstractPayload{}, err
	}
	snap, err = e.applyCompileHooks(ctx, snap)
	if err != nil {
		return AbstractPayload{}, err
	}
	snap, err = e.applyBudgetSegment(ctx, snap)
	if err != nil {
		return AbstractPayload{}, err
	}
	payload := e.payloadFromSnapshot(snap)
	if obs := e.resolveCompileObserver(); obs != nil {
		if total, estErr := e.estimatePayloadTokens(ctx, payload); estErr == nil {
			obs.OnPipelineCompiled(ctx, total, time.Since(start))
		}
	}
	return payload, nil
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
	return TransformPipeline(ctx, snap, e.hooks...)
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

func (e *Engine) applyBudgetSegment(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error) {
	if e.budget == nil {
		return snap, nil
	}
	seg := e.budgetSeg
	if seg == "" {
		seg = SegmentHistory
	}
	msgs := snap.Segment(seg)
	ctx = withBudgetObservation(ctx, e.resolveBudgetObserver(), string(seg))
	trimmed, err := e.budget.Apply(ctx, msgs)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	return snap.WithSegment(seg, trimmed), nil
}

func (e *Engine) payloadFromSnapshot(snap ConversationSnapshot) AbstractPayload {
	payload := AbstractPayload{
		System:  snap.Segment(SegmentSystem),
		History: snap.Segment(SegmentHistory),
		Tools:   snap.Segment(SegmentTools),
		Memory:  snap.Segment(SegmentMemory),
		Overlay: nil,
	}
	if len(e.overlay) > 0 {
		payload.Overlay = make(Overlay, len(e.overlay))
		maps.Copy(payload.Overlay, e.overlay)
	}
	return payload
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
