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
	_ TokenEstimator,
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
	_ TokenEstimator,
) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: drop: %w", err)
	}
	if originalTokens > limit {
		reportEvictions(ctx, msgs, nil, EvictionReasonBudget)
		return nil, nil
	}
	return msgs, nil
}

// dropTailStrategy removes messages from the end until the block fits.
// Tool-turn blocks are dropped atomically from the tail.
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
	estimator TokenEstimator,
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
		out = dropTailAtomicUnit(out)
		tokens, err := estimateOwned(ctx, estimator, out)
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		if err != nil {
			return nil, fmt.Errorf("contexty: drop tail: %w: %w", ErrTokenCountFailed, err)
		}
		if tokens <= limit {
			reportEvictions(ctx, msgs, out, EvictionReasonTruncate)
			return out, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("contexty: drop tail: %w", err)
		}
	}
	reportEvictions(ctx, msgs, nil, EvictionReasonTruncate)
	return nil, ErrBlockTooLarge
}

// dropTailAtomicUnit removes one trailing message or an entire tool-turn block from the end.
func dropTailAtomicUnit(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	last := len(msgs) - 1
	if msgs[last].Role != RoleTool {
		return msgs[:last]
	}
	return msgs[:toolRoundStartForTail(msgs)]
}

// dropHeadStrategy removes older messages from the front until the block fits.
type dropHeadStrategy struct {
	cfg DropHeadConfig
}

// NewDropHeadStrategy returns a strategy that trims older messages from the front.
// Empty config enables tool-turn atomicity by default.
func NewDropHeadStrategy(cfg DropHeadConfig) EvictionStrategy {
	return &dropHeadStrategy{cfg: cfg.normalized()}
}

func (s *dropHeadStrategy) Apply(
	ctx context.Context,
	msgs []Message,
	originalTokens int,
	limit int,
	counter TokenEstimator,
) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: drop head: %w", err)
	}
	if nilInterfaceValue(counter) || s.cfg.MinMessages < 0 || originalTokens < 0 || limit < 0 {
		return nil, ErrInvalidBudgetRequest
	}
	if err := validateBuiltinEstimator(counter); err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	if originalTokens <= limit {
		return msgs, nil
	}
	weights, err := counter.EstimatePerMessage(ctx, estimatorCallbackInput(counter, msgs))
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
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	weights = slices.Clone(weights)
	sum, err := sumEstimateTokens(weights)
	if err != nil {
		return nil, err
	}
	if sum != originalTokens {
		return nil, ErrInconsistentEstimate
	}
	if s.cfg.MinMessages < 0 {
		return nil, ErrInvalidBudgetRequest
	}
	if s.usesFastPath() && intrinsicAdditiveEstimator(counter) {
		out := s.applyFastPath(msgs, weights, limit)
		reportEvictions(ctx, msgs, out, EvictionReasonTruncate)
		return out, nil
	}
	out, err := s.applySelectivePath(ctx, dropHeadState{
		msgs:    slices.Clone(msgs),
		weights: slices.Clone(weights),
		deleted: make([]bool, len(msgs)),
		total:   originalTokens,
	}, limit, counter)
	if err != nil {
		return nil, err
	}
	reportEvictions(ctx, msgs, out, EvictionReasonTruncate)
	return out, nil
}

type dropHeadState struct {
	msgs        []Message
	weights     []int
	deleted     []bool
	total       int
	searchStart int
}

func (s *dropHeadStrategy) usesFastPath() bool {
	return !s.cfg.keepTurnAtomicity()
}

func (s *dropHeadStrategy) applyFastPath(msgs []Message, weights []int, limit int) []Message {
	// Binary search (suffix-based: "keep from index i") is only valid when we are free to
	// drop any prefix by index. KeepTurnAtomicity requires removing the
	// first droppable message or atomic tool-turn instead of an arbitrary prefix.
	suffixSum := make([]int, len(weights)+1)
	for i, weight := range slices.Backward(weights) {
		suffixSum[i] = suffixSum[i+1] + weight
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

func (s *dropHeadStrategy) applySelectivePath(
	ctx context.Context,
	state dropHeadState,
	limit int,
	counter TokenEstimator,
) ([]Message, error) {
	for state.total > limit {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("contexty: drop head: %w", err)
		}
		startIdx := s.findFirstDroppableIndex(state)
		if startIdx == -1 {
			break
		}
		endIdx := startIdx
		if s.cfg.keepTurnAtomicity() && state.msgs[startIdx].Role == RoleAssistant &&
			state.msgs[startIdx].HasToolCalls() {
			endIdx = s.toolTurnEndIndex(state.msgs, startIdx, state.deleted)
		}
		state.removeUnit(startIdx, endIdx)
		state.searchStart = endIdx + 1
		if !intrinsicAdditiveEstimator(counter) {
			candidate := retainedDropHeadMessages(state)
			var err error
			state.total, err = estimateOwned(ctx, counter, candidate)
			if err != nil {
				return nil, err
			}
		}
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

func (s *dropHeadStrategy) findFirstDroppableIndex(state dropHeadState) int {
	for idx := state.searchStart; idx < len(state.msgs); idx++ {
		if !state.deleted[idx] {
			return idx
		}
	}
	return -1
}

// toolTurnEndIndex returns the last index (inclusive) of the atomic tool-turn block.
func (s *dropHeadStrategy) toolTurnEndIndex(cur []Message, startIdx int, deleted []bool) int {
	return activeToolRoundEndIndex(cur, startIdx, deleted)
}

// Compile-time checks.
var (
	_ EvictionStrategy = (*strictStrategy)(nil)
	_ EvictionStrategy = (*dropStrategy)(nil)
	_ EvictionStrategy = (*dropTailStrategy)(nil)
	_ EvictionStrategy = (*dropHeadStrategy)(nil)
)

func retainedDropHeadMessages(state dropHeadState) []Message {
	out := make([]Message, 0, len(state.msgs))
	for index, message := range state.msgs {
		if !state.deleted[index] {
			out = append(out, message)
		}
	}
	return out
}

func intrinsicAdditiveEstimator(estimator TokenEstimator) bool {
	switch value := estimator.(type) {
	case CharTokenEstimator, *FixedEstimator:
		return true
	case *CharFallbackEstimator:
		return value != nil && value.EstimateTool == nil
	default:
		return false
	}
}

func (state *dropHeadState) removeUnit(start, end int) {
	for index := start; index <= end && index < len(state.msgs); index++ {
		if state.deleted[index] {
			continue
		}
		state.deleted[index] = true
		state.total -= state.weights[index]
	}
}
