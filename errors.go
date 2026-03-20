package contexty

import "errors"

// Sentinel errors for typical contexty failure modes.
// Use errors.Is to check for these in calling code.
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
)
