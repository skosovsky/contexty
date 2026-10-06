package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Transformations_ByMessageID(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:   contexty.EffectiveInputBudget(15),
			DropHead: contexty.DropHeadConfig{MinMessages: 1},
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := fixtureEngine(
		contexty.WithBudgetPipeline(pipe),
	)
	req := contexty.CompileRequest{History: []contexty.Message{
		{
			ID:    "drop",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}},
		},
		{
			ID:    "keep",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "new"}},
		},
	}}
	// Act.
	result, err := engine.CompileSnapshot(ctx, req)
	// Assert.
	require.NoError(t, err)
	rec, ok := result.Transformations["drop"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionTruncated, rec.Final().Action)
	assert.Equal(t, contexty.ReasonTokenBudgetExceeded, rec.Final().Reason)
	_, ok = result.Transformations["keep"]
	assert.True(t, ok)
}
