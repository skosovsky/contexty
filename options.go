package contexty

import "slices"

// truncateConfig holds options for TruncateOldestStrategy.
type truncateConfig struct {
	keepPairs      bool
	minMessages    int
	protectedRoles []string // roles that must not be removed when truncating
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

// ProtectRole marks a role so that messages with this role are never removed when truncating.
// The first removable message (or user+assistant pair when KeepUserAssistantPairs is set) is
// removed instead. Duplicate roles are not added; the config stays deduplicated.
func ProtectRole(role string) TruncateOption {
	return func(c *truncateConfig) {
		if slices.Contains(c.protectedRoles, role) {
			return
		}
		c.protectedRoles = append(c.protectedRoles, role)
	}
}
