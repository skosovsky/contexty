package contexty_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/skosovsky/contexty"
)

type loggingFormatter struct {
	next contexty.Formatter
}

func (f loggingFormatter) Format(ctx context.Context, blocks []contexty.NamedBlock) ([]contexty.Message, error) {
	msgs, err := f.next.Format(ctx, blocks)
	if err != nil {
		return nil, err
	}
	fmt.Printf("blocks=%d messages=%d\n", len(blocks), len(msgs))
	return msgs, nil
}

type loggingStrategy struct {
	next contexty.EvictionStrategy
}

func (s loggingStrategy) Apply(ctx context.Context, msgs []contexty.Message, originalTokens int, limit int, counter contexty.TokenCounter) ([]contexty.Message, error) {
	out, err := s.next.Apply(ctx, msgs, originalTokens, limit, counter)
	if err != nil {
		return nil, err
	}
	after, err := counter.Count(ctx, out)
	if err != nil {
		return nil, err
	}
	fmt.Printf("before=%d after=%d\n", originalTokens, after)
	return out, nil
}

func applyEvictionMiddleware(strategy contexty.EvictionStrategy, middlewares ...contexty.EvictionMiddleware) contexty.EvictionStrategy {
	wrapped := strategy
	for i := len(middlewares) - 1; i >= 0; i-- {
		wrapped = middlewares[i](wrapped)
	}
	return wrapped
}

func applyFormatterMiddleware(formatter contexty.Formatter, middlewares ...contexty.FormatterMiddleware) contexty.Formatter {
	wrapped := formatter
	for i := len(middlewares) - 1; i >= 0; i-- {
		wrapped = middlewares[i](wrapped)
	}
	return wrapped
}

func messageText(msg contexty.Message) string {
	var builder strings.Builder
	for _, part := range msg.Content {
		if part.Type == "text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func ExampleBuilder_Build() {
	builder := contexty.NewBuilder(30, &contexty.FixedCounter{TokensPerMessage: 10})
	builder.AddBlock("instructions", contexty.MemoryBlock{
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage("system", "You are helpful."),
		},
	})
	builder.AddBlock("conversation", contexty.MemoryBlock{
		Strategy: contexty.NewTruncateOldestStrategy(),
		Messages: []contexty.Message{
			contexty.TextMessage("user", "hello"),
			contexty.TextMessage("assistant", "hi"),
			contexty.TextMessage("user", "need a summary"),
		},
	})

	msgs, err := builder.Build(context.Background())
	if err != nil {
		return
	}
	fmt.Printf("messages=%d last=%s\n", len(msgs), messageText(msgs[len(msgs)-1]))
	// Output: messages=3 last=need a summary
}

func ExampleEvictionMiddleware() {
	loggingMiddleware := contexty.EvictionMiddleware(func(next contexty.EvictionStrategy) contexty.EvictionStrategy {
		return loggingStrategy{next: next}
	})
	strategy := applyEvictionMiddleware(contexty.NewDropTailStrategy(), loggingMiddleware)
	counter := &contexty.FixedCounter{TokensPerMessage: 10}
	msgs := []contexty.Message{
		contexty.TextMessage("system", "first"),
		contexty.TextMessage("system", "second"),
		contexty.TextMessage("system", "third"),
	}

	out, err := strategy.Apply(context.Background(), msgs, 30, 20, counter)
	if err != nil {
		return
	}
	fmt.Printf("kept=%d\n", len(out))
	// Output:
	// before=30 after=20
	// kept=2
}

func ExampleFormatterMiddleware() {
	loggingMiddleware := contexty.FormatterMiddleware(func(next contexty.Formatter) contexty.Formatter {
		return loggingFormatter{next: next}
	})

	builder := contexty.NewBuilder(20, &contexty.FixedCounter{TokensPerMessage: 10})
	builder.AddBlock("instructions", contexty.MemoryBlock{
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "A")},
	})
	builder.AddBlock("notes", contexty.MemoryBlock{
		Strategy: contexty.NewStrictStrategy(),
		Messages: []contexty.Message{contexty.TextMessage("system", "B")},
	})
	builder.WithFormatter(applyFormatterMiddleware(contexty.DefaultFormatter{}, loggingMiddleware))

	msgs, err := builder.Build(context.Background())
	if err != nil {
		return
	}
	fmt.Println(messageText(msgs[0]))
	fmt.Println(messageText(msgs[1]))
	// Output:
	// blocks=2 messages=2
	// A
	// B
}
