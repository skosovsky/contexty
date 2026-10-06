package contexty

import (
	"context"
	"fmt"
	"unicode/utf8"
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
	return sumEstimateTokens(per)
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
		weight, err := messageRuneWeight(m)
		if err != nil {
			return nil, err
		}
		weights[i] = weight
	}
	return weights, nil
}

func rejectUnknownMedia(message Message) error {
	if err := validateContentParts(message.Parts); err != nil {
		return err
	}
	if err := rejectOpaqueEstimateCost(message); err != nil {
		return err
	}
	for _, part := range message.Parts {
		switch value := canonicalPartValue(part).(type) {
		case MediaPart:
			return ErrUnknownEstimateCost
		case ToolCallPart:
			if len(value.Arguments.Binary) != 0 {
				return ErrUnknownEstimateCost
			}
		case ToolResultPart:
			if len(value.Payload.Binary) != 0 {
				return ErrUnknownEstimateCost
			}
		}
	}
	return nil
}

func rejectOpaqueEstimateCost(message Message) error {
	for _, extension := range message.Extensions {
		if !nilInterfaceValue(extension) && extension.ExtensionType() == OpaqueStateExtensionType {
			return ErrUnknownEstimateCost
		}
	}
	return nil
}

func messageRuneWeight(m Message) (int, error) {
	total := 0
	for _, p := range m.Parts {
		var weights [3]int
		switch v := canonicalPartValue(p).(type) {
		case TextPart:
			weights[0] = utf8.RuneCountInString(v.Text)
		case ImagePart:
			weights[0] = len(v.URL)
		case ToolCallPart:
			weights = [3]int{len(v.Name), len(v.Arguments.PlainText()), len(v.ID)}
		case ToolResultPart:
			weights = [3]int{len(v.Payload.PlainText()), len(v.ToolCallID), 0}
		}
		for _, weight := range weights {
			var err error
			total, err = addEstimateTokens(total, weight)
			if err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}
