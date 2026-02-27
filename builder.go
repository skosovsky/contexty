package contexty

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

// AllocatorConfig configures the token budget and how to count tokens.
type AllocatorConfig struct {
	MaxTokens    int          // Total token budget (must be > 0)
	TokenCounter TokenCounter // Required; used by Compile
}

// CompileReport describes what happened during Compile: token usage and evictions.
type CompileReport struct {
	TotalTokensUsed int               // Total tokens in the final result
	OriginalTokens  int               // Total tokens before eviction (all blocks considered)
	TokensPerBlock  map[string]int    // Block ID -> tokens used in output
	Evictions       map[string]string // Block ID -> strategy applied ("rejected", "dropped", "truncated", "summarized")
	BlocksDropped   []string          // IDs of blocks completely removed (may contain duplicates if multiple blocks shared the same ID)
}

// Builder collects memory blocks and compiles them into a single message slice within the token budget.
// A Builder can be reused: call AddBlock and Compile multiple times. Each Compile uses the current
// list of blocks (blocks are not cleared after Compile). For a fresh compile, create a new Builder.
type Builder struct {
	config AllocatorConfig
	blocks []MemoryBlock
}

// NewBuilder returns a new Builder with the given config. Config is not validated until Compile.
func NewBuilder(cfg AllocatorConfig) *Builder {
	return &Builder{
		config: cfg,
		blocks: nil,
	}
}

// AddBlock appends a block and returns the builder for chaining.
func (b *Builder) AddBlock(block MemoryBlock) *Builder {
	b.blocks = append(b.blocks, block)
	return b
}

// Compile assembles all blocks into a single []Message that fits within MaxTokens.
// Blocks are processed in Tier order (stable sort); within the same Tier, insertion order is kept.
// Returns the final messages, a report, and an error (e.g. invalid config or StrictStrategy overflow).
// Compile can be called multiple times on the same Builder; each call uses the current blocks.
func (b *Builder) Compile(ctx context.Context) ([]Message, CompileReport, error) {
	if b.config.MaxTokens <= 0 || b.config.TokenCounter == nil {
		return nil, CompileReport{}, ErrInvalidConfig
	}
	counter := b.config.TokenCounter
	report := CompileReport{
		TokensPerBlock: make(map[string]int),
		Evictions:      make(map[string]string),
	}

	// Stable sort by Tier
	sorted := slices.Clone(b.blocks)
	slices.SortStableFunc(sorted, func(x, y MemoryBlock) int {
		return cmp.Compare(x.Tier, y.Tier)
	})

	var result []Message
	remaining := b.config.MaxTokens

	for _, block := range sorted {
		if err := ctx.Err(); err != nil {
			return nil, CompileReport{}, fmt.Errorf("contexty: compile: %w", err)
		}
		if len(block.Messages) == 0 {
			continue
		}
		if block.Strategy == nil {
			return nil, CompileReport{}, fmt.Errorf("block %q: %w", block.ID, ErrNilStrategy)
		}
		blockTokens, err := countBlockTokens(counter, block.Messages)
		if err != nil {
			return nil, CompileReport{}, fmt.Errorf("block %q: %w: %w", block.ID, ErrTokenCountFailed, err)
		}
		report.OriginalTokens += blockTokens

		// TODO(v2): pass pre-counted blockTokens into Apply to avoid double token counting in strategies.
		var out []Message
		var eviction string
		if blockTokens <= remaining {
			out = block.Messages
			// no eviction label
		} else {
			out, err = block.Strategy.Apply(ctx, block.Messages, remaining, counter)
			if err != nil {
				return nil, CompileReport{}, fmt.Errorf("block %q: %w", block.ID, err)
			}
			eviction = evictionLabel(block.Strategy)
			if len(out) == 0 {
				report.BlocksDropped = append(report.BlocksDropped, block.ID)
			}
		}

		if eviction != "" {
			report.Evictions[block.ID] = eviction
		}
		if len(out) > 0 {
			used, err := countBlockTokens(counter, out)
			if err != nil {
				return nil, CompileReport{}, fmt.Errorf("block %q: %w: %w", block.ID, ErrTokenCountFailed, err)
			}
			if used > remaining {
				return nil, CompileReport{}, fmt.Errorf("block %q: %w", block.ID, ErrStrategyExceededBudget)
			}
			report.TokensPerBlock[block.ID] = used
			remaining -= used
			report.TotalTokensUsed += used
			result = append(result, out...)
		}
	}

	return result, report, nil
}

func evictionLabel(s EvictionStrategy) string {
	switch s.(type) {
	case *strictStrategy:
		return "rejected"
	case *dropStrategy:
		return "dropped"
	case *truncateOldestStrategy:
		return "truncated"
	case *summarizeStrategy:
		return "summarized"
	default:
		return "evicted"
	}
}
