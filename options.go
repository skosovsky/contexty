package contexty

// DropHeadConfig configures how DropHeadStrategy trims older messages.
type DropHeadConfig struct {
	// KeepTurnAtomicity enables atomic removal of assistant tool-call turns.
	// Nil means true (default).
	KeepTurnAtomicity *bool `json:"keep_turn_atomicity"`
	MinMessages       int   `json:"min_messages"`
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
	return normalized
}

// BoolPtr returns a pointer to b (helper for optional config fields).
func BoolPtr(b bool) *bool {
	return new(b)
}
