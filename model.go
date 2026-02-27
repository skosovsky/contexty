package contexty

import (
	"context"
	"fmt"
)

// Message is the minimal unit of context: a single chat turn with role and text content.
// v1 focuses on text only; Name is optional (e.g. for tool/function messages).
type Message struct {
	Role    string // system, user, assistant, tool
	Content string // Text representation
	Name    string // Optional: function name for tool messages
}

// Tier is the priority level of a memory block (lower number = higher priority).
// The type is int so callers can define custom tiers (e.g. Tier(10) for debug logs).
// Built-in constants cover typical use cases but the set is not closed.
type Tier int

const (
	// TierSystem is for immutable instructions (persona, rules). Never evicted; error if doesn't fit.
	TierSystem Tier = 0
	// TierCore is for pinned facts (user name, preferences).
	TierCore Tier = 1
	// TierRAG is for external knowledge (episodic retrieval).
	TierRAG Tier = 2
	// TierHistory is for conversation history (working memory).
	TierHistory Tier = 3
	// TierScratchpad is for temporary reasoning and tool call logs.
	TierScratchpad Tier = 4
)

// String returns the tier name for built-in constants, or "Tier(N)" for custom values.
func (t Tier) String() string {
	switch t {
	case TierSystem:
		return "system"
	case TierCore:
		return "core"
	case TierRAG:
		return "rag"
	case TierHistory:
		return "history"
	case TierScratchpad:
		return "scratchpad"
	default:
		return fmt.Sprintf("Tier(%d)", int(t))
	}
}

// TokenCounter counts the number of tokens in a text string.
// The library does not implement real tokenization; the caller injects an implementation
// (e.g. tiktoken for OpenAI, or CharFallbackCounter for testing/rough estimation).
type TokenCounter interface {
	Count(text string) (int, error)
}

// EvictionStrategy defines how to shrink or trim a block to fit the remaining budget.
// Each MemoryBlock has its own strategy (strict, drop, truncate, summarize).
//
// Implementations of Apply must return a slice of messages whose total token count
// (as measured by counter) does not exceed limit. Compile validates this postcondition
// and returns ErrStrategyExceededBudget if a strategy violates it.
type EvictionStrategy interface {
	// Apply returns a subset of msgs that fits within limit tokens, or an error.
	// The returned messages must have total token count <= limit; Compile enforces this.
	// counter is used to count tokens; strategies must not store it.
	Apply(ctx context.Context, msgs []Message, limit int, counter TokenCounter) ([]Message, error)
}

// Summarizer compresses a slice of messages into a single summary message.
// Typically implemented via a cheap/fast LLM call; used by SummarizeStrategy.
type Summarizer interface {
	Summarize(ctx context.Context, msgs []Message) (Message, error)
}

// MemoryBlock is a logical group of messages (e.g. "Chat History" or "RAG Results")
// with a Tier and an EvictionStrategy for when it does not fit the budget.
// ID is used in CompileReport (TokensPerBlock, Evictions, BlocksDropped); empty ID is allowed.
type MemoryBlock struct {
	ID       string
	Messages []Message
	Tier     Tier
	Strategy EvictionStrategy
}

// countBlockTokens returns the total token count for all messages in the block.
// Each message is counted by concatenating Role and Content for the counter;
// used by Builder and strategies. Returns 0 for empty slice.
func countBlockTokens(counter TokenCounter, msgs []Message) (int, error) {
	var total int
	for _, m := range msgs {
		// Represent message for counting: role + content (name is optional metadata)
		text := m.Role + "\n" + m.Content
		if m.Name != "" {
			text = m.Name + "\n" + text
		}
		n, err := counter.Count(text)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
