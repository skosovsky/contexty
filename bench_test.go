package contexty_test

import (
	"context"
	"testing"

	"github.com/skosovsky/contexty"
)

func BenchmarkRender_LLMXML(b *testing.B) {
	ctx := context.Background()
	msgs := make([]contexty.Message, 100)
	for i := range msgs {
		msgs[i] = contexty.TextMessage(contexty.RoleUser, "benchmark message content")
	}
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, msgs)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = contexty.Render(ctx, snap, contexty.ViewLLMXML)
	}
}

func BenchmarkRedactionHook(b *testing.B) {
	ctx := context.Background()
	msgs := make([]contexty.Message, 50)
	for i := range msgs {
		msgs[i] = contexty.TextMessage(contexty.RoleUser, "user@example.com says hello")
	}
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, msgs)
	hook := contexty.NewRedactionHook()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = hook.Transform(ctx, snap)
	}
}

func BenchmarkBudgetPipeline_Truncate(b *testing.B) {
	ctx := context.Background()
	msgs := make([]contexty.Message, 200)
	for i := range msgs {
		msgs[i] = contexty.TextMessage(contexty.RoleUser, "token heavy message body")
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		TokenLimit:       500,
		TruncateStrategy: contexty.NewDropHeadStrategy(contexty.DropHeadConfig{}),
	}, contexty.CharTokenEstimator{})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = pipe.Apply(ctx, msgs)
	}
}
