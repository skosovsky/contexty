package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/testutil"
)

func TestAcceptance_Estimator_ErrorPropagation(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	boom := errors.New("tokenizer unavailable")
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget: contexty.EffectiveInputBudget(10),
	}, &testutil.FailingEstimator{Err: boom})
	// Act.
	_, err := pipe.Apply(ctx, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hello"),
	})
	// Assert.
	require.Error(t, err)
	require.ErrorIs(t, err, contexty.ErrTokenCountFailed)
}
