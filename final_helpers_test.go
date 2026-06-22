package contexty_test

import (
	"context"

	"github.com/skosovsky/contexty"
)

func fixtureFinalCancellationRequest(
	target bool,
	counter contexty.TokenEstimator,
) (*contexty.Engine, contexty.CompileRequest) {
	input := contexty.TextMessage(contexty.RoleUser, "input")
	input.ID = "input"
	request := contexty.CompileRequest{History: []contexty.Message{input}}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, counter)
	if target {
		request.Targets = []contexty.CompileTarget{{Name: "target", Budget: pipe,
			Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "final"}}
				return messages, nil
			}}}
		return contexty.NewEngine(), request
	}
	request.Options = []contexty.CompileOption{contexty.WithTextReplacement(contexty.TextReplacement{
		Segment: contexty.SegmentHistory, MessageID: "input", Text: "final",
	})}
	return contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe)), request
}
