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
