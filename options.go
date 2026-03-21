package contexty

import "slices"

// DropHeadConfig configures how DropHeadStrategy trims older messages.
type DropHeadConfig struct {
	KeepTurnAtomicity bool
	MinMessages       int
	ProtectedRoles    []string
}

func (cfg DropHeadConfig) normalized() DropHeadConfig {
	normalized := DropHeadConfig{
		KeepTurnAtomicity: cfg.KeepTurnAtomicity,
	}
	if cfg.MinMessages > 0 {
		normalized.MinMessages = cfg.MinMessages
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
