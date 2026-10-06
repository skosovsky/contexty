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
	return sumEstimateTokens(weights)
}

// EstimatePerMessage returns one token weight per message.
func (c *CharFallbackEstimator) EstimatePerMessage(ctx context.Context, msgs []Message) ([]int, error) {
	if err := validateBuiltinEstimator(c); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nonTextWeight := c.TokensPerNonTextPart
	if nonTextWeight == 0 {
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
		runes, toolTokens, nonTextTokens, err := c.messageComponents(ctx, m, nonTextWeight)
		if err != nil {
			return nil, err
		}

		tokensFromRunes := 0
		if runes > 0 {
			tokensFromRunes = runes / c.CharsPerToken
			if runes%c.CharsPerToken != 0 {
				tokensFromRunes++
			}
		}
		out[i], err = addEstimateTokens(tokensFromRunes, toolTokens)
		if err == nil {
			out[i], err = addEstimateTokens(out[i], nonTextTokens)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

var _ TokenEstimator = (*CharFallbackEstimator)(nil)

func (c *CharFallbackEstimator) messageComponents(
	ctx context.Context,
	message Message,
	nonTextWeight int,
) (int, int, int, error) {
	var runes, tools, nonText int
	for _, part := range message.Parts {
		partRunes, partTools, partNonText, err := c.partComponents(ctx, canonicalPartValue(part), nonTextWeight)
		if err != nil {
			return 0, 0, 0, err
		}
		runes, err = addEstimateTokens(runes, partRunes)
		if err != nil {
			return 0, 0, 0, err
		}
		tools, err = addEstimateTokens(tools, partTools)
		if err != nil {
			return 0, 0, 0, err
		}
		nonText, err = addEstimateTokens(nonText, partNonText)
		if err != nil {
			return 0, 0, 0, err
		}
	}
	return runes, tools, nonText, nil
}

func (c *CharFallbackEstimator) partComponents(
	ctx context.Context,
	part ContentPart,
	nonTextWeight int,
) (int, int, int, error) {
	switch value := part.(type) {
	case TextPart:
		return utf8.RuneCountInString(value.Text), 0, 0, nil
	case ImagePart:
		return 0, 0, nonTextWeight, nil
	case ToolCallPart:
		return c.toolComponents(ctx, value)
	case ToolResultPart:
		return utf8.RuneCountInString(value.Payload.PlainText()), 0, 0, nil
	default:
		return 0, 0, 0, ErrUnknownEstimateCost
	}
}

func (c *CharFallbackEstimator) toolComponents(ctx context.Context, call ToolCallPart) (int, int, int, error) {
	if c.EstimateTool == nil {
		runes, err := addEstimateTokens(
			utf8.RuneCountInString(call.Arguments.PlainText()),
			utf8.RuneCountInString(call.Name),
		)
		return runes, ToolCallOverhead, 0, err
	}
	owned, ok := call.clonePart().(ToolCallPart)
	if !ok {
		return 0, 0, 0, ErrInvalidContentPart
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, 0, err
	}
	weight := c.EstimateTool(owned)
	if canceled := ctx.Err(); canceled != nil {
		return 0, 0, 0, canceled
	}
	tokens, err := addEstimateTokens(weight, ToolCallOverhead)
	return 0, tokens, 0, err
}
