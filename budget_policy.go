package contexty

import (
	"context"
	"errors"
	"slices"
)

var (
	ErrInvalidRetention        = errors.New("contexty: invalid required content")
	ErrRetentionExceedsBudget  = errors.New("contexty: required content exceeds budget")
	ErrInvalidCompactionPolicy = errors.New("contexty: invalid compaction threshold policy")
)

// RetentionPolicy selects immutable required content, independently of eviction.
type RetentionPolicy struct {
	MessageIDs  []string     `json:"message_ids,omitempty"`
	ContentRefs []ContentRef `json:"content_refs,omitempty"`
	Roles       []Role       `json:"roles,omitempty"`
}

func (p RetentionPolicy) clone() RetentionPolicy {
	p.MessageIDs = slices.Clone(p.MessageIDs)
	p.ContentRefs = slices.Clone(p.ContentRefs)
	p.Roles = slices.Clone(p.Roles)
	return p
}

func (p RetentionPolicy) validate() error {
	if slices.Contains(p.MessageIDs, "") {
		return ErrInvalidRetention
	}
	for _, ref := range p.ContentRefs {
		if ref.Validate() != nil || ref.Occurrence != "" {
			return ErrInvalidRetention
		}
	}
	for _, role := range p.Roles {
		if err := role.Validate(); err != nil {
			return errors.Join(ErrInvalidRetention, err)
		}
	}
	return nil
}

// CompactionPolicy separates the trigger and soft target from hard capacity.
type CompactionPolicy struct {
	Descriptor     Descriptor `json:"descriptor"`
	TriggerPercent int        `json:"trigger_percent"`
	TargetPercent  int        `json:"target_percent"`
}

func (p CompactionPolicy) Validate() error {
	if p.Descriptor.Validate() != nil || p.TargetPercent < 1 || p.TriggerPercent > 100 ||
		p.TargetPercent > p.TriggerPercent {
		return ErrInvalidCompactionPolicy
	}
	return nil
}

func cloneCompactionPolicy(p *CompactionPolicy) *CompactionPolicy {
	if p == nil {
		return nil
	}
	copyPolicy := *p
	return &copyPolicy
}

func budgetPercent(limit, percent int) int {
	const whole = 100
	return limit/whole*percent + limit%whole*percent/whole
}

// SummaryBudget identifies the actual hard and desired output capacities.
type SummaryBudget struct {
	MaxTokens    int        `json:"max_tokens"`
	TargetTokens int        `json:"target_tokens"`
	Purpose      Descriptor `json:"purpose"`
}

// SummaryRequest gives the host an owned input and explicit output budget.
type SummaryRequest struct {
	Messages     []Message
	MaxTokens    int
	TargetTokens int
	Purpose      Descriptor
}

func (r SummaryRequest) budget() SummaryBudget {
	return SummaryBudget{MaxTokens: r.MaxTokens, TargetTokens: r.TargetTokens, Purpose: r.Purpose}
}

func (p *BudgetPipeline) summaryPurpose() Descriptor {
	if p.compaction != nil {
		return p.compaction.Policy
	}
	if p.cfg.Compaction != nil {
		return p.cfg.Compaction.Descriptor
	}
	if p.rolling != nil {
		return p.rolling.Descriptor
	}
	return Descriptor{ID: "contexty/summary/whole-block", Revision: "budget-contract"}
}

type summaryRequestKey struct{}
type budgetDecisionsKey struct{}

// BudgetDecision describes the budget stage, before later output transforms.
type BudgetDecision struct {
	HardLimit     int            `json:"hard_limit"`
	TriggerTokens int            `json:"trigger_tokens"`
	TargetTokens  int            `json:"target_tokens"`
	BeforeTokens  int            `json:"before_tokens"`
	AfterTokens   int            `json:"after_tokens"`
	Required      []ContentRef   `json:"required,omitempty"`
	Summary       *SummaryBudget `json:"summary,omitempty"`
	Compacted     bool           `json:"compacted"`
	TargetReached bool           `json:"target_reached"`
}

