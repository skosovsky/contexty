package contexty

// EstimatorIdentity pins semantic cost behavior without serializing executions.
type EstimatorIdentity struct {
	Descriptor Descriptor                    `json:"descriptor"`
	Fixed      *FixedEstimatorParameters     `json:"fixed,omitempty"`
	Character  *CharacterEstimatorParameters `json:"character,omitempty"`
}

type FixedEstimatorParameters struct {
	PerMessage  int `json:"per_message"`
	PerPart     int `json:"per_part"`
	PerToolCall int `json:"per_tool_call"`
}

type CharacterEstimatorParameters struct {
	CharsPerToken     int  `json:"chars_per_token"`
	NonTextWeight     int  `json:"non_text_weight"`
	CustomToolCounter bool `json:"custom_tool_counter"`
}

const (
	estimatorCharID     = "contexty/estimate/characters"
	estimatorFixedID    = "contexty/estimate/fixed"
	estimatorFallbackID = "contexty/estimate/character-fallback"
	estimatorContract   = "contract"
)

// WithEstimatorDescriptor binds an opaque host counter (including custom tool cost).
func WithEstimatorDescriptor(descriptor Descriptor) BudgetPipelineOption {
	return func(p *BudgetPipeline) { copyDescriptor := descriptor; p.estimatorDescriptor = &copyDescriptor }
}

func freezeBuiltinEstimator(estimator TokenEstimator) TokenEstimator {
	switch counter := estimator.(type) {
	case *FixedEstimator:
		if counter != nil {
			copyCounter := *counter
			return &copyCounter
		}
	case *CharFallbackEstimator:
		if counter != nil {
			copyCounter := *counter
			return &copyCounter
		}
	}
	return estimator
}

func (p *BudgetPipeline) estimatorIdentity() (EstimatorIdentity, error) {
	if reporter, ok := p.estimator.(*EstimateReporter); ok {
		if reporter == nil {
			return EstimatorIdentity{}, ErrInvalidRecordingComponent
		}
		if p.estimatorDescriptor != nil && *p.estimatorDescriptor != reporter.profile.Estimator {
			return EstimatorIdentity{}, ErrInvalidRecordingComponent
		}
		return estimatorIdentityFor(reporter.estimator, &reporter.profile.Estimator)
	}
	return estimatorIdentityFor(p.estimator, p.estimatorDescriptor)
}

func estimatorIdentityFor(estimator TokenEstimator, host *Descriptor) (EstimatorIdentity, error) {
	identity := EstimatorIdentity{Descriptor: Descriptor{ID: "", Revision: ""}, Fixed: nil, Character: nil}
	var intrinsic string
	switch counter := estimator.(type) {
	case CharTokenEstimator:
		intrinsic = estimatorCharID
	case *CharTokenEstimator:
		if counter == nil {
			return identity, ErrInvalidRecordingComponent
		}
		intrinsic = estimatorCharID
	case *FixedEstimator:
		if counter == nil {
			return identity, ErrInvalidRecordingComponent
		}
		intrinsic = estimatorFixedID
		identity.Fixed = &FixedEstimatorParameters{PerMessage: counter.TokensPerMessage,
			PerPart: counter.TokensPerContentPart, PerToolCall: counter.TokensPerToolCall}
	case *CharFallbackEstimator:
		if counter == nil {
			return identity, ErrInvalidRecordingComponent
		}
		intrinsic = estimatorFallbackID
		nonText := counter.TokensPerNonTextPart
		if nonText <= 0 {
			nonText = DefaultTokensPerNonTextPart
		}
		identity.Character = &CharacterEstimatorParameters{CharsPerToken: counter.CharsPerToken,
			NonTextWeight: nonText, CustomToolCounter: counter.EstimateTool != nil}
		if counter.EstimateTool != nil && host == nil {
			return identity, ErrInvalidRecordingComponent
		}
	default:
		if host == nil {
			return identity, ErrInvalidRecordingComponent
		}
	}
	descriptor, err := estimatorBinding(intrinsic, host)
	if err != nil {
		return identity, err
	}
	identity.Descriptor = descriptor
	return identity, identity.validate()
}

func estimatorBinding(intrinsic string, host *Descriptor) (Descriptor, error) {
	if host == nil {
		return Descriptor{ID: intrinsic, Revision: estimatorContract}, nil
	}
	switch host.ID {
	case estimatorCharID, estimatorFixedID, estimatorFallbackID:
		if host.ID != intrinsic {
			return Descriptor{ID: "", Revision: ""}, ErrInvalidRecordingComponent
		}
	}
	return *host, nil
}

func (p EstimatorIdentity) clone() EstimatorIdentity {
	if p.Fixed != nil {
		copyParameters := *p.Fixed
		p.Fixed = &copyParameters
	}
	if p.Character != nil {
		copyParameters := *p.Character
		p.Character = &copyParameters
	}
	return p
}

func (p EstimatorIdentity) validate() error {
	if err := p.Descriptor.Validate(); err != nil {
		return err
	}
	if p.Fixed != nil && p.Character != nil {
		return ErrInvalidRecordingComponent
	}
	if p.Fixed != nil && (p.Fixed.PerMessage < 0 || p.Fixed.PerPart < 0 || p.Fixed.PerToolCall < 0) {
		return ErrInvalidRecordingComponent
	}
	if p.Character != nil && (p.Character.CharsPerToken <= 0 || p.Character.NonTextWeight <= 0) {
		return ErrInvalidRecordingComponent
	}
	switch p.Descriptor.ID {
	case estimatorCharID:
		if p.Descriptor.Revision != estimatorContract || p.Fixed != nil || p.Character != nil {
			return ErrInvalidRecordingComponent
		}
	case estimatorFixedID:
		if p.Descriptor.Revision != estimatorContract || p.Fixed == nil {
			return ErrInvalidRecordingComponent
		}
	case estimatorFallbackID:
		if p.Descriptor.Revision != estimatorContract || p.Character == nil || p.Character.CustomToolCounter {
			return ErrInvalidRecordingComponent
		}
	}
	return nil
}
