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

func (s *strictStrategy) Apply(
	ctx context.Context,
	msgs []Message,
	originalTokens int,
	limit int,
	_ TokenCounter,
) ([]Message, error) {
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

func (s *dropStrategy) Apply(
	ctx context.Context,
	msgs []Message,
	originalTokens int,
	limit int,
	_ TokenCounter,
) ([]Message, error) {
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

func (s *dropTailStrategy) Apply(
	ctx context.Context,
	msgs []Message,
	originalTokens int,
	limit int,
	counter TokenCounter,
) ([]Message, error) {
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

// dropHeadStrategy removes older messages from the front until the block fits.
type dropHeadStrategy struct {
	cfg DropHeadConfig
}

// NewDropHeadStrategy returns a strategy that trims older messages from the front.
func NewDropHeadStrategy(cfg DropHeadConfig) EvictionStrategy {
	return &dropHeadStrategy{cfg: cfg.normalized()}
}

func (s *dropHeadStrategy) Apply(
	ctx context.Context,
	msgs []Message,
	originalTokens int,
	limit int,
	counter TokenCounter,
) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: drop head: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	if originalTokens <= limit {
		return msgs, nil
	}
	weights, err := counter.CountPerMessage(ctx, msgs)
	if err != nil {
		return nil, fmt.Errorf("contexty: drop head: %w: %w", ErrTokenCountFailed, err)
	}
	if len(weights) != len(msgs) {
		return nil, fmt.Errorf(
			"token counter returned %d weights for %d messages: %w",
			len(weights),
			len(msgs),
			ErrTokenCountFailed,
		)
	}
	if s.usesFastPath() {
		return s.applyFastPath(msgs, weights, limit), nil
	}
	return s.applySelectivePath(ctx, dropHeadState{
		msgs:    slices.Clone(msgs),
		weights: slices.Clone(weights),
		deleted: make([]bool, len(msgs)),
		total:   originalTokens,
	}, limit)
}

type dropHeadState struct {
	msgs        []Message
	weights     []int
	deleted     []bool
	total       int
	searchStart int
}

func (s *dropHeadStrategy) usesFastPath() bool {
	return len(s.cfg.ProtectedRoles) == 0 && !s.cfg.KeepTurnAtomicity
}

func (s *dropHeadStrategy) applyFastPath(msgs []Message, weights []int, limit int) []Message {
	// Binary search (suffix-based: "keep from index i") is only valid when we are free to
	// drop any prefix by index. ProtectedRoles and KeepTurnAtomicity require removing the
	// first droppable message or atomic tool-turn instead of an arbitrary prefix.
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
	return s.enforceMinMessages(slices.Clone(msgs[bestValidIdx:]))
}

func (s *dropHeadStrategy) applySelectivePath(ctx context.Context, state dropHeadState, limit int) ([]Message, error) {
	protected := s.protectedRoleSet()
	for state.total > limit {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("contexty: drop head: %w", err)
		}
		startIdx := s.findFirstDroppableIndex(state, protected)
		if startIdx == -1 {
			break
		}
		endIdx := startIdx
		if s.cfg.KeepTurnAtomicity && state.msgs[startIdx].Role == RoleAssistant &&
			len(state.msgs[startIdx].ToolCalls) > 0 {
			endIdx = s.toolTurnEndIndex(state.msgs, startIdx, state.deleted)
		}
		for idx := startIdx; idx <= endIdx && idx < len(state.msgs); idx++ {
			if state.deleted[idx] {
				continue
			}
			state.deleted[idx] = true
			state.total -= state.weights[idx]
		}
		state.searchStart = endIdx + 1
	}
	if state.total > limit {
		return nil, nil
	}
	out := make([]Message, 0, len(state.msgs))
	for idx, msg := range state.msgs {
		if !state.deleted[idx] {
			out = append(out, msg)
		}
	}
	return s.enforceMinMessages(out), nil
}

func (s *dropHeadStrategy) enforceMinMessages(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}
	if s.cfg.MinMessages > 0 && len(msgs) < s.cfg.MinMessages {
		return nil
	}
	return msgs
}

func (s *dropHeadStrategy) protectedRoleSet() map[string]struct{} {
	if len(s.cfg.ProtectedRoles) == 0 {
		return nil
	}
	protected := make(map[string]struct{}, len(s.cfg.ProtectedRoles))
	for _, role := range s.cfg.ProtectedRoles {
		protected[role] = struct{}{}
	}
	return protected
}

func (s *dropHeadStrategy) findFirstDroppableIndex(state dropHeadState, protected map[string]struct{}) int {
	for idx := state.searchStart; idx < len(state.msgs); idx++ {
		if state.deleted[idx] {
			continue
		}
		if protected != nil {
			if _, ok := protected[state.msgs[idx].Role]; ok {
				continue
			}
		}
		return idx
	}
	return -1
}

// toolTurnEndIndex returns the last index (inclusive) of the atomic tool-turn block
// starting at startIdx (assistant with ToolCalls). Uses expectedIDs for strict matching
// when ToolCallID is set; falls back to contiguous tool messages on anomaly or empty IDs.
// When deleted is non-nil, skips indices j with deleted[j] when scanning for the block end.
func (s *dropHeadStrategy) toolTurnEndIndex(cur []Message, startIdx int, deleted []bool) int {
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
		if cur[j].Role != RoleTool {
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

func (s *summarizeStrategy) Apply(
	ctx context.Context,
	msgs []Message,
	originalTokens int,
	limit int,
	counter TokenCounter,
) ([]Message, error) {
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
	_ EvictionStrategy = (*dropHeadStrategy)(nil)
	_ EvictionStrategy = (*summarizeStrategy)(nil)
)
