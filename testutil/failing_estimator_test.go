package testutil_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty/testutil"
)

func TestFailingEstimator_Errors(t *testing.T) {
	for _, configured := range []error{nil, errors.New("configured failure")} {
		t.Run(map[bool]string{true: "default", false: "configured"}[configured == nil], func(t *testing.T) {
			// Arrange.
			estimator := &testutil.FailingEstimator{Err: configured}
			// Act.
			tokens, totalErr := estimator.Estimate(context.Background(), nil)
			weights, weightsErr := estimator.EstimatePerMessage(context.Background(), nil)
			// Assert.
			require.Zero(t, tokens)
			require.Nil(t, weights)
			if configured == nil {
				require.EqualError(t, totalErr, "estimate failed")
				require.EqualError(t, weightsErr, "estimate failed")
			} else {
				require.ErrorIs(t, totalErr, configured)
				require.ErrorIs(t, weightsErr, configured)
			}
		})
	}
}
