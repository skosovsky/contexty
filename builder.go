package contexty

import (
	"context"
	"fmt"
)

type registeredBlock struct {
	name  string
	block MemoryBlock
}

// Builder collects named memory blocks and builds them into a final message slice within the token budget.
// A Builder can be reused: call AddBlock and Build multiple times. Each Build uses the current
// list of registered blocks (blocks are not cleared after Build). For a fresh build, create a new Builder.
type Builder struct {
	maxTokens int
	counter   TokenCounter
	formatter Formatter
	blocks    []registeredBlock
}

// NewBuilder returns a new Builder with the given token budget and counter.
// Arguments are validated when Build is called.
func NewBuilder(maxTokens int, counter TokenCounter) *Builder {
	return &Builder{
		maxTokens: maxTokens,
		counter:   counter,
	}
}

// AddBlock appends a named block and returns the builder for chaining.
// The builder snapshots the provided block so later caller mutation does not
// affect future builds. Panics if name is empty.
func (b *Builder) AddBlock(name string, block MemoryBlock) *Builder {
	if name == "" {
		panic("contexty: AddBlock called with empty name")
	}
	b.blocks = append(b.blocks, registeredBlock{
		name:  name,
		block: cloneBlock(block),
	})
	return b
}

// WithFormatter sets the formatter used to assemble post-eviction blocks.
// Panics if f is nil.
func (b *Builder) WithFormatter(f Formatter) *Builder {
	if f == nil {
		panic("contexty: WithFormatter called with nil Formatter")
	}
	b.formatter = f
	return b
}

// Build assembles all blocks into a final []Message that fits within maxTokens.
// Blocks are budgeted strictly in AddBlock registration order. Each build
// creates fresh post-eviction snapshots before invoking the formatter, so the
// builder remains repeatable even if strategies or formatters mutate their input.
func (b *Builder) Build(ctx context.Context) ([]Message, error) {
	if b.maxTokens <= 0 || b.counter == nil {
		return nil, ErrInvalidConfig
	}
	counter := b.counter
	remaining := b.maxTokens
	postEviction := make([]NamedBlock, 0, len(b.blocks))

	for _, registered := range b.blocks {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("contexty: build: %w", err)
		}
		block := cloneBlock(registered.block)
		if len(block.Messages) == 0 {
			continue
		}
		if block.Strategy == nil {
			return nil, fmt.Errorf("block %q: %w", registered.name, ErrNilStrategy)
		}
		blockTokens, err := counter.Count(ctx, block.Messages)
		if err != nil {
			return nil, fmt.Errorf("block %q: %w: %w", registered.name, ErrTokenCountFailed, err)
		}

		blockBudget := remaining
		if block.MaxTokens > 0 && block.MaxTokens < blockBudget {
			blockBudget = block.MaxTokens
		}
		var out []Message
		if blockTokens <= blockBudget {
			out = cloneMessages(block.Messages)
		} else {
			out, err = block.Strategy.Apply(ctx, block.Messages, blockTokens, blockBudget, counter)
			if err != nil {
				return nil, fmt.Errorf("block %q: %w", registered.name, err)
			}
		}
		if len(out) > 0 {
			out = cloneMessages(out)
			if len(block.CacheControl) > 0 {
				lastIdx := len(out) - 1
				out[lastIdx].CacheControl = cloneMap(block.CacheControl)
			}
			used, err := counter.Count(ctx, out)
			if err != nil {
				return nil, fmt.Errorf("block %q: %w: %w", registered.name, ErrTokenCountFailed, err)
			}
			if used > blockBudget {
				return nil, fmt.Errorf("block %q: %w", registered.name, ErrStrategyExceededBudget)
			}
			remaining -= used
			postEviction = append(postEviction, NamedBlock{
				Name: registered.name,
				Block: MemoryBlock{
					Strategy:     block.Strategy,
					Messages:     out,
					MaxTokens:    block.MaxTokens,
					CacheControl: cloneMap(block.CacheControl),
				},
			})
		}
	}

	formatter := b.formatter
	if formatter == nil {
		formatter = DefaultFormatter{}
	}
	finalMessages, err := formatter.Format(ctx, cloneNamedBlocks(postEviction))
	if err != nil {
		return nil, fmt.Errorf("contexty: format: %w", err)
	}
	finalTokens, err := counter.Count(ctx, finalMessages)
	if err != nil {
		return nil, fmt.Errorf("contexty: format: %w: %w", ErrTokenCountFailed, err)
	}
	if finalTokens > b.maxTokens {
		return nil, fmt.Errorf("contexty: format: %w", ErrFormatterExceededBudget)
	}
	return finalMessages, nil
}
