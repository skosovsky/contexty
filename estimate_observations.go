package contexty

import (
	"encoding/json"
	"errors"
	"slices"
)

var ErrDuplicateEstimateObservation = errors.New("contexty: duplicate estimate observation")

// WireCountEvidence is host-produced evidence, not a provider invocation.
// Guaranteed means the counter guarantees accuracy for this exact wire request.
type WireCountEvidence struct {
	ID            string          `json:"id"`
	Counter       Descriptor      `json:"counter"`
	RequestDigest string          `json:"request_digest"`
	ProfileDigest string          `json:"profile_digest"`
	WireRef       ContentRef      `json:"wire_ref"`
	Tokens        int             `json:"tokens"`
	Quality       EstimateQuality `json:"quality"`
	Guaranteed    bool            `json:"guaranteed"`
}

type WireEstimateObservation struct {
	Evidence       WireCountEvidence `json:"evidence"`
	InputLimit     int               `json:"input_limit"`
	OverflowReason string            `json:"overflow_reason,omitempty"`
}

type UsageEvidence struct {
	ID            string     `json:"id"`
	Source        Descriptor `json:"source"`
	RequestDigest string     `json:"request_digest"`
	ProfileDigest string     `json:"profile_digest"`
	WireRef       ContentRef `json:"wire_ref"`
	InputTokens   int        `json:"input_tokens"`
	OutputTokens  int        `json:"output_tokens"`
}

// EstimateHistory keeps semantic estimates, wire observations and usage separate.
type EstimateHistory struct {
	Digest   string                    `json:"digest"`
	Estimate EstimateReport            `json:"estimate"`
	Wire     []WireEstimateObservation `json:"wire"`
	Usage    []UsageEvidence           `json:"usage"`
}

func NewEstimateHistory(report EstimateReport) (EstimateHistory, error) {
	copyReport, err := report.Clone()
	if err != nil {
		return EstimateHistory{}, err
	}
	return finalizeEstimateHistory(EstimateHistory{Digest: "", Estimate: copyReport, Wire: nil, Usage: nil})
}

func (h EstimateHistory) WithWireCount(evidence WireCountEvidence) (EstimateHistory, error) {
	next, err := h.Clone()
	if err != nil {
		return EstimateHistory{}, err
	}
	if err := validateWireCount(next.Estimate, evidence); err != nil {
		return EstimateHistory{}, err
	}
	if slices.ContainsFunc(
		next.Wire,
		func(item WireEstimateObservation) bool { return item.Evidence.ID == evidence.ID },
	) {
		return EstimateHistory{}, ErrDuplicateEstimateObservation
	}
	next.Wire = append(next.Wire, wireEstimateObservation(next.Estimate, evidence))
	return finalizeEstimateHistory(next)
}

func (h EstimateHistory) WithUsage(evidence UsageEvidence) (EstimateHistory, error) {
	next, err := h.Clone()
	if err != nil {
		return EstimateHistory{}, err
	}
	if err := validateUsage(next.Estimate, evidence); err != nil {
		return EstimateHistory{}, err
	}
	if slices.ContainsFunc(next.Usage, func(item UsageEvidence) bool { return item.ID == evidence.ID }) {
		return EstimateHistory{}, ErrDuplicateEstimateObservation
	}
	next.Usage = append(next.Usage, evidence)
	return finalizeEstimateHistory(next)
}

func (h EstimateHistory) Validate() error {
	if err := h.Estimate.Validate(); err != nil {
		return err
	}
	seenWire, seenUsage := make(map[string]bool), make(map[string]bool)
	for _, observation := range h.Wire {
		if seenWire[observation.Evidence.ID] {
			return ErrDuplicateEstimateObservation
		}
		seenWire[observation.Evidence.ID] = true
		if err := validateWireCount(h.Estimate, observation.Evidence); err != nil {
			return err
		}
		if observation != wireEstimateObservation(h.Estimate, observation.Evidence) {
			return ErrInvalidEstimateReport
		}
	}
	for _, evidence := range h.Usage {
		if seenUsage[evidence.ID] {
			return ErrDuplicateEstimateObservation
		}
		seenUsage[evidence.ID] = true
		if err := validateUsage(h.Estimate, evidence); err != nil {
			return err
		}
	}
	digest, err := h.contentDigest()
	if err != nil || digest != h.Digest {
		return ErrInvalidEstimateReport
	}
	return nil
}

func (h EstimateHistory) contentDigest() (string, error) {
	h.Digest = ""
	return estimateDigest(h)
}

func finalizeEstimateHistory(history EstimateHistory) (EstimateHistory, error) {
	digest, err := history.contentDigest()
	if err != nil {
		return EstimateHistory{}, err
	}
	history.Digest = digest
	if err := history.Validate(); err != nil {
		return EstimateHistory{}, err
	}
	return history, nil
}

func validateWireCount(report EstimateReport, evidence WireCountEvidence) error {
	if evidence.ID == "" || evidence.Tokens < 0 ||
		(evidence.Quality != EstimateCounted && evidence.Quality != EstimateEstimated) ||
		(evidence.Quality == EstimateCounted && !evidence.Guaranteed) {
		return ErrInvalidEstimateReport
	}
	if err := evidence.Counter.Validate(); err != nil {
		return err
	}
	return validateObservationIdentity(report, evidence.RequestDigest, evidence.ProfileDigest, evidence.WireRef)
}

func validateUsage(report EstimateReport, evidence UsageEvidence) error {
	if evidence.ID == "" || evidence.InputTokens < 0 || evidence.OutputTokens < 0 {
		return ErrInvalidEstimateReport
	}
	if err := evidence.Source.Validate(); err != nil {
		return err
	}
	return validateObservationIdentity(report, evidence.RequestDigest, evidence.ProfileDigest, evidence.WireRef)
}

func validateObservationIdentity(report EstimateReport, request, profile string, wire ContentRef) error {
	if err := wire.Validate(); err != nil {
		return err
	}
	if report.WireRef == nil || wire != *report.WireRef || request != report.RequestDigest ||
		profile != report.ProfileDigest {
		return ErrStaleEstimate
	}
	return nil
}

func wireEstimateObservation(report EstimateReport, evidence WireCountEvidence) WireEstimateObservation {
	limit := report.EffectiveLimit
	if report.Budget.Mode == BudgetWindow {
		limit = report.Budget.Window - report.Budget.OutputReservation
	}
	reason := ""
	if evidence.Tokens > limit {
		reason = ReasonTokenBudgetExceeded
	}
	return WireEstimateObservation{Evidence: evidence, InputLimit: limit, OverflowReason: reason}
}

func EncodeEstimateHistory(history EstimateHistory) ([]byte, error) {
	if err := history.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(history)
}

func DecodeEstimateHistory(wire []byte) (EstimateHistory, error) {
	var history EstimateHistory
	if err := decodeEstimateValue(wire, &history); err != nil {
		return EstimateHistory{}, err
	}
	if err := history.Validate(); err != nil {
		return EstimateHistory{}, err
	}
	return history, nil
}

func (h EstimateHistory) Clone() (EstimateHistory, error) {
	wire, err := EncodeEstimateHistory(h)
	if err != nil {
		return EstimateHistory{}, err
	}
	return DecodeEstimateHistory(wire)
}
