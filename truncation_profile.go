package contexty

const (
	truncationDropHeadID = "contexty/truncate/drop-head"
	truncationStrictID   = "contexty/truncate/strict"
	truncationDropID     = "contexty/truncate/drop"
	truncationDropTailID = "contexty/truncate/drop-tail"
	truncationContract   = "contract"
)

// TruncationProfile describes behavior, never the executable strategy object.
type TruncationProfile struct {
	Descriptor Descriptor      `json:"descriptor"`
	DropHead   *DropHeadConfig `json:"drop_head,omitempty"`
}

// WithTruncationDescriptor identifies a host strategy in recording mode.
// Built-in strategies already have intrinsic identities and cannot override them.
func WithTruncationDescriptor(descriptor Descriptor) BudgetPipelineOption {
	return func(p *BudgetPipeline) { copyDescriptor := descriptor; p.truncation = &copyDescriptor }
}

func (p *BudgetPipeline) truncationProfile() (TruncationProfile, error) {
	profile := TruncationProfile{Descriptor: Descriptor{ID: "", Revision: ""}, DropHead: nil}
	var id string
	switch strategy := p.cfg.TruncateStrategy.(type) {
	case nil:
		id = truncationDropHeadID
		cfg := p.cfg.DropHead.normalized()
		profile.DropHead = &cfg
	case *dropHeadStrategy:
		if strategy == nil {
			return profile, ErrInvalidRecordingComponent
		}
		id = truncationDropHeadID
		cfg := strategy.cfg.normalized()
		profile.DropHead = &cfg
	case *strictStrategy:
		if strategy == nil {
			return profile, ErrInvalidRecordingComponent
		}
		id = truncationStrictID
	case *dropStrategy:
		if strategy == nil {
			return profile, ErrInvalidRecordingComponent
		}
		id = truncationDropID
	case *dropTailStrategy:
		if strategy == nil {
			return profile, ErrInvalidRecordingComponent
		}
		id = truncationDropTailID
	default:
		if p.truncation == nil {
			return profile, ErrInvalidRecordingComponent
		}
		profile.Descriptor = *p.truncation
		switch profile.Descriptor.ID {
		case truncationDropHeadID, truncationStrictID, truncationDropID, truncationDropTailID:
			return profile, ErrInvalidRecordingComponent
		}
		return profile, profile.validate()
	}
	if p.truncation != nil {
		return profile, ErrInvalidRecordingComponent
	}
	profile.Descriptor = Descriptor{ID: id, Revision: truncationContract}
	return profile, profile.validate()
}

func (p TruncationProfile) clone() TruncationProfile {
	if p.DropHead != nil {
		cfg := p.DropHead.normalized()
		p.DropHead = &cfg
	}
	return p
}

func (p TruncationProfile) validate() error {
	if err := p.Descriptor.Validate(); err != nil {
		return err
	}
	if p.Descriptor.ID == truncationDropHeadID {
		if p.Descriptor.Revision != truncationContract || p.DropHead == nil || p.DropHead.KeepTurnAtomicity == nil ||
			p.DropHead.MinMessages < 0 {
			return ErrInvalidRecordingComponent
		}
		return nil
	}
	if p.DropHead != nil {
		return ErrInvalidRecordingComponent
	}
	switch p.Descriptor.ID {
	case truncationStrictID, truncationDropID, truncationDropTailID:
		if p.Descriptor.Revision != truncationContract {
			return ErrInvalidRecordingComponent
		}
	}
	return nil
}

func (e *Engine) validateRecordingBudgets(targets []CompileTarget) error {
	if e.budget != nil {
		if err := e.budget.validateRecordingBudget(); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if target.Budget != nil {
			if err := target.Budget.validateRecordingBudget(); err != nil {
				return err
			}
		}
	}
	return nil
}
