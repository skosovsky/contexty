package contexty

import "errors"

var ErrInvalidBudgetRequest = errors.New("contexty: invalid input budget request")

type BudgetMode string

const (
	BudgetWindow    BudgetMode = "window"
	BudgetEffective BudgetMode = "effective"
)

// BudgetRequest explicitly separates model window from usable input capacity.
// A zero effective limit is valid; an unspecified mode is not.
type BudgetRequest struct {
	Mode              BudgetMode `json:"mode"`
	Window            int        `json:"window"`
	OutputReservation int        `json:"output_reservation"`
	WireReservation   int        `json:"wire_reservation"`
	EffectiveLimit    int        `json:"effective_limit"`
}

func EffectiveInputBudget(limit int) BudgetRequest {
	return BudgetRequest{Mode: BudgetEffective, Window: 0, OutputReservation: 0, WireReservation: 0,
		EffectiveLimit: limit}
}

func WindowInputBudget(window, output, wire int) BudgetRequest {
	return BudgetRequest{Mode: BudgetWindow, Window: window, OutputReservation: output,
		WireReservation: wire, EffectiveLimit: 0}
}

// Resolve returns the usable input limit without overflow or repeated subtraction.
func (b BudgetRequest) Resolve() (int, error) {
	if b.Window < 0 || b.OutputReservation < 0 || b.WireReservation < 0 || b.EffectiveLimit < 0 {
		return 0, ErrInvalidBudgetRequest
	}
	switch b.Mode {
	case BudgetEffective:
		if b.Window != 0 || b.OutputReservation != 0 || b.WireReservation != 0 {
			return 0, ErrInvalidBudgetRequest
		}
		return b.EffectiveLimit, nil
	case BudgetWindow:
		if b.EffectiveLimit != 0 || b.OutputReservation > b.Window || b.WireReservation > b.Window-b.OutputReservation {
			return 0, ErrInvalidBudgetRequest
		}
		return b.Window - b.OutputReservation - b.WireReservation, nil
	default:
		return 0, ErrInvalidBudgetRequest
	}
}

func (e *Engine) validateInputBudgets(targets []CompileTarget) error {
	if e.budget != nil {
		if err := e.budget.validateBudgetPolicy(); err != nil {
			return err
		}
		if _, err := e.budget.cfg.Budget.Resolve(); err != nil {
			return err
		}
		if err := e.validateEstimateProfile(e.budget, true); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if target.Budget != nil {
			if err := target.Budget.validateBudgetPolicy(); err != nil {
				return err
			}
			if _, err := target.Budget.cfg.Budget.Resolve(); err != nil {
				return err
			}
			if err := e.validateEstimateProfile(target.Budget, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) validateEstimateProfile(pipeline *BudgetPipeline, main bool) error {
	profile := pipeline.reportProfile()
	if profile == nil {
		return nil
	}
	if e.trace != nil && profile.Encoding != e.trace.Encoding {
		return ErrStaleEstimate
	}
	if e.recording != nil &&
		(profile.Model != e.recording.Model || (main && profile.Estimator != e.recording.Estimator)) {
		return ErrStaleEstimate
	}
	return nil
}
