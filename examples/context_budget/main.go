// Context budgeting example with required content, completed tool rounds and a recent tail.
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/skosovsky/contexty"
)

// fixtureSummary stands in for a host summarizer; it does not claim LLM quality.
type fixtureSummary struct{}

func (fixtureSummary) Summarize(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
	text := []rune("Earlier lookups finished; originals remain in the host archive.")
	text = text[:min(len(text), request.MaxTokens)]
	message := contexty.TextMessage(contexty.RoleAssistant, string(text))
	message.ID = "summary"
	return message, nil
}

func main() {
	const triggerPercent, targetPercent = 80, 50
	const evidenceRepeats, hardLimit = 8, 300
	rule := contexty.TextMessage(contexty.RoleSystem, "Do not change production data.")
	rule.ID = "rule"
	messages := []contexty.Message{rule}
	for i := range 12 {
		messages = append(messages,
			contexty.Message{
				ID:   "call-" + strconv.Itoa(i),
				Role: contexty.RoleAssistant,
				Parts: []contexty.ContentPart{
					contexty.ToolCallPart{
						ID:            strconv.Itoa(i),
						Name:          "lookup",
						Arguments:     contexty.TextPayload("read-only"),
						ArgumentsBlob: nil,
					},
				},
			},
			contexty.Message{
				ID:   "result-" + strconv.Itoa(i),
				Role: contexty.RoleTool,
				Parts: []contexty.ContentPart{
					contexty.ToolResultPart{
						ToolCallID: strconv.Itoa(i),
						Name:       "lookup",
						Payload:    contexty.TextPayload(strings.Repeat("evidence ", evidenceRepeats)),
						IsError:    false,
					},
				},
			})
	}
	current := contexty.TextMessage(contexty.RoleUser, "Continue from the last result.")
	current.ID = "current"
	messages = append(messages, current)
	//nolint:exhaustruct_v5 // Only the explicit budget and retention features are configured.
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget:     contexty.EffectiveInputBudget(hardLimit),
		Retention:  contexty.RetentionPolicy{MessageIDs: []string{"rule"}},
		Summarizer: fixtureSummary{},
		Compaction: &contexty.CompactionPolicy{
			Descriptor:     contexty.Descriptor{ID: "host/session-summary", Revision: "1"},
			TriggerPercent: triggerPercent,
			TargetPercent:  targetPercent,
		},
	},
		contexty.CharTokenEstimator{}, contexty.WithRollingSummary(contexty.RollingSummaryPolicy{
			Descriptor: contexty.Descriptor{ID: "host/recent-tail", Revision: "1"}, RecentMessages: 2}))
	result, err := pipe.Apply(context.Background(), messages)
	if err != nil {
		panic(err)
	}
	fmt.Printf("messages=%d before=%d after=%d hard=%d target=%d reached=%t\n",
		len(result.Messages), result.Decision.BeforeTokens, result.Decision.AfterTokens,
		result.Decision.HardLimit, result.Decision.TargetTokens, result.Decision.TargetReached)
}
