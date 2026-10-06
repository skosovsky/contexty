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
	return sumEstimateTokens(weights)
}

// EstimatePerMessage returns per-message weights.
func (c *FixedEstimator) EstimatePerMessage(ctx context.Context, msgs []Message) ([]int, error) {
	if err := validateBuiltinEstimator(c); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: fixed estimator: %w", err)
	}
	out := make([]int, len(msgs))
	for i, m := range msgs {
		if err := validateContentParts(m.Parts); err != nil {
			return nil, err
		}
		if err := rejectOpaqueEstimateCost(m); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("contexty: fixed estimator: %w", err)
		}
		partCost, err := multiplyEstimateTokens(len(m.Parts), c.TokensPerContentPart)
		if err != nil {
			return nil, err
		}
		toolCost, err := multiplyEstimateTokens(len(m.ToolCallParts()), c.TokensPerToolCall)
		if err != nil {
			return nil, err
		}
		w, err := addEstimateTokens(c.TokensPerMessage, partCost)
		if err != nil {
			return nil, err
		}
		w, err = addEstimateTokens(w, toolCost)
		if err != nil {
			return nil, err
		}
		out[i] = w
	}
	return out, nil
}

var _ TokenEstimator = (*FixedEstimator)(nil)
