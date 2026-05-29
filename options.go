package contexty

import "slices"

// DropHeadConfig configures how DropHeadStrategy trims older messages.
type DropHeadConfig struct {
	// KeepTurnAtomicity enables atomic removal of assistant tool-call turns.
	// Nil means true (default).
	KeepTurnAtomicity *bool
	MinMessages       int
	ProtectedRoles    []string
}

func (cfg DropHeadConfig) keepTurnAtomicity() bool {
	if cfg.KeepTurnAtomicity == nil {
		return true
	}
	return *cfg.KeepTurnAtomicity
}

func (cfg DropHeadConfig) normalized() DropHeadConfig {
	normalized := DropHeadConfig{
		KeepTurnAtomicity: cfg.KeepTurnAtomicity,
		MinMessages:       cfg.MinMessages,
	}
	if len(cfg.ProtectedRoles) == 0 {
		return normalized
	}
	normalized.ProtectedRoles = make([]string, 0, len(cfg.ProtectedRoles))
	for _, role := range cfg.ProtectedRoles {
		if role == "" || slices.Contains(normalized.ProtectedRoles, role) {
			continue
		}
		normalized.ProtectedRoles = append(normalized.ProtectedRoles, role)
	}
	return normalized
}

// BoolPtr returns a pointer to b (helper for optional config fields).
func BoolPtr(b bool) *bool {
	return new(b)
}
