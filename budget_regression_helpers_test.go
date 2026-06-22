package contexty_test

import (
	"context"

	"github.com/skosovsky/contexty"
)

type budgetCallbackEviction struct {
	output []contexty.Message
	cancel context.CancelFunc
}

type budgetCallbackObserver struct {
	contexty.NoopObserver

	cancel  context.CancelFunc
	summary bool
}

func (o budgetCallbackObserver) OnTokensEstimated(context.Context, string, int) {
	if !o.summary {
		o.cancel()
	}
}

func (o budgetCallbackObserver) OnContextSummarized(context.Context, float64) {
	if o.summary {
		o.cancel()
	}
}

func (s budgetCallbackEviction) Apply(
	_ context.Context,
	_ []contexty.Message,
	_, _ int,
	_ contexty.TokenEstimator,
) ([]contexty.Message, error) {
	if s.cancel != nil {
		s.cancel()
	}
	return s.output, nil
}
