// Full-assembly example: builds named blocks in registration order and runs Build
// within a token budget. Total content exceeds the limit so that multiple
// strategies are visible. Run with: go run .
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/skosovsky/contexty"
)

func main() {
	ctx := context.Background()
	msgs, err := buildPrompt(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Built %d messages\n", len(msgs))
	for i, m := range msgs {
		fmt.Printf("  [%d] %s: %q\n", i, m.Role, trunc(contentText(m.Content), 60))
	}
}

func contentText(parts []contexty.ContentPart) string {
	for _, p := range parts {
		if p.Type == "text" {
			return p.Text
		}
	}
	return ""
}

func buildPrompt(ctx context.Context) ([]contexty.Message, error) {
	// FixedCounter so total size is predictable; total content exceeds maxTokens to trigger evictions.
	counter := &contexty.FixedCounter{TokensPerMessage: 25}
	const maxTokens = 200

	builder := contexty.NewBuilder(maxTokens, counter)

	builder.AddBlock("instructions", contexty.MemoryBlock{
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "You are a medical assistant.")},
	})

	builder.AddBlock("profile", contexty.MemoryBlock{
		Strategy: contexty.NewDropStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "Patient Name: Anna. Age: 30.")},
	})

	builder.AddBlock("reference_material", contexty.MemoryBlock{
		Strategy:  contexty.NewDropTailStrategy(),
		MaxTokens: 75,
		Messages:  fetchReferenceMessages(),
	})

	builder.AddBlock("conversation", contexty.MemoryBlock{
		Strategy: contexty.NewTruncateOldestStrategy(
			contexty.KeepTurnAtomicity(true),
			contexty.MinMessages(2),
		),
		Messages: fetchConversation(),
	})

	finalMessages, err := builder.Build(ctx)
	if err != nil {
		return nil, err
	}

	used, err := counter.Count(ctx, finalMessages)
	if err != nil {
		return nil, err
	}
	log.Printf("built %d messages, used %d/%d tokens", len(finalMessages), used, maxTokens)

	return finalMessages, nil
}

func fetchReferenceMessages() []contexty.Message {
	return []contexty.Message{
		contexty.TextMessage("system", "Retrieved: Article about vitamin D and calcium."),
		contexty.TextMessage("system", "Retrieved: Summary on magnesium and sleep."),
		contexty.TextMessage("system", "Retrieved: Guidelines for daily intake."),
		contexty.TextMessage("system", "Retrieved: Drug interactions with supplements."),
	}
}

func fetchConversation() []contexty.Message {
	return []contexty.Message{
		contexty.TextMessage("user", "What supplements should I take?"),
		contexty.TextMessage("assistant", "Consider vitamin D and calcium based on your profile."),
		contexty.TextMessage("user", "Any side effects?"),
		contexty.TextMessage("assistant", "Generally well tolerated. Discuss with your doctor."),
		contexty.TextMessage("user", "Can I take them at night?"),
		contexty.TextMessage("assistant", "Vitamin D can be taken anytime. Magnesium may help sleep."),
		contexty.TextMessage("user", "Thanks."),
		contexty.TextMessage("assistant", "You're welcome. Ask if you need more."),
	}
}

func trunc(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "..."
}
