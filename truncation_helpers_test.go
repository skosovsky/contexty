package contexty_test

import (
	"context"

	"github.com/skosovsky/contexty"
)

func fixtureTruncationEngine(pipe *contexty.BudgetPipeline) *contexty.Engine {
	return contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(func(context.Context, contexty.CaptureCandidate) (bool, error) { return true, nil })))
}

type fixtureHostStrategy struct{ calls *int }

func (s fixtureHostStrategy) Apply(_ context.Context, messages []contexty.Message, _, _ int,
	_ contexty.TokenEstimator) ([]contexty.Message, error) {
	*s.calls++
	return messages, nil
}
