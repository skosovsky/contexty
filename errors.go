package contexty

import "errors"

// Sentinel errors for typical contexty failure modes.
// Use [errors.Is] to check for these in calling code.
var (
	// ErrBudgetExceeded is returned by StrictStrategy when a block does not fit
	// within the remaining token budget.
	ErrBudgetExceeded = errors.New("contexty: block exceeds remaining token budget")

	// ErrTokenCountFailed is returned when TokenEstimator.Estimate returns an error.
	ErrTokenCountFailed = errors.New("contexty: token counting failed")

	// ErrInvalidCharsPerToken is returned by CharFallbackEstimator when
	// CharsPerToken is zero or negative.
	ErrInvalidCharsPerToken = errors.New("contexty: CharsPerToken must be positive")

	// ErrBlockTooLarge is returned when truncation cannot shrink a block to fit.
	ErrBlockTooLarge = errors.New("contexty: block cannot be shrunk to fit limit")

	// ErrPendingExceedsBudget is returned when protected Pending messages alone exceed the token limit.
	ErrPendingExceedsBudget = errors.New("contexty: pending messages exceed token budget")

	// ErrDuplicateMessageID is returned when the same Message.ID appears more than once in CompileRequest.
	ErrDuplicateMessageID = errors.New("contexty: duplicate message id in compile request")

	// ErrConversationVersionConflict is returned when a store write is rejected because
	// the conversation was modified concurrently (optimistic concurrency).
	// Do not retry the same write without a fresh LoadState: merge against the
	// returned Version, then call ApplyDelta or ClearState again. This is not a
	// transient outage and must not be conflated with [ErrUnavailable] or with context cancellation.
	ErrConversationVersionConflict = errors.New("contexty: conversation version conflict")

	// ErrUnavailable indicates a transient storage failure (network I/O, client-side
	// deadline via [context.DeadlineExceeded], dial/read timeouts, closed pools, etc.).
	// Callers may retry with backoff when policy allows; use [errors.Is] to detect it.
	// [context.Canceled] is usually not retried (the request was cancelled).
	// Storage adapters wrap underlying errors so ErrUnavailable remains visible in the chain.
	ErrUnavailable = errors.New("contexty: storage unavailable")
)
