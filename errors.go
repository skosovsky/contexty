package contexty

import "errors"

// Sentinel errors for typical contexty failure modes.
// Use [errors.Is] to check for these in calling code.
var (
	// ErrBudgetExceeded is returned by StrictStrategy when a block does not fit
	// within the remaining token budget.
	ErrBudgetExceeded = errors.New("contexty: block exceeds remaining token budget")

	// ErrInvalidConfig is returned by Build when configuration is invalid
	// (e.g. maxTokens <= 0 or TokenCounter == nil).
	ErrInvalidConfig = errors.New("contexty: invalid builder config")

	// ErrTokenCountFailed is returned when TokenCounter.Count returns an error.
	ErrTokenCountFailed = errors.New("contexty: token counting failed")

	// ErrInvalidCharsPerToken is returned by CharFallbackCounter when
	// CharsPerToken is zero or negative.
	ErrInvalidCharsPerToken = errors.New("contexty: CharsPerToken must be positive")

	// ErrNilStrategy is returned by Build when a MemoryBlock has a nil Strategy.
	ErrNilStrategy = errors.New("contexty: block has nil eviction strategy")

	// ErrStrategyExceededBudget is returned by Build when an EvictionStrategy.Apply
	// returns messages whose token count exceeds the remaining budget (contract violation).
	ErrStrategyExceededBudget = errors.New("contexty: strategy returned output exceeding remaining budget")

	// ErrFormatterExceededBudget is returned when Formatter.Format returns
	// messages that exceed the builder token budget.
	ErrFormatterExceededBudget = errors.New("contexty: formatter returned output exceeding token budget")

	// ErrBlockTooLarge is returned by DropTailStrategy when a single message
	// still exceeds the limit after all trailing messages were dropped.
	ErrBlockTooLarge = errors.New("contexty: block cannot be shrunk to fit limit")

	// ErrBlockNotFound is returned when a Builder block lookup by name fails.
	ErrBlockNotFound = errors.New("contexty: block not found")

	// ErrInvalidThreadConfig is returned when Thread configuration is invalid.
	ErrInvalidThreadConfig = errors.New("contexty: invalid thread config")

	// ErrHistoryVersionConflict is returned when a store write is rejected because
	// the thread history was modified concurrently (optimistic concurrency).
	// Do not retry the same write without a fresh Load: merge against the returned
	// Version, then call Append/Save/Clear again. This is not a transient outage and
	// must not be conflated with [ErrUnavailable] or with context cancellation.
	ErrHistoryVersionConflict = errors.New("contexty: history version conflict")

	// ErrUnavailable indicates a transient storage failure (network I/O, client-side
	// deadline via [context.DeadlineExceeded], dial/read timeouts, closed pools, etc.).
	// Callers may retry with backoff when policy allows; use [errors.Is] to detect it.
	// [context.Canceled] is usually not retried (the request was cancelled).
	// Storage adapters wrap underlying errors so ErrUnavailable remains visible in the chain.
	ErrUnavailable = errors.New("contexty: storage unavailable")
)
