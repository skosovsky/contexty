package contexty_test

import (
	"context"

	"github.com/skosovsky/contexty"
)

func fixtureCancelTransformFixture(
	stage string,
	cancel context.CancelFunc,
	callbackError error,
) (*contexty.Engine, contexty.CompileRequest) {
	request := contexty.CompileRequest{History: []contexty.Message{fixtureRollingText("input", "long input")}}
	formatter := func(context.Context, []contexty.Message) ([]contexty.Message, error) {
		cancel()
		return nil, callbackError
	}
	switch stage {
	case "hook":
		return contexty.NewEngine(contexty.WithTransformHooks(fixtureDeferredHook(
			func(context.Context, contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
				cancel()
				return contexty.ConversationSnapshot{}, callbackError
			}))), request
	case "role":
		return contexty.NewEngine(contexty.WithRoleProjectionPolicy(contexty.RoleProjectionFunc(
			func(contexty.Message) (contexty.Role, error) {
				cancel()
				return contexty.RoleUser, callbackError
			}))), request
	case "segment":
		return contexty.NewEngine(contexty.WithSegmentFormatter(contexty.SegmentHistory, formatter)), request
	case "target":
		request.Targets = []contexty.CompileTarget{{Name: "target", Formatter: formatter}}
		return contexty.NewEngine(), request
	default:
		pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1),
			Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
				cancel()
				return contexty.Message{}, callbackError
			})}, contexty.CharTokenEstimator{})
		return contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe)), request
	}
}
