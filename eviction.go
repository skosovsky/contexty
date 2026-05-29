package contexty

import "context"

// EvictionStrategy shrinks a message block to fit a token limit.
type EvictionStrategy interface {
	Apply(
		ctx context.Context,
		msgs []Message,
		originalTokens int,
		limit int,
		estimator TokenEstimator,
	) ([]Message, error)
}
