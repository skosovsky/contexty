package contexty

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

// EvictionReason classifies why a message node was removed from the compiled context.
type EvictionReason string

const (
	// EvictionReasonBudget indicates the block was dropped because it exceeded the token budget.
	EvictionReasonBudget EvictionReason = "budget"
	// EvictionReasonTruncate indicates truncation removed the node to fit the limit.
	EvictionReasonTruncate EvictionReason = "truncate"
	// EvictionReasonOrphanRepair indicates post-truncation tool-pair repair removed the node.
	EvictionReasonOrphanRepair EvictionReason = "orphan_repair"
)

// Observer receives compile-time telemetry without coupling to external metrics SDKs.
// All callbacks receive the same [context.Context] passed to Compile/Apply for trace correlation.
type Observer interface {
	OnTokensEstimated(ctx context.Context, blockID string, count int)
	OnNodeEvicted(ctx context.Context, nodeID string, reason EvictionReason)
	OnContextSummarized(ctx context.Context, compressionRatio float64)
	OnPipelineCompiled(ctx context.Context, totalCost int, duration time.Duration)
}

// NoopObserver discards all observer events.
type NoopObserver struct{}

func (NoopObserver) OnTokensEstimated(context.Context, string, int)         {}
func (NoopObserver) OnNodeEvicted(context.Context, string, EvictionReason)  {}
func (NoopObserver) OnContextSummarized(context.Context, float64)           {}
func (NoopObserver) OnPipelineCompiled(context.Context, int, time.Duration) {}

// WithObserver attaches an observer to the compile engine.
func WithObserver(obs Observer) EngineOption {
	return func(e *Engine) { e.observer = obs }
}

// BudgetPipelineOption configures a budget pipeline.
type BudgetPipelineOption func(*BudgetPipeline)

// WithBudgetObserver attaches an observer to a budget pipeline (overrides engine observer for budget events).
func WithBudgetObserver(obs Observer) BudgetPipelineOption {
	return func(p *BudgetPipeline) { p.observer = obs }
}

type observeContextKey struct{}

type budgetObserve struct {
	observer Observer
	blockID  string
}

func withBudgetObservation(ctx context.Context, obs Observer, blockID string) context.Context {
	if obs == nil || blockID == "" {
		return ctx
	}
	return context.WithValue(ctx, observeContextKey{}, budgetObserve{observer: obs, blockID: blockID})
}

func budgetObservationFrom(ctx context.Context) (Observer, string) {
	v, ok := ctx.Value(observeContextKey{}).(budgetObserve)
	if !ok || v.observer == nil {
		return nil, ""
	}
	return v.observer, v.blockID
}

func observerFrom(ctx context.Context) Observer {
	obs, _ := budgetObservationFrom(ctx)
	return obs
}

func budgetBlockIDFrom(ctx context.Context) string {
	_, blockID := budgetObservationFrom(ctx)
	return blockID
}

// ensureBudgetObservation wires pipeline-level observer when context has none.
func ensureBudgetObservation(ctx context.Context, pipeObserver Observer) context.Context {
	if obs, blockID := budgetObservationFrom(ctx); obs != nil {
		if blockID != "" {
			return ctx
		}
		return withBudgetObservation(ctx, obs, "budget")
	}
	if pipeObserver == nil {
		return ctx
	}
	return withBudgetObservation(ctx, pipeObserver, "budget")
}

// MessageNodeID returns a deterministic, non-empty identifier for observer events.
// RefID takes priority; otherwise a stable hash+index fallback is used.
func MessageNodeID(msg Message, index int, blockID string) string {
	if id := strings.TrimSpace(msg.Annotations.RefID); id != "" {
		return id
	}
	if blockID == "" {
		blockID = "block"
	}
	return fmt.Sprintf("%s:index_%d:%s", blockID, index, messageFingerprint(msg))
}

func messageFingerprint(msg Message) string {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s|%s", msg.Role, msg.TextContent())
	for _, p := range msg.Parts {
		switch v := p.(type) {
		case ToolCallPart:
			_, _ = fmt.Fprintf(h, "|tc:%s:%s", v.ID, v.Name)
		case ToolResultPart:
			_, _ = fmt.Fprintf(h, "|tr:%s", v.ToolCallID)
		case TextPart:
			_, _ = fmt.Fprintf(h, "|txt:%d", len(v.Text))
		}
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

func reportEvictions(ctx context.Context, before, after []Message, reason EvictionReason) {
	obs := observerFrom(ctx)
	if obs == nil {
		return
	}
	blockID := budgetBlockIDFrom(ctx)
	if blockID == "" {
		blockID = "block"
	}
	used := make([]bool, len(after))
	for i, bm := range before {
		matched := false
		for j, am := range after {
			if used[j] {
				continue
			}
			if MessageEqual(bm, am) {
				used[j] = true
				matched = true
				break
			}
		}
		if !matched {
			nodeID := MessageNodeID(bm, i, blockID)
			obs.OnNodeEvicted(ctx, nodeID, reason)
		}
	}
}