func (d BudgetDecision) clone() BudgetDecision {
	d.Required = slices.Clone(d.Required)
	if d.Summary != nil {
		copyBudget := *d.Summary
		d.Summary = &copyBudget
	}
	return d
}

func (b SummaryBudget) validate() error {
	if b.MaxTokens < 0 || b.TargetTokens < 0 || b.TargetTokens > b.MaxTokens {
		return ErrInvalidBudgetRequest
	}
	return b.Purpose.Validate()
}

func (d BudgetDecision) validate() error {
	if d.HardLimit < 0 || d.TargetTokens < 0 || d.TriggerTokens < d.TargetTokens ||
		d.TriggerTokens > d.HardLimit || d.BeforeTokens < 0 || d.AfterTokens < 0 ||
		d.AfterTokens > d.HardLimit || d.TargetReached != (d.AfterTokens <= d.TargetTokens) ||
		d.Compacted != (d.Summary != nil) {
		return ErrInvalidBudgetRequest
	}
	for _, ref := range d.Required {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	if d.Summary != nil {
		if d.Summary.MaxTokens > d.HardLimit || d.Summary.TargetTokens > d.TargetTokens {
			return ErrInvalidBudgetRequest
		}
		return d.Summary.validate()
	}
	return nil
}

// BudgetResult includes output messages and explicit soft-target evidence.
type BudgetResult struct {
	Messages []Message
	Decision BudgetDecision
}

// CompileBudgetDecision addresses a main or named-target budget execution.
type CompileBudgetDecision struct {
	Kind     ManifestOutputKind `json:"kind"`
	Target   string             `json:"target"`
	Decision BudgetDecision     `json:"decision"`
}

func recordBudgetDecision(ctx context.Context, decision BudgetDecision) {
	if decisions, ok := ctx.Value(budgetDecisionsKey{}).(map[manifestChannelKey]BudgetDecision); ok {
		decisions[finalBudgetChannel(ctx)] = decision.clone()
	}
}

func compileBudgetDecisions(ctx context.Context) []CompileBudgetDecision {
	decisions, _ := ctx.Value(budgetDecisionsKey{}).(map[manifestChannelKey]BudgetDecision)
	out := make([]CompileBudgetDecision, 0, len(decisions))
	for channel, decision := range decisions {
		out = append(out, CompileBudgetDecision{Kind: channel.kind, Target: channel.name, Decision: decision.clone()})
	}
	slices.SortFunc(out, compareBudgetChannels)
	return out
}

func compareBudgetChannels(a, b CompileBudgetDecision) int {
	if a.Target < b.Target {
		return -1
	}
	if a.Target > b.Target {
		return 1
	}
	if a.Kind < b.Kind {
		return -1
	}
	if a.Kind > b.Kind {
		return 1
	}
	return 0
}

func (p *BudgetPipeline) validateBudgetPolicy() error {
	if p == nil || nilInterfaceValue(p.estimator) || p.cfg.DropHead.MinMessages < 0 {
		return ErrInvalidBudgetRequest
	}
	if (p.cfg.TruncateStrategy != nil && nilInterfaceValue(p.cfg.TruncateStrategy)) ||
		(p.cfg.Summarizer != nil && nilInterfaceValue(p.cfg.Summarizer)) {
		return ErrInvalidBudgetRequest
	}
	if strategy, ok := p.cfg.TruncateStrategy.(*dropHeadStrategy); ok && strategy.cfg.MinMessages < 0 {
		return ErrInvalidBudgetRequest
	}
	if err := validateBuiltinEstimator(p.estimator); err != nil {
		return err
	}
	if err := p.cfg.Retention.validate(); err != nil {
		return err
	}
	if p.cfg.Compaction != nil {
		if err := p.cfg.Compaction.Validate(); err != nil {
			return err
		}
		if p.cfg.Summarizer == nil {
			return ErrInvalidCompactionPolicy
		}
	}
	return p.validateRollingSummary()
}
