package testutil

import (
	"context"
	"errors"

	"github.com/skosovsky/contexty"
)

// FailingEstimator is a test double that always fails with Err, or a default error if Err is nil.
type FailingEstimator struct {
	Err error
}

// Estimate returns no tokens and the configured error.
func (f *FailingEstimator) Estimate(context.Context, []contexty.Message) (int, error) {
	if f.Err == nil {
		return 0, errors.New("estimate failed")
	}
	return 0, f.Err
}

// EstimatePerMessage returns no weights and the configured error.
func (f *FailingEstimator) EstimatePerMessage(context.Context, []contexty.Message) ([]int, error) {
	if f.Err == nil {
		return nil, errors.New("estimate failed")
	}
	return nil, f.Err
}

var _ contexty.TokenEstimator = (*FailingEstimator)(nil)
