package contexty

import (
	"context"
	"unicode/utf8"
)

// DefaultTokensPerNonTextPart is the fallback token count for non-text parts.
const DefaultTokensPerNonTextPart = 85

// ToolCallOverhead is pessimistic token overhead per tool call part.
const ToolCallOverhead = 20

// ToolCallPartEstimator returns token weight for a ToolCallPart.
type ToolCallPartEstimator func(call ToolCallPart) int

// CharFallbackEstimator approximates token count by character ratio.
type CharFallbackEstimator struct {
	CharsPerToken        int
	TokensPerNonTextPart int
	EstimateTool         ToolCallPartEstimator
}

func (*CharFallbackEstimator) EstimateAccuracy() EstimateQuality { return EstimateEstimated }

func (*CharFallbackEstimator) EstimateCapabilities() map[EstimateKind]EstimateQuality {
	return legacyEstimateCapabilities()
}

// Estimate returns estimated token count for all messages.
func (c *CharFallbackEstimator) Estimate(ctx context.Context, msgs []Message) (int, error) {
	weights, err := c.EstimatePerMessage(ctx, msgs)
	if err != nil {
		return 0, err
	}
	var sum int
	for _, w := range weights {
		sum += w
	}
	return sum, nil
}

// EstimatePerMessage returns one token weight per message.
func (c *CharFallbackEstimator) EstimatePerMessage(ctx context.Context, msgs []Message) ([]int, error) {
	if c.CharsPerToken <= 0 {
		return nil, ErrInvalidCharsPerToken
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nonTextWeight := c.TokensPerNonTextPart
	if nonTextWeight <= 0 {
		nonTextWeight = DefaultTokensPerNonTextPart
	}
	out := make([]int, len(msgs))
	for i, m := range msgs {
		if err := rejectUnknownMedia(m); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var runes int
		var toolTokens int
		for _, p := range m.Parts {
			switch v := p.(type) {
			case TextPart:
				runes += utf8.RuneCountInString(v.Text)
			case ImagePart:
				runes += nonTextWeight * c.CharsPerToken
			case ToolCallPart:
				if c.EstimateTool != nil {
					toolTokens += c.EstimateTool(v) + ToolCallOverhead
				} else {
					runes += utf8.RuneCountInString(v.Arguments.PlainText()) + utf8.RuneCountInString(v.Name)
					toolTokens += ToolCallOverhead
				}
			case ToolResultPart:
				runes += utf8.RuneCountInString(v.Payload.PlainText())
			}
		}
		tokensFromRunes := 0
		if runes > 0 {
			tokensFromRunes = (runes + c.CharsPerToken - 1) / c.CharsPerToken
		}
		out[i] = tokensFromRunes + toolTokens
	}
	return out, nil
}

var _ TokenEstimator = (*CharFallbackEstimator)(nil)
