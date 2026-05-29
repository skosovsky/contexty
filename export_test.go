package contexty

import (
	"context"
	"sync"
	"time"
)

// WithBudgetObservationForTest wires observer telemetry for unit tests.
func WithBudgetObservationForTest(ctx context.Context, obs Observer, blockID string) context.Context {
	return withBudgetObservation(ctx, obs, blockID)
}

// RecordingObserver captures observer events for tests.
type RecordingObserver struct {
	mu sync.Mutex

	Tokens       []TokenEstimatedEvent
	Evictions    []NodeEvictedEvent
	Summaries    []float64
	Compilations []PipelineCompiledEvent
}

type TokenEstimatedEvent struct {
	BlockID string
	Count   int
	Ctx     context.Context
}

type NodeEvictedEvent struct {
	NodeID string
	Reason EvictionReason
	Ctx    context.Context
}

type PipelineCompiledEvent struct {
	TotalCost int
	Duration  time.Duration
	Ctx       context.Context
}

func (r *RecordingObserver) OnTokensEstimated(ctx context.Context, blockID string, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Tokens = append(r.Tokens, TokenEstimatedEvent{BlockID: blockID, Count: count, Ctx: ctx})
}

func (r *RecordingObserver) OnNodeEvicted(ctx context.Context, nodeID string, reason EvictionReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Evictions = append(r.Evictions, NodeEvictedEvent{NodeID: nodeID, Reason: reason, Ctx: ctx})
}

func (r *RecordingObserver) OnContextSummarized(ctx context.Context, compressionRatio float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Summaries = append(r.Summaries, compressionRatio)
	_ = ctx
}

func (r *RecordingObserver) OnPipelineCompiled(ctx context.Context, totalCost int, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Compilations = append(r.Compilations, PipelineCompiledEvent{TotalCost: totalCost, Duration: duration, Ctx: ctx})
}

func (r *RecordingObserver) EvictionNodeIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.Evictions))
	for i, e := range r.Evictions {
		out[i] = e.NodeID
	}
	return out
}
