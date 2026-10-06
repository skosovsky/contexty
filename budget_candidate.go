package contexty

import (
	"context"
	"slices"
)

// Built-in eviction measures the complete candidate, including retained messages.
// Host strategies remain responsible for their optional block; final admission
// always verifies their composed output with the original estimator.
func (p *BudgetPipeline) evictCompleteCandidate(
	ctx context.Context, before []Message, required []bool, limit int,
) ([]Message, bool, error) {
	strategy := p.cfg.TruncateStrategy
	if strategy == nil {
		strategy = NewDropHeadStrategy(p.cfg.DropHead)
	}
	var tail, dropAll, strict bool
	minimum := 0
	keepAtomic := true
	switch value := strategy.(type) {
	case *dropHeadStrategy:
		minimum = value.cfg.MinMessages
		keepAtomic = value.cfg.keepTurnAtomicity()
	case *dropTailStrategy:
		tail = true
	case *dropStrategy:
		dropAll = true
	case *strictStrategy:
		strict = true
	default:
		return nil, false, nil
	}
	if strict {
		return nil, true, ErrBudgetExceeded
	}
	cur := cloneMessageSlice(before)
	selected := append([]bool(nil), required...)
	for {
		if err := ctx.Err(); err != nil {
			return nil, true, err
		}
		index := firstOptionalIndex(selected, tail)
		if index < 0 {
			return nil, true, p.requiredOverflowError(before)
		}
		start, end := budgetCandidateUnit(cur, index, tail, keepAtomic)
		cur, selected = removeBudgetCandidate(cur, selected, start, end, dropAll)
		if minimum > 0 && optionalCount(selected) < minimum {
			cur, selected = removeBudgetCandidate(cur, selected, 0, len(cur)-1, true)
		}
		cost, err := p.estimateBudgetMessages(ctx, cur)
		if err != nil {
			return nil, true, err
		}
		if cost <= limit {
			reportEvictions(ctx, before, cur, EvictionReasonTruncate)
			return cur, true, ctx.Err()
		}
	}
}

func firstOptionalIndex(required []bool, tail bool) int {
	if tail {
		for index, selected := range slices.Backward(required) {
			if !selected {
				return index
			}
		}
	}
	for index, selected := range required {
		if !selected {
			return index
		}
	}
	return -1
}

func optionalCount(required []bool) int {
	count := 0
	for _, selected := range required {
		if !selected {
			count++
		}
	}
	return count
}

func removeBudgetCandidate(messages []Message, required []bool, start, end int, all bool) ([]Message, []bool) {
	out := make([]Message, 0, len(messages))
	selected := make([]bool, 0, len(required))
	for index, message := range messages {
		if !required[index] && (all || (index >= start && index <= end)) {
			continue
		}
		out = append(out, message)
		selected = append(selected, required[index])
	}
	return out, selected
}

func budgetCandidateUnit(messages []Message, index int, tail, keepAtomic bool) (int, int) {
	start, end := index, index
	if tail && messages[index].Role == RoleTool {
		start = toolRoundStartForTail(messages[:index+1])
	}
	if keepAtomic && messages[start].HasToolCalls() {
		end = toolRoundEndIndex(messages, start)
	}
	return start, end
}
