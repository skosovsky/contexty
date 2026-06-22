package contexty

import (
	"context"
	"fmt"
)

func (p *BudgetPipeline) estimateBudgetMessages(ctx context.Context, messages []Message) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	tokens, err := p.estimator.Estimate(ctx, estimatorCallbackInput(p.estimator, messages))
	if canceled := ctx.Err(); canceled != nil {
		return 0, canceled
	}
	if err != nil {
		return 0, fmt.Errorf("contexty: budget: %w: %w", ErrTokenCountFailed, err)
	}
	if tokens < 0 {
		return 0, ErrBudgetExceeded
	}
	return tokens, nil
}

func (p *BudgetPipeline) applyProtectedSuffix(
	ctx context.Context,
	messages []Message,
	start, limit int,
) ([]Message, error) {
	suffix := cloneMessageSlice(messages[start:])
	reserved, err := p.estimateBudgetMessages(ctx, suffix)
	if err != nil {
		return nil, err
	}
	if reserved > limit {
		if p.rolling != nil {
			return nil, ErrRecentTailExceedsBudget
		}
		return nil, ErrPendingExceedsBudget
	}
	prefix, err := p.compressProtectedPrefix(ctx, messages[:start], messages, limit-reserved)
	if err != nil {
		return nil, err
	}
	out := append(cloneMessageSlice(prefix), suffix...)
	cost, err := p.estimateBudgetMessages(ctx, out)
	if err != nil {
		return nil, err
	}
	if cost > limit {
		return nil, ErrBudgetExceeded
	}
	if _, err = InspectToolRoundStates(out, nil); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *BudgetPipeline) compressProtectedPrefix(
	ctx context.Context,
	prefix, all []Message,
	limit int,
) ([]Message, error) {
	if len(prefix) == 0 {
		return nil, nil
	}
	prefix = cloneMessageSlice(prefix)
	cost, err := p.estimateBudgetMessages(ctx, prefix)
	if err != nil {
		return nil, err
	}
	if p.rolling != nil {
		return p.summarizeRollingPrefix(ctx, prefix, all, cost, limit)
	}
	if cost > limit {
		return p.applyEvictable(ctx, prefix, cost, limit)
	}
	return prefix, nil
}

func (p *BudgetPipeline) summarizeRollingPrefix(
	ctx context.Context,
	prefix, all []Message,
	cost, limit int,
) ([]Message, error) {
	output, summaryCost, err := p.summarize(ctx, prefix, cost, limit)
	if err != nil {
		return nil, err
	}
	if summaryCost < 0 || summaryCost > limit {
		return nil, ErrBudgetExceeded
	}
	if err = validateEvictableRounds(output); err != nil {
		return nil, err
	}
	if err = validateRollingSummaryIdentity(output[0], all); err != nil {
		return nil, err
	}
	return output, nil
}

// No pending round exists in an evictable prefix; creating one would split a
// formerly complete round or manufacture an unresolved call during summarization.
func validateEvictableRounds(messages []Message) error {
	rounds, err := InspectToolRoundStates(messages, nil)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if round.State != ToolRoundComplete {
			return ErrInvalidToolRound
		}
	}
	return nil
}
