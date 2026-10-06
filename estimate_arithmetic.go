package contexty

import "context"

func sumEstimateTokens(weights []int) (int, error) {
	var total int
	for _, weight := range weights {
		var err error
		total, err = addEstimateTokens(total, weight)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func multiplyEstimateTokens(a, b int) (int, error) {
	if a < 0 || b < 0 || (a != 0 && b > int(^uint(0)>>1)/a) {
		return 0, ErrInconsistentEstimate
	}
	return a * b, nil
}

func estimateOwned(ctx context.Context, estimator TokenEstimator, messages []Message) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if nilInterfaceValue(estimator) {
		return 0, ErrInvalidBudgetRequest
	}
	total, err := estimator.Estimate(ctx, estimatorCallbackInput(estimator, messages))
	if canceled := ctx.Err(); canceled != nil {
		return 0, canceled
	}
	if err != nil {
		return 0, err
	}
	if total < 0 {
		return 0, ErrInconsistentEstimate
	}
	return total, nil
}
