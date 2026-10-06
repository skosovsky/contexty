package contexty

import (
	"context"
	"slices"
)

func (p *BudgetPipeline) retentionCodec(ctx context.Context) JSONSerializer {
	if trace := traceFromContext(ctx); trace != nil {
		return trace.profile.Codec
	}
	if reporter, ok := p.estimator.(*EstimateReporter); ok {
		return reporter.codec
	}
	return DefaultJSONSerializer()
}

func (p *BudgetPipeline) selectRequired(
	ctx context.Context,
	messages []Message,
	rounds []ToolRoundObservation,
) ([]bool, error) {
	selected := make([]bool, len(messages))
	ids := make(map[string]bool)
	refs := make(map[ContentRef]bool)
	for i, message := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		selected[i] = slices.Contains(p.fixedIDs, message.ID) || slices.Contains(p.cfg.Retention.Roles, message.Role) ||
			slices.Contains(p.cfg.Retention.MessageIDs, message.ID)
		if slices.Contains(p.cfg.Retention.MessageIDs, message.ID) {
			ids[message.ID] = true
		}
		if len(p.cfg.Retention.ContentRefs) > 0 {
			ref, err := MessageContentRef(message, p.retentionCodec(ctx))
			if err != nil {
				return nil, err
			}
			if slices.Contains(p.cfg.Retention.ContentRefs, ref) {
				selected[i] = true
				refs[ref] = true
			}
		}
		if selected[i] && message.ID == "" {
			return nil, ErrInvalidRetention
		}
	}
	if err := p.cfg.Retention.validateResolved(ids, refs); err != nil {
		return nil, err
	}
	expandRequiredRounds(selected, rounds)
	return selected, nil
}

func (p RetentionPolicy) validateResolved(ids map[string]bool, refs map[ContentRef]bool) error {
	for _, id := range p.MessageIDs {
		if !ids[id] {
			return ErrInvalidRetention
		}
	}
	for _, ref := range p.ContentRefs {
		if !refs[ref] {
			return ErrInvalidRetention
		}
	}
	return nil
}

func expandRequiredRounds(selected []bool, rounds []ToolRoundObservation) {
	for _, round := range rounds {
		protect := false
		for i := round.Start; i <= round.End; i++ {
			protect = protect || selected[i]
		}
		if protect {
			for i := round.Start; i <= round.End; i++ {
				selected[i] = true
			}
		}
	}
}

func protectBudgetSuffix(selected []bool, rounds []ToolRoundObservation, rolling *RollingSummaryPolicy) {
	start := len(selected)
	if rolling != nil {
		start = rollingTailStart(len(selected), *rolling, rounds)
	}
	for _, round := range rounds {
		if round.State == ToolRoundPending && round.Start < start {
			start = round.Start
		}
	}
	for i := start; i < len(selected); i++ {
		selected[i] = true
	}
}

func splitRequired(messages []Message, selected []bool) ([]Message, []Message) {
	var required, evictable []Message
	for i, message := range messages {
		if selected[i] {
			required = append(required, message)
		} else {
			evictable = append(evictable, message)
		}
	}
	return required, evictable
}

func (p *BudgetPipeline) requiredRefs(ctx context.Context, messages []Message) ([]ContentRef, error) {
	var refs []ContentRef
	for _, message := range messages {
		if message.ID == "" {
			return nil, ErrInvalidRetention
		}
		ref, err := MessageContentRef(message, p.retentionCodec(ctx))
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func mergeRequired(before []Message, selected []bool, outputs []Message, summarized bool) ([]Message, error) {
	if !slices.Contains(selected, true) {
		return cloneMessageSlice(outputs), nil
	}
	if err := validateUniqueMessageIDs(before); err != nil {
		return nil, err
	}
	if err := validateUniqueMessageIDs(outputs); err != nil {
		return nil, err
	}
	var out []Message
	consumed := make([]bool, len(outputs))
	inserted := false
	for i, message := range before {
		switch {
		case selected[i]:
			if requiredIDCollision(message.ID, outputs) {
				return nil, ErrInvalidRetention
			}
			out = append(out, message.Clone())
		case summarized:
			if !inserted {
				out = append(out, cloneMessageSlice(outputs)...)
				inserted = true
			}
		default:
			if takeMatchingOutput(message, outputs, consumed) {
				out = append(out, message.Clone())
			}
		}
	}
	if !summarized && slices.Contains(consumed, false) {
		return nil, ErrInvalidRetention
	}
	return out, nil
}

func requiredIDCollision(id string, outputs []Message) bool {
	if id == "" {
		return false
	}
	for _, output := range outputs {
		if output.ID == id {
			return true
		}
	}
	return false
}

func takeMatchingOutput(message Message, outputs []Message, consumed []bool) bool {
	for i, output := range outputs {
		if !consumed[i] && MessageEqual(output, message) {
			consumed[i] = true
			return true
		}
	}
	return false
}

func (p *BudgetPipeline) validateRequiredOutput(ctx context.Context, messages []Message, refs []ContentRef) error {
	previous := -1
	for _, required := range refs {
		first, err := p.requiredPosition(ctx, messages, required)
		if err != nil {
			return err
		}
		if first <= previous {
			return ErrInvalidRetention
		}
		previous = first
	}
	return nil
}

func (p *BudgetPipeline) requiredPosition(ctx context.Context, messages []Message, required ContentRef) (int, error) {
	first := -1
	for i, message := range messages {
		if message.ID != required.ID {
			continue
		}
		ref, err := MessageContentRef(message, p.retentionCodec(ctx))
		if err != nil {
			return -1, err
		}
		if ref != required {
			return -1, ErrInvalidRetention
		}
		if first < 0 {
			first = i
		}
	}
	return first, nil
}

func (p *BudgetPipeline) validateRecordedRetention(ctx context.Context, messages []Message) error {
	decisions, _ := ctx.Value(budgetDecisionsKey{}).(map[manifestChannelKey]BudgetDecision)
	return p.validateRequiredOutput(ctx, messages, decisions[finalBudgetChannel(ctx)].Required)
}
