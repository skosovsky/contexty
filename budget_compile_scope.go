package contexty

import (
	"context"
	"slices"
)

func (p *BudgetPipeline) inspectBudgetRounds(messages []Message) ([]ToolRoundObservation, error) {
	if len(p.fixedIDs) == 0 {
		return InspectToolRoundStates(messages, nil)
	}
	input := slices.Clone(messages)
	for index, message := range input {
		if slices.Contains(p.fixedIDs, message.ID) {
			input[index].Role = RoleSystem
			input[index].Parts = nil
		}
	}
	return InspectToolRoundStates(input, nil)
}

func (p *BudgetPipeline) explicitRequiredMessages(ctx context.Context, messages []Message) ([]Message, error) {
	var out []Message
	for _, message := range messages {
		explicit := !slices.Contains(p.fixedIDs, message.ID) ||
			slices.Contains(p.cfg.Retention.MessageIDs, message.ID) ||
			slices.Contains(p.cfg.Retention.Roles, message.Role)
		if !explicit && len(p.cfg.Retention.ContentRefs) > 0 {
			ref, err := MessageContentRef(message, p.retentionCodec(ctx))
			if err != nil {
				return nil, err
			}
			explicit = slices.Contains(p.cfg.Retention.ContentRefs, ref)
		}
		if explicit {
			out = append(out, message)
		}
	}
	return out, nil
}

func (p *BudgetPipeline) protectCompileBudgetSuffix(
	messages []Message,
	selected []bool,
	rounds []ToolRoundObservation,
) {
	if len(p.fixedIDs) == 0 || p.rolling == nil {
		protectBudgetSuffix(selected, rounds, p.rolling)
		return
	}
	var history []Message
	var indexes []int
	for index, message := range messages {
		if !slices.Contains(p.fixedIDs, message.ID) {
			history = append(history, message)
			indexes = append(indexes, index)
		}
	}
	historyRounds, _ := InspectToolRoundStates(history, nil) // Already validated in the full history scope.
	historySelected := make([]bool, len(history))
	protectBudgetSuffix(historySelected, historyRounds, p.rolling)
	for index, protect := range historySelected {
		if protect {
			selected[indexes[index]] = true
		}
	}
}
