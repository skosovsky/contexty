package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
)

var ErrInvalidCompaction = errors.New("contexty: invalid compaction record")

type CompactionProfile struct {
	Model      Descriptor `json:"model"`
	Summarizer Descriptor `json:"summarizer"`
	Estimator  Descriptor `json:"estimator"`
	Policy     Descriptor `json:"policy"`
	Encoding   Descriptor `json:"encoding"`
	Privacy    Descriptor `json:"privacy"`
}

// CompactionExecution records the concrete request and retention configuration.
type CompactionExecution struct {
	Summary   SummaryBudget     `json:"summary"`
	Retention RetentionPolicy   `json:"retention"`
	Policy    *CompactionPolicy `json:"policy,omitempty"`
	Required  []ContentRef      `json:"required,omitempty"`
}

func (e CompactionExecution) clone() CompactionExecution {
	e.Retention = e.Retention.clone()
	e.Policy = cloneCompactionPolicy(e.Policy)
	e.Required = slices.Clone(e.Required)
	return e
}

func (e CompactionExecution) validate() error {
	if err := e.Summary.validate(); err != nil {
		return err
	}
	if err := e.Retention.validate(); err != nil {
		return err
	}
	if e.Policy != nil {
		if err := e.Policy.Validate(); err != nil {
			return err
		}
	}
	for _, ref := range e.Required {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (p CompactionProfile) validate() error {
	for _, descriptor := range []Descriptor{p.Model, p.Summarizer, p.Estimator, p.Policy, p.Encoding, p.Privacy} {
		if err := descriptor.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func cloneCompactionProfile(profile *CompactionProfile) *CompactionProfile {
	if profile == nil {
		return nil
	}
	copyProfile := *profile
	return &copyProfile
}

// CompactionRecord records a summary decision, not an accepted whole compilation.
// Result is local saved content, never an isolated export or a read capability.
type CompactionRecord struct {
	Execution   CompactionExecution `json:"execution"`
	ID          string              `json:"id"`
	Digest      string              `json:"digest"`
	Profile     CompactionProfile   `json:"profile"`
	Covered     []ContentRef        `json:"covered"`
	Output      ContentRef          `json:"output"`
	Lineage     Lineage             `json:"lineage"`
	Budget      BudgetRequest       `json:"budget"`
	State       RecordState         `json:"state"`
	DecisionRef string              `json:"decision_ref,omitempty"`
	Result      *SavedContent       `json:"result,omitempty"`
	Estimate    *EstimateReport     `json:"estimate,omitempty"`
}

// NewCompactionRecord preserves references only when result is omitted by host.
func NewCompactionRecord(
	id string,
	profile CompactionProfile,
	covered []ContentRef,
	output ContentRef,
	lineage Lineage,
	budget BudgetRequest,
	execution CompactionExecution,
	result *SavedContent,
	estimate *EstimateReport,
) (CompactionRecord, error) {
	record := CompactionRecord{
		ID:          id,
		Digest:      "",
		Profile:     profile,
		Covered:     slices.Clone(covered),
		Output:      output,
		Lineage:     lineage.Clone(),
		Budget:      budget,
		Execution:   execution.clone(),
		State:       RecordProposed,
		DecisionRef: "",
		Result:      nil,
		Estimate:    nil,
	}
	if result != nil {
		copyResult := result.clone()
		record.Result = &copyResult
	}
	if estimate != nil {
		copyEstimate, err := estimate.Clone()
		if err != nil {
			return CompactionRecord{}, err
		}
		record.Estimate = &copyEstimate
	}
	return finalizeCompaction(record)
}

func (r CompactionRecord) Validate() error {
	if r.ID == "" {
		return ErrInvalidCompaction
	}
	if err := r.Profile.validate(); err != nil {
		return err
	}
	if _, err := r.Budget.Resolve(); err != nil {
		return err
	}
	if err := r.Execution.validate(); err != nil {
		return err
	}
	limit, _ := r.Budget.Resolve()
	if r.Execution.Summary.MaxTokens != limit || r.Execution.Summary.Purpose != r.Profile.Policy {
		return ErrInvalidCompaction
	}
	if err := r.validateCoverage(); err != nil {
		return err
	}
	if err := r.validateState(); err != nil {
		return err
	}
	if err := r.validateEstimate(); err != nil {
		return err
	}
	if r.Result != nil {
		if r.Result.Kind != SavedMessage || baseContentRef(r.Result.Ref) != baseContentRef(r.Output) {
			return ErrReplayContentMismatch
		}
		if err := validateSavedContent(*r.Result, r.Profile.Encoding); err != nil {
			return err
		}
	}
	digest, err := r.contentDigest()
	if err != nil || digest != r.Digest {
		return ErrInvalidCompaction
	}
	return nil
}

func (r CompactionRecord) validateState() error {
	switch r.State {
	case RecordProposed:
		if r.DecisionRef != "" {
			return ErrInvalidRecordState
		}
	case RecordAccepted, RecordSuperseded:
		if r.DecisionRef == "" {
			return ErrInvalidRecordState
		}
		if r.State == RecordAccepted && r.Result == nil {
			return ErrMissingReplayDependency
		}
		if r.State == RecordAccepted && r.Estimate == nil {
			return ErrMissingEstimateReport
		}
	default:
		return ErrInvalidRecordState
	}
	return nil
}

func (r CompactionRecord) validateEstimate() error {
	if r.Estimate == nil {
		return nil
	}
	report := r.Estimate
	if err := report.Validate(); err != nil {
		return err
	}
	if report.Profile.Model != r.Profile.Model || report.Profile.Estimator != r.Profile.Estimator ||
		report.Profile.Encoding != r.Profile.Encoding || report.Budget != r.Budget || report.ManifestRef != nil || report.WireRef != nil {
		return ErrStaleEstimate
	}
	if len(report.Segments) != 1 || report.Segments[0].Name != "summary" ||
		!slices.Equal(report.Segments[0].Messages, []ContentRef{baseContentRef(r.Output)}) {
		return ErrStaleEstimate
	}
	if r.State == RecordAccepted && report.OverflowReason != "" {
		return ErrBudgetExceeded
	}
	return nil
}

func (r CompactionRecord) validateCoverage() error {
	if len(r.Covered) == 0 {
		return ErrInvalidCoverage
	}
	if err := r.Output.Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, input := range r.Covered {
		if err := input.Validate(); err != nil {
			return err
		}
		if seen[input.ID] || input.ID == r.Output.ID {
			return ErrInvalidCoverage
		}
		seen[input.ID] = true
	}
	if err := r.Lineage.Validate(); err != nil {
		return err
	}
	for _, edge := range r.Lineage.Records {
		if edge.Stage != traceStageSummarize || !slices.Contains(edge.Outputs, r.Output) {
			continue
		}
		if edge.Transform != r.Profile.Summarizer || len(edge.Outputs) != 1 || !slices.Equal(edge.Inputs, r.Covered) {
			return ErrInvalidCoverage
		}
		return nil
	}
	return ErrInvalidCoverage
}

func (r CompactionRecord) contentDigest() (string, error) {
	r.Digest = ""
	return estimateDigest(r)
}

func finalizeCompaction(record CompactionRecord) (CompactionRecord, error) {
	digest, err := record.contentDigest()
	if err != nil {
		return CompactionRecord{}, err
	}
	record.Digest = digest
	if err := record.Validate(); err != nil {
		return CompactionRecord{}, err
	}
	return record, nil
}

func (r CompactionRecord) Clone() (CompactionRecord, error) {
	if err := r.Validate(); err != nil {
		return CompactionRecord{}, err
	}
	r.Covered = slices.Clone(r.Covered)
	r.Execution = r.Execution.clone()
	r.Lineage = r.Lineage.Clone()
	if r.Result != nil {
		result := r.Result.clone()
		r.Result = &result
	}
	if r.Estimate != nil {
		estimate, err := r.Estimate.Clone()
		if err != nil {
			return CompactionRecord{}, err
		}
		r.Estimate = &estimate
	}
	return r, nil
}

func (r CompactionRecord) Accept(decisionRef string) (CompactionRecord, error) {
	if r.State != RecordProposed || decisionRef == "" {
		return CompactionRecord{}, ErrInvalidRecordState
	}
	copyRecord, err := r.Clone()
	if err != nil {
		return CompactionRecord{}, err
	}
	copyRecord.State, copyRecord.DecisionRef = RecordAccepted, decisionRef
	return finalizeCompaction(copyRecord)
}

func (r CompactionRecord) Supersede(decisionRef string) (CompactionRecord, error) {
	if r.State != RecordAccepted || decisionRef == "" {
		return CompactionRecord{}, ErrInvalidRecordState
	}
	copyRecord, err := r.Clone()
	if err != nil {
		return CompactionRecord{}, err
	}
	copyRecord.State, copyRecord.DecisionRef = RecordSuperseded, decisionRef
	return finalizeCompaction(copyRecord)
}

func EncodeCompactionRecord(record CompactionRecord) ([]byte, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func DecodeCompactionRecord(wire []byte) (CompactionRecord, error) {
	var record CompactionRecord
	if err := decodeEstimateValue(wire, &record); err != nil {
		return CompactionRecord{}, ErrInvalidCompaction
	}
	if err := record.Validate(); err != nil {
		return CompactionRecord{}, err
	}
	return record, nil
}

// ReplayCompaction restores only currently expected, accepted saved results.
// It never executes a summarizer, resolves a resource or infers a remote outcome.
func ReplayCompaction(ctx context.Context, record CompactionRecord, expected ContentRef,
	profile CompactionProfile, covered []ContentRef, budget BudgetRequest, codec JSONSerializer) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	if err := record.Validate(); err != nil {
		return Message{}, err
	}
	if record.State != RecordAccepted {
		return Message{}, ErrUnsupportedReplay
	}
	if expected.ID != record.ID || expected.Digest != record.Digest || expected.Occurrence != "" ||
		profile != record.Profile || !slices.Equal(covered, record.Covered) || budget != record.Budget {
		return Message{}, ErrReplayMismatch
	}
	var result Message
	if err := codec.Unmarshal(slices.Clone(record.Result.Wire), &result); err != nil {
		return Message{}, ErrReplayCodec
	}
	ref, err := MessageContentRef(result, codec)
	if err != nil || ref != baseContentRef(record.Output) {
		return Message{}, ErrReplayContentMismatch
	}
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	return result, nil
}
