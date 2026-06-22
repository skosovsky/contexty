package contexty

import "errors"

var (
	ErrInvalidRollingSummary   = errors.New("contexty: invalid rolling summary recipe")
	ErrRecentTailExceedsBudget = errors.New("contexty: protected recent tail exceeds token budget")
)

// RollingSummaryPolicy preserves at least RecentMessages chronological messages.
// Atomic rounds or earlier pending calls may extend that tail backward.
type RollingSummaryPolicy struct {
	Descriptor     Descriptor `json:"descriptor"`
	RecentMessages int        `json:"recent_messages"`
}

func (p RollingSummaryPolicy) Validate() error {
	if p.RecentMessages <= 0 {
		return ErrInvalidRollingSummary
	}
	return p.Descriptor.Validate()
}

// WithRollingSummary replaces whole-block compression with summary + recent tail.
// It never silently truncates the preserved tail or the resulting summary.
func WithRollingSummary(policy RollingSummaryPolicy) BudgetPipelineOption {
	return func(p *BudgetPipeline) {
		copyPolicy := policy
		p.rolling = &copyPolicy
	}
}

func cloneRollingSummary(policy *RollingSummaryPolicy) *RollingSummaryPolicy {
	if policy == nil {
		return nil
	}
	copyPolicy := *policy
	return &copyPolicy
}

func (p *BudgetPipeline) validateRollingSummary() error {
	if p.rolling == nil {
		return nil
	}
	if err := p.rolling.Validate(); err != nil {
		return err
	}
	if p.cfg.Summarizer == nil {
		return ErrInvalidRollingSummary
	}
	if p.compaction != nil && p.compaction.Policy != p.rolling.Descriptor {
		return ErrInvalidCompaction
	}
	return nil
}

func rollingTailStart(count int, policy RollingSummaryPolicy, rounds []ToolRoundObservation) int {
	start := 0
	if policy.RecentMessages < count {
		start = count - policy.RecentMessages
	}
	for _, round := range rounds {
		if (round.State == ToolRoundPending && round.Start < start) ||
			(round.Start < start && start <= round.End) {
			start = round.Start
		}
	}
	return start
}

func validateRollingSummaryIdentity(summary Message, inputs []Message) error {
	if summary.ID == "" {
		return nil
	}
	for _, input := range inputs {
		if input.ID == summary.ID {
			return ErrInvalidRollingSummary
		}
	}
	return nil
}
