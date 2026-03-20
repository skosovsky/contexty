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
// Use for blocks that must never be evicted.
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
// Use for optional blocks where partial content is worse than none.
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

// dropTailStrategy removes messages from the end until the block fits.
type dropTailStrategy struct{}

// NewDropTailStrategy returns a strategy that removes trailing messages one by one until the block fits.
func NewDropTailStrategy() EvictionStrategy {
	return &dropTailStrategy{}
}

func (s *dropTailStrategy) Apply(ctx context.Context, msgs []Message, originalTokens int, limit int, counter TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: drop tail: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	if originalTokens <= limit {
		return msgs, nil
	}

	out := slices.Clone(msgs)
	for len(out) > 1 {
		out = out[:len(out)-1]
		tokens, err := counter.Count(ctx, out)
		if err != nil {
			return nil, fmt.Errorf("contexty: drop tail: %w: %w", ErrTokenCountFailed, err)
		}
		if tokens <= limit {
			return out, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("contexty: drop tail: %w", err)
		}
	}
	return nil, ErrBlockTooLarge
}

// truncateOldestStrategy removes messages from the start until the block fits.
// Starts with total := originalTokens; re-counts remaining slice when trimming (unavoidable for per-message overhead).
type truncateOldestStrategy struct {
	cfg truncateConfig
}

// NewTruncateOldestStrategy returns a strategy that truncates from the oldest messages.
// Options: KeepTurnAtomicity, MinMessages, ProtectRole.
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
	weights, err := counter.CountPerMessage(ctx, msgs)
	if err != nil {
		return nil, fmt.Errorf("contexty: truncate: %w: %w", ErrTokenCountFailed, err)
	}
	if len(weights) != len(msgs) {
		return nil, fmt.Errorf("token counter returned %d weights for %d messages: %w", len(weights), len(msgs), ErrTokenCountFailed)
	}
	// Binary search (suffix-based: "keep from index i") is only valid when we are free to drop
	// any prefix by index. ProtectRole and KeepTurnAtomicity require removing the "first
	// removable" message or atomic tool-turn, which is not the same as cutting at an arbitrary index;
	// using binary search with those options would break their semantics, so we use the
	// sequential path when either option is set.
	if len(s.cfg.protectedRoles) == 0 && !s.cfg.keepTurnAtomicity {
		// Build suffix sums once so binary search can use O(1) lookup per iteration.
		// suffixSum[i] = token count of msgs[i:]
		suffixSum := make([]int, len(weights)+1)
		for i := len(weights) - 1; i >= 0; i-- {
			suffixSum[i] = suffixSum[i+1] + weights[i]
		}
		bestValidIdx := len(msgs)
		low, high := 0, len(msgs)
		for low <= high {
			mid := low + (high-low)/2
			if suffixSum[mid] <= limit {
				bestValidIdx = mid
				high = mid - 1
			} else {
				low = mid + 1
			}
		}
		out := msgs[bestValidIdx:]
		if s.cfg.minMessages > 0 && len(out) < s.cfg.minMessages {
			return nil, nil
		}
		return slices.Clone(out), nil
	}
	var protected map[string]struct{}
	if len(s.cfg.protectedRoles) > 0 {
		protected = make(map[string]struct{}, len(s.cfg.protectedRoles))
		for _, r := range s.cfg.protectedRoles {
			protected[r] = struct{}{}
		}
	}
	cur := slices.Clone(msgs)
	weightsCur := slices.Clone(weights)
	deleted := make([]bool, len(cur))
	total := originalTokens
	searchStart := 0
	for total > limit {
		i := -1
		for j := searchStart; j < len(cur); j++ {
			if deleted[j] {
				continue
			}
			if protected != nil {
				if _, ok := protected[cur[j].Role]; ok {
					continue
				}
			}
			i = j
			break
		}
		if i == -1 {
			break
		}
		endIdx := i
		if s.cfg.keepTurnAtomicity && cur[i].Role == "assistant" && len(cur[i].ToolCalls) > 0 {
			endIdx = s.toolTurnBlockSize(cur, i, deleted)
		}
		for k := i; k <= endIdx && k < len(cur); k++ {
			if !deleted[k] {
				deleted[k] = true
				total -= weightsCur[k]
			}
		}
		searchStart = endIdx + 1
	}
	out := make([]Message, 0, len(cur))
	for idx, m := range cur {
		if !deleted[idx] {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	if s.cfg.minMessages > 0 && len(out) < s.cfg.minMessages {
		return nil, nil
	}
	return out, nil
}

// toolTurnBlockSize returns the last index (inclusive) of the atomic tool-turn block
// starting at startIdx (assistant with ToolCalls). Uses expectedIDs for strict matching
// when ToolCallID is set; falls back to contiguous tool messages on anomaly or empty IDs.
// When deleted is non-nil, skips indices j with deleted[j] when scanning for the block end.
func (s *truncateOldestStrategy) toolTurnBlockSize(cur []Message, startIdx int, deleted []bool) int {
	msg := cur[startIdx]
	expectedIDs := make(map[string]bool)
	for _, tc := range msg.ToolCalls {
		if tc.ID != "" {
			expectedIDs[tc.ID] = true
		}
	}
	expectedCount := len(msg.ToolCalls)
	endIdx := startIdx
	for j := startIdx + 1; j < len(cur); j++ {
		if deleted != nil && deleted[j] {
			continue
		}
		if cur[j].Role != "tool" {
			break
		}
		if cur[j].ToolCallID != "" {
			if !expectedIDs[cur[j].ToolCallID] {
				break
			}
			delete(expectedIDs, cur[j].ToolCallID)
		}
		endIdx = j
		expectedCount--
		if expectedCount <= 0 && len(expectedIDs) == 0 {
			break
		}
	}
	return endIdx
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
		return nil, fmt.Errorf("contexty: summarize: %w: %w", ErrTokenCountFailed, err)
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
	_ EvictionStrategy = (*dropTailStrategy)(nil)
	_ EvictionStrategy = (*truncateOldestStrategy)(nil)
	_ EvictionStrategy = (*summarizeStrategy)(nil)
)
