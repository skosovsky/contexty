package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestCharFallbackEstimator(t *testing.T) {
	// Arrange.
	estimator := &contexty.CharFallbackEstimator{CharsPerToken: 2}
	msgs := []contexty.Message{{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.TextPart{Text: "ab"},
			contexty.ToolCallPart{Name: "x", Arguments: contexty.TextPayload("yz")},
		},
	}}
	// Act.
	n, err := estimator.Estimate(context.Background(), msgs)
	// Assert.
	require.NoError(t, err)
	assert.Positive(t, n)
}

func TestFixedEstimator_ToolParts(t *testing.T) {
	// Arrange.
	estimator := &contexty.FixedEstimator{TokensPerMessage: 1, TokensPerToolCall: 5}
	msgs := []contexty.Message{{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{ID: "1", Name: "f", Arguments: contexty.JSONPayload("{}")},
		},
	}}
	// Act.
	per, err := estimator.EstimatePerMessage(context.Background(), msgs)
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, 6, per[0])
}
