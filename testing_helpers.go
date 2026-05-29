package contexty

import (
	"context"
	"errors"
	"fmt"
)

// FixedEstimator returns deterministic token counts for tests.
type FixedEstimator struct {
	TokensPerMessage     int
	TokensPerContentPart int
	TokensPerToolCall    int
}

// Estimate returns total token weight.
func (c *FixedEstimator) Estimate(ctx context.Context, msgs []Message) (int, error) {
	weights, err := c.EstimatePerMessage(ctx, msgs)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, w := range weights {
		total += w
	}
	return total, nil
}

// EstimatePerMessage returns per-message weights.
func (c *FixedEstimator) EstimatePerMessage(ctx context.Context, msgs []Message) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: fixed estimator: %w", err)
	}
	out := make([]int, len(msgs))
	for i, m := range msgs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("contexty: fixed estimator: %w", err)
		}
		w := c.TokensPerMessage
		if c.TokensPerContentPart != 0 {
			w += len(m.Parts) * c.TokensPerContentPart
		}
		if c.TokensPerToolCall != 0 {
			w += len(m.ToolCallParts()) * c.TokensPerToolCall
		}
		out[i] = w
	}
	return out, nil
}

var _ TokenEstimator = (*FixedEstimator)(nil)

// FailingEstimator always returns Err from Estimate calls (tests).
type FailingEstimator struct {
	Err error
}

// Estimate returns the configured error.
func (f *FailingEstimator) Estimate(context.Context, []Message) (int, error) {
	if f.Err == nil {
		return 0, errors.New("estimate failed")
	}
	return 0, f.Err
}

// EstimatePerMessage returns the configured error.
func (f *FailingEstimator) EstimatePerMessage(context.Context, []Message) ([]int, error) {
	if f.Err == nil {
		return nil, errors.New("estimate failed")
	}
	return nil, f.Err
}

var _ TokenEstimator = (*FailingEstimator)(nil)
