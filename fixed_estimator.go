package contexty

import (
	"context"
	"fmt"
)

// FixedEstimator assigns deterministic token weights to messages, content parts and tool calls.
// Its configuration participates in recording identity; these weights are estimates, not model token counts.
type FixedEstimator struct {
	TokensPerMessage     int
	TokensPerContentPart int
	TokensPerToolCall    int
}

func (*FixedEstimator) EstimateAccuracy() EstimateQuality { return EstimateEstimated }

func (*FixedEstimator) EstimateCapabilities() map[EstimateKind]EstimateQuality {
	return legacyEstimateCapabilities()
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
