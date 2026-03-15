package contexty_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/skosovsky/contexty"
)

func ExampleNewBuilder() {
	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
	builder := contexty.NewBuilder(contexty.AllocatorConfig{
		MaxTokens:    100,
		TokenCounter: counter,
	})
	builder.AddBlock(contexty.MemoryBlock{
		ID: "sys", Tier: contexty.TierSystem, Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "You are helpful.")},
	})
	msgs, report, err := builder.Compile(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("messages: %d, tokens: %d\n", len(msgs), report.TotalTokensUsed)
	// Output: messages: 1, tokens: 4
}

func ExampleBuilder_Compile() {
	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
	b := contexty.NewBuilder(contexty.AllocatorConfig{MaxTokens: 50, TokenCounter: counter})
	b.AddBlock(contexty.MemoryBlock{
		ID: "core", Tier: contexty.TierCore, Strategy: contexty.NewDropStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "User: Alice")},
	})
	b.AddBlock(contexty.MemoryBlock{
		ID: "history", Tier: contexty.TierHistory, Strategy: contexty.NewTruncateOldestStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage("user", "Hi"),
			contexty.TextMessage("assistant", "Hello!"),
		},
	})
	msgs, report, err := b.Compile(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("msgs=%d evictions=%v\n", len(msgs), report.Evictions)
	// Output: msgs=3 evictions=map[]
}

func Example_builderChatHistory() {
	// System (1 msg) + history (6 msgs); 50 tokens each = 350 total, limit 300 -> truncate removes 1.
	counter := &contexty.FixedCounter{TokensPerMessage: 50}
	b := contexty.NewBuilder(contexty.AllocatorConfig{MaxTokens: 300, TokenCounter: counter})
	b.AddBlock(contexty.MemoryBlock{
		ID: "sys", Tier: contexty.TierSystem, Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "You are helpful.")},
	})
	history := []contexty.Message{
		contexty.TextMessage("user", "hi"),
		contexty.TextMessage("assistant", "hello"),
		contexty.TextMessage("user", "hi"),
		contexty.TextMessage("assistant", "hello"),
		contexty.TextMessage("user", "hi"),
		contexty.TextMessage("assistant", "hello"),
	}
	b.AddBlock(contexty.MemoryBlock{
		ID: "history", Tier: contexty.TierHistory, Strategy: contexty.NewTruncateOldestStrategy(),
		Messages: history,
	})
	msgs, report, err := b.Compile(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("messages: %d, tokens: %d, eviction(history)=%q\n",
		len(msgs), report.TotalTokensUsed, report.Evictions["history"])
	// Output: messages: 6, tokens: 300, eviction(history)="truncated"
}

func Example_injectIntoSystemXML() {
	sys := contexty.TextMessage("system", "Base.")
	got := contexty.InjectIntoSystem(sys, contexty.XMLFormatter("context"),
		contexty.Message{Content: []contexty.ContentPart{{Type: "text", Text: "Fact1"}}},
		contexty.Message{Content: []contexty.ContentPart{{Type: "text", Text: "Fact2"}}},
	)
	fmt.Println(got.Role)
	fmt.Println(got.Content[0].Text == "Base.")
	fmt.Println(len(got.Content) == 2 && strings.Contains(got.Content[1].Text, "<context>") && strings.Contains(got.Content[1].Text, "<fact>"))
	// Output:
	// system
	// true
	// true
}

func ExampleInjectIntoSystem() {
	sys := contexty.TextMessage("system", "You are a doctor.")
	got := contexty.InjectIntoSystem(sys, contexty.XMLFormatter("context"),
		contexty.Message{Content: []contexty.ContentPart{{Type: "text", Text: "Patient has fever."}}},
		contexty.Message{Content: []contexty.ContentPart{{Type: "text", Text: "Allergies: none."}}},
	)
	fmt.Println(got.Role)
	fmt.Println(len(got.Content) > 0 && len(got.Content[0].Text) > 0 && got.Content[0].Text[0] == 'Y')
	// Output:
	// system
	// true
}

func ExampleInjectIntoSystem_markdown() {
	sys := contexty.TextMessage("system", "Base instructions.")
	got := contexty.InjectIntoSystem(sys, contexty.MarkdownListFormatter("Context:"),
		contexty.Message{Content: []contexty.ContentPart{{Type: "text", Text: "Fact A"}}},
		contexty.Message{Content: []contexty.ContentPart{{Type: "text", Text: "Fact B"}}},
	)
	// Injected facts are in the second ContentPart (non-destructive append).
	injected := got.Content[1].Text
	fmt.Println(got.Role)
	fmt.Println(strings.Contains(injected, "### Context:\n"))
	fmt.Println(strings.Contains(injected, "- Fact A\n"))
	fmt.Println(strings.Contains(injected, "- Fact B\n"))
	// Output:
	// system
	// true
	// true
	// true
}
