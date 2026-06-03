// Full-assembly example: Engine + ConversationStore + budget pipeline + Compile.
// Run with: go run .
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/skosovsky/contexty"
)

const (
	fixedTokensPerMsg  = 25
	exampleTokenLimit  = 200
	conversationMinMsg = 2
)

func main() {
	ctx := context.Background()
	result, err := buildPrompt(ctx)
	if err != nil {
		log.Fatal(err)
	}
	msgs := result.Payload.FlattenMessages()
	fmt.Printf("Compiled %d messages\n", len(msgs))
	for i, m := range msgs {
		fmt.Printf("  [%d] %s: %q\n", i, m.Role, m.TextContent())
	}
}

func buildPrompt(ctx context.Context) (contexty.CompileResult, error) {
	store := contexty.NewMemoryConversationStore()
	s0, _ := store.Load(ctx, "demo")
	_ = store.UpdateSegment(ctx, "demo", s0.Version(), contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "You are a medical assistant."),
	})
	_ = store.UpdateSegment(ctx, "demo", 1, contexty.SegmentMemory, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "Patient Name: Anna. Age: 30."),
	})
	_ = store.UpdateSegment(ctx, "demo", 2, contexty.SegmentHistory, fetchConversation())

	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{ //nolint:exhaustruct // optional Summarizer/TruncateStrategy omitted
			TokenLimit: exampleTokenLimit,
			DropHead:   contexty.DropHeadConfig{MinMessages: conversationMinMsg},
		}, &contexty.FixedEstimator{TokensPerMessage: fixedTokensPerMsg})

	engine := contexty.NewEngine(
		contexty.WithConversationID("demo"),
		contexty.WithStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(compileObserver{}),
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "session_hint",
			Segment: contexty.SegmentSystem,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{
					contexty.TextMessage(contexty.RoleSystem, "Session locale: en-US"),
				}, nil
			},
		}),
	)
	return engine.Compile(ctx, contexty.CompileRequest{ //nolint:exhaustruct // only Pending for this example
		Pending: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "What should I recommend for Anna?"),
		},
	})
}

type compileObserver struct{}

func (compileObserver) OnTokensEstimated(_ context.Context, blockID string, count int) {
	log.Printf("tokens estimated: block=%s count=%d", blockID, count)
}

func (compileObserver) OnNodeEvicted(_ context.Context, nodeID string, reason contexty.EvictionReason) {
	log.Printf("node evicted: id=%s reason=%s", nodeID, reason)
}

func (compileObserver) OnContextSummarized(_ context.Context, ratio float64) {
	log.Printf("context summarized: ratio=%.2f", ratio)
}

func (compileObserver) OnPipelineCompiled(_ context.Context, totalCost int, duration time.Duration) {
	log.Printf("pipeline compiled: cost=%d duration=%s", totalCost, duration)
}

func fetchConversation() []contexty.Message {
	return []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "What supplements should I take?"),
		contexty.TextMessage(contexty.RoleAssistant, "Consider vitamin D and calcium based on your profile."),
		contexty.TextMessage(contexty.RoleUser, "Any side effects?"),
		contexty.TextMessage(contexty.RoleAssistant, "Generally well tolerated. Discuss with your doctor."),
		contexty.TextMessage(contexty.RoleUser, "Thanks."),
		contexty.TextMessage(contexty.RoleAssistant, "You're welcome."),
	}
}
