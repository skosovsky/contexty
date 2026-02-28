// Full-assembly example: builds a multi-tier context (system, core, RAG, history)
// and compiles it within a token budget. Run with: go run .
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/skosovsky/contexty"
)

func main() {
	ctx := context.Background()
	msgs, err := buildAgentContext(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Compiled %d messages\n", len(msgs))
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

func buildAgentContext(ctx context.Context) ([]contexty.Message, error) {
	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}

	builder := contexty.NewBuilder(contexty.AllocatorConfig{
		MaxTokens:    4000,
		TokenCounter: counter,
	})

	builder.AddBlock(contexty.MemoryBlock{
		ID:       "persona",
		Tier:     contexty.TierSystem,
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "You are a medical assistant.")},
	})

	builder.AddBlock(contexty.MemoryBlock{
		ID:       "patient_card",
		Tier:     contexty.TierCore,
		Strategy: contexty.NewDropStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "Patient Name: Anna. Age: 30.")},
	})

	builder.AddBlock(contexty.MemoryBlock{
		ID:       "rag_results",
		Tier:     contexty.TierRAG,
		Strategy: contexty.NewDropStrategy(),
		Messages: fetchRAGMessages(),
	})

	builder.AddBlock(contexty.MemoryBlock{
		ID:   "chat_history",
		Tier: contexty.TierHistory,
		Strategy: contexty.NewTruncateOldestStrategy(
			contexty.KeepUserAssistantPairs(true),
			contexty.MinMessages(2),
		),
		Messages: fetchChatHistoryFromDB(),
	})

	finalMessages, report, err := builder.Compile(ctx)
	if err != nil {
		return nil, err
	}

	log.Printf("tokens used: %d/%d, evictions: %v, dropped: %v",
		report.TotalTokensUsed, 4000, report.Evictions, report.BlocksDropped)

	return finalMessages, nil
}

func fetchRAGMessages() []contexty.Message {
	return []contexty.Message{
		contexty.TextMessage("system", "Retrieved: Article about vitamin D and calcium."),
	}
}

func fetchChatHistoryFromDB() []contexty.Message {
	return []contexty.Message{
		contexty.TextMessage("user", "What supplements should I take?"),
		contexty.TextMessage("assistant", "Consider vitamin D and calcium based on your profile."),
		contexty.TextMessage("user", "Any side effects?"),
		contexty.TextMessage("assistant", "Generally well tolerated. Discuss with your doctor."),
	}
}

func trunc(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "..."
}
