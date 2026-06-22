package contexty

import "slices"

// DropHeadConfig configures how DropHeadStrategy trims older messages.
type DropHeadConfig struct {
	// KeepTurnAtomicity enables atomic removal of assistant tool-call turns.
	// Nil means true (default).
	KeepTurnAtomicity *bool    `json:"keep_turn_atomicity"`
	MinMessages       int      `json:"min_messages"`
	ProtectedRoles    []string `json:"protected_roles"`
}

func (cfg DropHeadConfig) keepTurnAtomicity() bool {
	if cfg.KeepTurnAtomicity == nil {
		return true
	}
	return *cfg.KeepTurnAtomicity
}

func (cfg DropHeadConfig) normalized() DropHeadConfig {
	normalized := DropHeadConfig{
		KeepTurnAtomicity: BoolPtr(cfg.keepTurnAtomicity()),
		MinMessages:       cfg.MinMessages,
	}
	if normalized.MinMessages < 0 {
		normalized.MinMessages = 0
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
	slices.Sort(normalized.ProtectedRoles)
	return normalized
}

// BoolPtr returns a pointer to b (helper for optional config fields).
func BoolPtr(b bool) *bool {
	return new(b)
}
