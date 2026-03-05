package contexty

import (
	"context"
	"fmt"
	"slices"
)

// strictStrategy returns an error if the block does not fit; otherwise passes through.
// DRY: uses originalTokens only, no counter.Count call.
type strictStrategy struct{}

// NewStrictStrategy returns a strategy that fails with ErrBudgetExceeded when the block exceeds the limit.
// Use for TierSystem and other blocks that must never be evicted.
func NewStrictStrategy() EvictionStrategy {
	return &strictStrategy{}
}

func (s *strictStrategy) Apply(ctx context.Context, msgs []Message, originalTokens int, limit int, _ TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: strict: %w", err)
	}
	if originalTokens > limit {
		return nil, ErrBudgetExceeded
	}
	return msgs, nil
}

// dropStrategy removes the entire block when it does not fit.
// DRY: uses originalTokens only, no counter.Count call.
type dropStrategy struct{}

// NewDropStrategy returns a strategy that drops the block entirely when it exceeds the limit.
// Use for RAG or other optional blocks where partial content is worse than none.
func NewDropStrategy() EvictionStrategy {
	return &dropStrategy{}
}

func (s *dropStrategy) Apply(ctx context.Context, msgs []Message, originalTokens int, limit int, _ TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: drop: %w", err)
	}
	if originalTokens > limit {
		return nil, nil
	}
	return msgs, nil
}

// truncateOldestStrategy removes messages from the start until the block fits.
// Starts with total := originalTokens; re-counts remaining slice when trimming (unavoidable for per-message overhead).
type truncateOldestStrategy struct {
	cfg truncateConfig
}

// NewTruncateOldestStrategy returns a strategy that truncates from the oldest messages.
// Options: KeepUserAssistantPairs, MinMessages, ProtectRole.
func NewTruncateOldestStrategy(opts ...TruncateOption) EvictionStrategy {
	cfg := truncateConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &truncateOldestStrategy{cfg: cfg}
}

func (s *truncateOldestStrategy) Apply(ctx context.Context, msgs []Message, originalTokens int, limit int, counter TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: truncate: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	if originalTokens <= limit {
		return msgs, nil
	}
	// Binary search (suffix-based: "keep from index i") is only valid when we are free to drop
	// any prefix by index. ProtectRole and KeepUserAssistantPairs require removing the "first
	// removable" message or pair, which is not the same as cutting at an arbitrary index;
	// using binary search with those options would break their semantics, so we keep the
	// sequential path when either option is set.
	if len(s.cfg.protectedRoles) == 0 && !s.cfg.keepPairs {
		cur := slices.Clone(msgs)
		bestValidIdx := len(cur)
		low, high := 0, len(cur)
		for low <= high {
			mid := low + (high-low)/2
			count, err := counter.Count(ctx, cur[mid:])
			if err != nil {
				return nil, fmt.Errorf("contexty: truncate: %w: %w", ErrTokenCountFailed, err)
			}
			if count <= limit {
				bestValidIdx = mid
				high = mid - 1
			} else {
				low = mid + 1
			}
		}
		out := cur[bestValidIdx:]
		if s.cfg.minMessages > 0 && len(out) < s.cfg.minMessages {
			return nil, nil
		}
		return out, nil
	}
	var protected map[string]struct{}
	if len(s.cfg.protectedRoles) > 0 {
		protected = make(map[string]struct{}, len(s.cfg.protectedRoles))
		for _, r := range s.cfg.protectedRoles {
			protected[r] = struct{}{}
		}
	}
	cur := slices.Clone(msgs)
	total := originalTokens
	maxIterations := len(cur)
	for iter := 0; iter < maxIterations && total > limit && len(cur) > 0; iter++ {
		// Find first removable index (skip protected roles from the start).
		i := 0
		for i < len(cur) && protected != nil {
			if _, ok := protected[cur[i].Role]; !ok {
				break
			}
			i++
		}
		if i >= len(cur) {
			break // all remaining messages are protected
		}
		remove := 1
		if s.cfg.keepPairs && i+1 < len(cur) && cur[i].Role == "user" && cur[i+1].Role == "assistant" {
			remove = 2
		}
		cur = append(cur[:i], cur[i+remove:]...)
		if len(cur) == 0 {
			return nil, nil
		}
		var err error
		total, err = counter.Count(ctx, cur)
		if err != nil {
			return nil, fmt.Errorf("contexty: truncate: %w", err)
		}
		if total > originalTokens {
			return nil, fmt.Errorf("contexty: truncate: %w: count exceeded original", ErrTokenCountFailed)
		}
	}
	if len(cur) == 0 {
		return nil, nil
	}
	if s.cfg.minMessages > 0 && len(cur) < s.cfg.minMessages {
		return nil, nil
	}
	return cur, nil
}

// summarizeStrategy compresses the block via a Summarizer when it does not fit.
// DRY: uses originalTokens for initial check; re-counts only the summary result.
type summarizeStrategy struct {
	summarizer Summarizer
}

// NewSummarizeStrategy returns a strategy that calls the given Summarizer when the block exceeds the limit.
// If the summary still does not fit, the block is dropped (empty result).
// Panics if summarizer is nil (programmer error at init time).
func NewSummarizeStrategy(summarizer Summarizer) EvictionStrategy {
	if summarizer == nil {
		panic("contexty: NewSummarizeStrategy called with nil Summarizer")
	}
	return &summarizeStrategy{summarizer: summarizer}
}

func (s *summarizeStrategy) Apply(ctx context.Context, msgs []Message, originalTokens int, limit int, counter TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: summarize: %w", err)
	}
	if originalTokens <= limit {
		return msgs, nil
	}
	summary, err := s.summarizer.Summarize(ctx, msgs)
	if err != nil {
		return nil, fmt.Errorf("contexty: summarize: %w", err)
	}
	summaryTokens, err := counter.Count(ctx, []Message{summary})
	if err != nil {
		return nil, fmt.Errorf("contexty: summarize: %w", err)
	}
	if summaryTokens > limit {
		return nil, nil
	}
	return []Message{summary}, nil
}

// Compile-time checks.
var (
	_ EvictionStrategy = (*strictStrategy)(nil)
	_ EvictionStrategy = (*dropStrategy)(nil)
	_ EvictionStrategy = (*truncateOldestStrategy)(nil)
	_ EvictionStrategy = (*summarizeStrategy)(nil)
)
