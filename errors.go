package contexty

import "errors"

// Sentinel errors for typical contexty failure modes.
// Use errors.Is to check for these in calling code.
var (
	// ErrBudgetExceeded is returned by StrictStrategy when a block does not fit
	// within the remaining token budget.
	ErrBudgetExceeded = errors.New("contexty: block exceeds remaining token budget")

	// ErrInvalidConfig is returned by Compile when configuration is invalid
	// (e.g. MaxTokens <= 0 or TokenCounter == nil).
	ErrInvalidConfig = errors.New("contexty: invalid allocator config")

	// ErrTokenCountFailed is returned when TokenCounter.Count returns an error.
	ErrTokenCountFailed = errors.New("contexty: token counting failed")

	// ErrInvalidCharsPerToken is returned by CharFallbackCounter when
	// CharsPerToken is zero or negative.
	ErrInvalidCharsPerToken = errors.New("contexty: CharsPerToken must be positive")

	// ErrNilStrategy is returned by Compile when a MemoryBlock has a nil Strategy.
	ErrNilStrategy = errors.New("contexty: block has nil eviction strategy")

	// ErrStrategyExceededBudget is returned by Compile when an EvictionStrategy.Apply
	// returns messages whose token count exceeds the remaining budget (contract violation).
	ErrStrategyExceededBudget = errors.New("contexty: strategy returned output exceeding remaining budget")
)
