package contexty_test

import (
	"context"
	"errors"

	"github.com/skosovsky/contexty"
)

type stubSummarizer func(context.Context, contexty.SummaryRequest) (contexty.Message, error)

func (f stubSummarizer) Summarize(ctx context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
	return f(ctx, request)
}

// callCountEstimator fails after whole-request budget application and mandatory final
// validation, so only the passive telemetry estimate fails.
type callCountEstimator struct {
	calls int
}

func (c *callCountEstimator) Estimate(context.Context, []contexty.Message) (int, error) {
	c.calls++
	if c.calls > 2 {
		return 0, errors.New("telemetry estimate failed")
	}
	return 50, nil
}

func (c *callCountEstimator) EstimatePerMessage(ctx context.Context, msgs []contexty.Message) ([]int, error) {
	total, err := c.Estimate(ctx, msgs)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	per := total / len(msgs)
	out := make([]int, len(msgs))
	for i := range out {
		out[i] = per
	}
	return out, nil
}
