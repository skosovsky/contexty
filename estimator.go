package contexty

import (
	"context"
	"fmt"
)

// TokenEstimator counts tokens in semantic messages (provider-agnostic).
type TokenEstimator interface {
	Estimate(ctx context.Context, msgs []Message) (int, error)
	EstimatePerMessage(ctx context.Context, msgs []Message) ([]int, error)
}

// CharTokenEstimator uses rune count as a deterministic test estimator.
type CharTokenEstimator struct{}

func (CharTokenEstimator) EstimateAccuracy() EstimateQuality { return EstimateEstimated }

func (CharTokenEstimator) EstimateCapabilities() map[EstimateKind]EstimateQuality {
	return legacyEstimateCapabilities()
}

func legacyEstimateCapabilities() map[EstimateKind]EstimateQuality {
	return map[EstimateKind]EstimateQuality{EstimateText: EstimateEstimated,
		EstimateToolCall: EstimateEstimated, EstimateToolResult: EstimateEstimated,
		EstimateImage: EstimateUnknown, EstimateMedia: EstimateUnknown, EstimateExtension: EstimateUnknown}
}

// Estimate returns total rune count across text and tool parts.
func (CharTokenEstimator) Estimate(ctx context.Context, msgs []Message) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("contexty: estimate tokens: %w", err)
	}
	per, err := CharTokenEstimator{}.EstimatePerMessage(ctx, msgs)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, n := range per {
		total += n
	}
	return total, nil
}

// EstimatePerMessage returns per-message rune weights.
func (CharTokenEstimator) EstimatePerMessage(ctx context.Context, msgs []Message) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: estimate per message: %w", err)
	}
	weights := make([]int, len(msgs))
	for i, m := range msgs {
		if err := rejectUnknownMedia(m); err != nil {
			return nil, err
		}
		weights[i] = messageRuneWeight(m)
	}
	return weights, nil
}

func rejectUnknownMedia(message Message) error {
	for _, part := range message.Parts {
		if part != nil && part.partKind() == PartKindMedia {
			return ErrUnknownEstimateCost
		}
	}
	return nil
}

func messageRuneWeight(m Message) int {
	n := 0
	for _, p := range m.Parts {
		switch v := p.(type) {
		case TextPart:
			n += len([]rune(v.Text))
		case ImagePart:
			n += len(v.URL)
		case ToolCallPart:
			n += len(v.Name) + len(v.Arguments.PlainText()) + len(v.ID)
		case ToolResultPart:
			n += len(v.Payload.PlainText()) + len(v.ToolCallID)
		}
	}
	return n
}
