package contexty

// truncateConfig holds options for TruncateOldestStrategy.
type truncateConfig struct {
	keepPairs   bool
	minMessages int
}

// TruncateOption configures TruncateOldestStrategy behavior.
type TruncateOption func(*truncateConfig)

// KeepUserAssistantPairs ensures messages are removed in user-assistant pairs
// from the start, so that dialog coherence is preserved (no orphan user or assistant).
func KeepUserAssistantPairs(keep bool) TruncateOption {
	return func(c *truncateConfig) {
		c.keepPairs = keep
	}
}

// MinMessages sets the minimum number of messages to keep after truncation.
// If the remaining budget cannot fit at least MinMessages messages, the block
// is dropped entirely (empty result).
func MinMessages(n int) TruncateOption {
	return func(c *truncateConfig) {
		if n > 0 {
			c.minMessages = n
		}
	}
}
