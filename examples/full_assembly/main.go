// Full-assembly example: Engine + ConversationStore + budget pipeline + Compile.
// Run with: go run .
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/skosovsky/contexty"
)

const (
	fixedTokensPerMsg  = 25
	exampleTokenLimit  = 200
	conversationMinMsg = 2
)

func main() {
	ctx := context.Background()
	payload, err := buildPrompt(ctx)
	if err != nil {
		log.Fatal(err)
	}
	msgs := payload.FlattenMessages()
	fmt.Printf("Compiled %d messages\n", len(msgs))
	for i, m := range msgs {
		fmt.Printf("  [%d] %s: %q\n", i, m.Role, m.TextContent())
	}
}

func buildPrompt(ctx context.Context) (contexty.AbstractPayload, error) {
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
	return engine.Compile(ctx)
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
