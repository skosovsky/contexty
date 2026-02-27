package contexty

import (
	"context"
	"fmt"
	"slices"
)

// strictStrategy returns an error if the block does not fit; otherwise passes through.
type strictStrategy struct{}

// NewStrictStrategy returns a strategy that fails with ErrBudgetExceeded when the block exceeds the limit.
// Use for TierSystem and other blocks that must never be evicted.
func NewStrictStrategy() EvictionStrategy {
	return &strictStrategy{}
}

func (s *strictStrategy) Apply(ctx context.Context, msgs []Message, limit int, counter TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: strict: %w", err)
	}
	n, err := countBlockTokens(counter, msgs)
	if err != nil {
		return nil, fmt.Errorf("contexty: strict: %w", err)
	}
	if n > limit {
		return nil, ErrBudgetExceeded
	}
	return msgs, nil
}

// dropStrategy removes the entire block when it does not fit.
type dropStrategy struct{}

// NewDropStrategy returns a strategy that drops the block entirely when it exceeds the limit.
// Use for RAG or other optional blocks where partial content is worse than none.
func NewDropStrategy() EvictionStrategy {
	return &dropStrategy{}
}

func (s *dropStrategy) Apply(ctx context.Context, msgs []Message, limit int, counter TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: drop: %w", err)
	}
	n, err := countBlockTokens(counter, msgs)
	if err != nil {
		return nil, fmt.Errorf("contexty: drop: %w", err)
	}
	if n > limit {
		return nil, nil
	}
	return msgs, nil
}

// truncateOldestStrategy removes messages from the start until the block fits.
type truncateOldestStrategy struct {
	cfg truncateConfig
}

// NewTruncateOldestStrategy returns a strategy that truncates from the oldest messages.
// Options: KeepUserAssistantPairs, MinMessages.
func NewTruncateOldestStrategy(opts ...TruncateOption) EvictionStrategy {
	cfg := truncateConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &truncateOldestStrategy{cfg: cfg}
}

func (s *truncateOldestStrategy) Apply(ctx context.Context, msgs []Message, limit int, counter TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: truncate: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	cur := slices.Clone(msgs)
	total, err := countBlockTokens(counter, cur)
	if err != nil {
		return nil, fmt.Errorf("contexty: truncate: %w", err)
	}
	for total > limit && len(cur) > 0 {
		remove := 1
		if s.cfg.keepPairs && len(cur) >= 2 && cur[0].Role == "user" && cur[1].Role == "assistant" {
			remove = 2
		}
		removedTokens, err := countBlockTokens(counter, cur[:remove])
		if err != nil {
			return nil, fmt.Errorf("contexty: truncate: %w", err)
		}
		total -= removedTokens
		cur = cur[remove:]
	}
	if len(cur) == 0 {
		return nil, nil
	}
	if s.cfg.minMessages > 0 && len(cur) < s.cfg.minMessages {
		return nil, nil
	}
	return cur, nil
}

// summarizeStrategy compresses the block via a Summarizer when it does not fit.
type summarizeStrategy struct {
	summarizer Summarizer
}

// NewSummarizeStrategy returns a strategy that calls the given Summarizer when the block exceeds the limit.
// If the summary still does not fit, the block is dropped (empty result).
// Panics if summarizer is nil (programmer error at init time).
func NewSummarizeStrategy(summarizer Summarizer) EvictionStrategy {
	if summarizer == nil {
		panic("contexty: NewSummarizeStrategy called with nil Summarizer")
	}
	return &summarizeStrategy{summarizer: summarizer}
}

func (s *summarizeStrategy) Apply(ctx context.Context, msgs []Message, limit int, counter TokenCounter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: summarize: %w", err)
	}
	n, err := countBlockTokens(counter, msgs)
	if err != nil {
		return nil, fmt.Errorf("contexty: summarize: %w", err)
	}
	if n <= limit {
		return msgs, nil
	}
	summary, err := s.summarizer.Summarize(ctx, msgs)
	if err != nil {
		return nil, fmt.Errorf("contexty: summarize: %w", err)
	}
	summaryTokens, err := countBlockTokens(counter, []Message{summary})
	if err != nil {
		return nil, fmt.Errorf("contexty: summarize: %w", err)
	}
	if summaryTokens > limit {
		return nil, nil
	}
	return []Message{summary}, nil
}

// Compile-time checks.
var (
	_ EvictionStrategy = (*strictStrategy)(nil)
	_ EvictionStrategy = (*dropStrategy)(nil)
	_ EvictionStrategy = (*truncateOldestStrategy)(nil)
	_ EvictionStrategy = (*summarizeStrategy)(nil)
)
