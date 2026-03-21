package contexty

import (
	"context"
	"errors"
	"fmt"
)

type registeredBlock struct {
	name  string
	block MemoryBlock
}

// Builder collects named memory blocks and builds them into a final message slice within the token budget.
//
// Concurrency: Builder is not safe for concurrent mutation. Do not call AddBlock, WithFormatter, or
// SetBlockMessages from multiple goroutines on the same instance without external synchronization.
// For concurrent request handling, build a template with NewBuilder + AddBlock, then call Clone per
// request or goroutine and run Build/BuildDetailed on the copy.
//
// A Builder can be reused on one goroutine: call AddBlock and Build multiple times. Each Build uses
// the current list of registered blocks (blocks are not cleared after Build). For a fresh build,
// create a new Builder or Clone from a template.
type Builder struct {
	maxTokens int
	counter   TokenCounter
	formatter Formatter
	blocks    []registeredBlock
}

// BuildResult contains the final formatted output plus raw post-eviction block
// snapshots for orchestration and persistence.
type BuildResult struct {
	Messages []Message
	Blocks   []NamedBlock
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
// affect future builds.
//
// Fail-fast: panics if name is empty (programming error). For API that returns
// errors instead, validate the name before calling AddBlock.
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

// Clone returns a deep-copied builder snapshot that can be mutated independently.
func (b *Builder) Clone() *Builder {
	if b == nil {
		return nil
	}
	cloned := &Builder{
		maxTokens: b.maxTokens,
		counter:   b.counter,
		formatter: b.formatter,
		blocks:    make([]registeredBlock, len(b.blocks)),
	}
	for i, block := range b.blocks {
		cloned.blocks[i] = registeredBlock{
			name:  block.name,
			block: cloneBlock(block.block),
		}
	}
	return cloned
}

// SetBlockMessages replaces the messages of a named block using a deep copy of msgs.
func (b *Builder) SetBlockMessages(name string, msgs []Message) error {
	if b == nil {
		return errors.New("contexty: set block messages: nil builder")
	}
	for i := range b.blocks {
		if b.blocks[i].name == name {
			b.blocks[i].block.Messages = cloneMessages(msgs)
			return nil
		}
	}
	return fmt.Errorf("block %q: %w", name, ErrBlockNotFound)
}

// Build assembles all blocks into a final []Message that fits within maxTokens.
// Blocks are budgeted strictly in AddBlock registration order. Each build
// creates fresh post-eviction snapshots before invoking the formatter, so the
// builder remains repeatable even if strategies or formatters mutate their input.
func (b *Builder) Build(ctx context.Context) ([]Message, error) {
	result, err := b.BuildDetailed(ctx)
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

// BuildDetailed assembles the configured blocks and returns formatter output plus
// raw post-eviction block snapshots for orchestration.
//
//nolint:gocognit // Sequential block loop: eviction, budget checks, and strategy branches belong in one flow for readability.
func (b *Builder) BuildDetailed(ctx context.Context) (BuildResult, error) {
	if b.maxTokens <= 0 || b.counter == nil {
		return BuildResult{}, ErrInvalidConfig
	}
	counter := b.counter
	remaining := b.maxTokens
	postEviction := make([]NamedBlock, 0, len(b.blocks))

	for _, registered := range b.blocks {
		if err := ctx.Err(); err != nil {
			return BuildResult{}, fmt.Errorf("contexty: build: %w", err)
		}
		block := cloneBlock(registered.block)
		if len(block.Messages) == 0 {
			continue
		}
		if block.Strategy == nil {
			return BuildResult{}, fmt.Errorf("block %q: %w", registered.name, ErrNilStrategy)
		}
		blockTokens, err := counter.Count(ctx, block.Messages)
		if err != nil {
			return BuildResult{}, fmt.Errorf("block %q: %w: %w", registered.name, ErrTokenCountFailed, err)
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
				return BuildResult{}, fmt.Errorf("block %q: %w", registered.name, err)
			}
		}
		if len(out) > 0 {
			rawOut := cloneMessages(out)
			used, err := counter.Count(ctx, rawOut)
			if err != nil {
				return BuildResult{}, fmt.Errorf("block %q: %w: %w", registered.name, ErrTokenCountFailed, err)
			}
			if used > blockBudget {
				return BuildResult{}, fmt.Errorf("block %q: %w", registered.name, ErrStrategyExceededBudget)
			}
			remaining -= used
			postEviction = append(postEviction, NamedBlock{
				Name: registered.name,
				Block: MemoryBlock{
					Strategy:  block.Strategy,
					Messages:  rawOut,
					MaxTokens: block.MaxTokens,
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
		return BuildResult{}, fmt.Errorf("contexty: format: %w", err)
	}
	finalTokens, err := counter.Count(ctx, finalMessages)
	if err != nil {
		return BuildResult{}, fmt.Errorf("contexty: format: %w: %w", ErrTokenCountFailed, err)
	}
	if finalTokens > b.maxTokens {
		return BuildResult{}, fmt.Errorf("contexty: format: %w", ErrFormatterExceededBudget)
	}
	return BuildResult{
		Messages: cloneMessages(finalMessages),
		Blocks:   cloneNamedBlocks(postEviction),
	}, nil
}
