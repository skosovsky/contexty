package contexty_test

import (
	"context"
	"fmt"

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
		Messages: []contexty.Message{{Role: "system", Content: "You are helpful."}},
	})
	msgs, report, err := builder.Compile(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("messages: %d, tokens: %d\n", len(msgs), report.TotalTokensUsed)
	// Output: messages: 1, tokens: 6
}

func ExampleBuilder_Compile() {
	counter := &contexty.CharFallbackCounter{CharsPerToken: 4}
	b := contexty.NewBuilder(contexty.AllocatorConfig{MaxTokens: 50, TokenCounter: counter})
	b.AddBlock(contexty.MemoryBlock{
		ID: "core", Tier: contexty.TierCore, Strategy: contexty.NewDropStrategy(),
		Messages: []contexty.Message{{Role: "system", Content: "User: Alice"}},
	})
	b.AddBlock(contexty.MemoryBlock{
		ID: "history", Tier: contexty.TierHistory, Strategy: contexty.NewTruncateOldestStrategy(),
		Messages: []contexty.Message{
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hello!"},
		},
	})
	msgs, report, err := b.Compile(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("msgs=%d evictions=%v\n", len(msgs), report.Evictions)
	// Output: msgs=3 evictions=map[]
}

func ExampleInjectIntoSystem() {
	sys := contexty.Message{Role: "system", Content: "You are a doctor."}
	got := contexty.InjectIntoSystem(sys,
		contexty.Message{Content: "Patient has fever."},
		contexty.Message{Content: "Allergies: none."},
	)
	fmt.Println(got.Role)
	fmt.Println(len(got.Content) > 0 && got.Content[0] == 'Y')
	// Output:
	// system
	// true
}
