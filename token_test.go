package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestCharFallbackEstimator(t *testing.T) {
	estimator := &contexty.CharFallbackEstimator{CharsPerToken: 2}
	msgs := []contexty.Message{{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.TextPart{Text: "ab"},
			contexty.ToolCallPart{Name: "x", Arguments: contexty.TextPayload("yz")},
		},
	}}
	n, err := estimator.Estimate(context.Background(), msgs)
	require.NoError(t, err)
	assert.Positive(t, n)
}

func TestFixedEstimator_ToolParts(t *testing.T) {
	estimator := &contexty.FixedEstimator{TokensPerMessage: 1, TokensPerToolCall: 5}
	msgs := []contexty.Message{{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{ID: "1", Name: "f", Arguments: contexty.JSONPayload("{}")},
		},
	}}
	per, err := estimator.EstimatePerMessage(context.Background(), msgs)
	require.NoError(t, err)
	assert.Equal(t, 6, per[0])
}
