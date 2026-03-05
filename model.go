package contexty

import (
	"context"
	"fmt"
)

// ContentPart represents a single part of message content (text or image).
// Type is not validated in core; typical values are "text", "image_url".
type ContentPart struct {
	Type     string    // "text", "image_url", or provider-specific
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL holds URL and optional detail level for image content.
// No URL validation or network checks in core.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // e.g. "low", "high"
}

// ToolCall represents a tool/function call in agent messages.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // typically "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall holds function name and arguments (JSON string; not validated in core).
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message is the minimal unit of context: a single chat turn with role and content.
// v2: Content is always []ContentPart; use TextMessage/MultipartMessage helpers.
// ToolCalls and Metadata support agents and prompt caching; no validation in core.
// CacheControl holds provider-specific cache metadata (e.g. {"type": "ephemeral"}); not interpreted in core.
type Message struct {
	Role         string
	Content      []ContentPart // Always slice; text-only = one part with Type "text"
	Name         string        // Optional: function name for tool messages
	ToolCalls    []ToolCall
	ToolCallID   string
	Metadata     map[string]any
	CacheControl map[string]any // Optional: cache hint for provider (e.g. ephemeral); set by Builder when block has CachePoint
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

// TokenCounter counts tokens for a slice of messages.
// The library does not implement real tokenization; the caller injects an implementation.
// Count must account for message structure (role, content parts, tool calls) and any
// per-message overhead; no validation of content types or URLs in core.
// The context is passed from Compile and may be used for cancellation or timeouts
// (e.g. when counting involves a network call to a tokenization service).
type TokenCounter interface {
	Count(ctx context.Context, msgs []Message) (int, error)
}

// EvictionStrategy defines how to shrink or trim a block to fit the remaining budget.
// Each MemoryBlock has its own strategy (strict, drop, truncate, summarize).
//
// Apply receives originalTokens (pre-counted by Builder) for DRY; implementations must
// return messages whose total token count <= limit. Compile re-counts output and
// returns ErrStrategyExceededBudget if the contract is violated.
type EvictionStrategy interface {
	// Apply returns a subset of msgs that fits within limit tokens, or an error.
	// originalTokens is the token count of msgs (from counter.Count(ctx, msgs)); use it to avoid re-counting.
	// Returned messages must have total token count <= limit; Compile enforces this.
	Apply(ctx context.Context, msgs []Message, originalTokens int, limit int, counter TokenCounter) ([]Message, error)
}

// Summarizer compresses a slice of messages into a single summary message.
// Typically implemented via a cheap/fast LLM call; used by SummarizeStrategy.
type Summarizer interface {
	Summarize(ctx context.Context, msgs []Message) (Message, error)
}

// CacheTypeEphemeral is the cache type value set on the last message of a block when CachePoint is true.
const CacheTypeEphemeral = "ephemeral"

// MemoryBlock is a logical group of messages with a Tier and an EvictionStrategy.
// ID is used in CompileReport; empty ID is allowed.
// MaxTokens is optional: when > 0 and less than the remaining global budget, Apply receives
// this value as the limit so the block is capped locally (e.g. RAG block limited to 200 tokens).
// CachePoint: when true, Compile sets the last message of this block's output with CacheControl
// so the provider can treat it as a cache boundary (e.g. ephemeral cache).
// CacheControl (string) is for other provider-specific caching rules; not interpreted in core.
type MemoryBlock struct {
	ID           string
	Messages     []Message
	Tier         Tier
	Strategy     EvictionStrategy
	MaxTokens    int    // Optional: hard per-block token limit (0 = no limit)
	CachePoint   bool   // If true, last message of block gets CacheControl set (e.g. type=ephemeral)
	CacheControl string // Optional: caching rules for the block
}

// TextMessage creates a simple text-only message (single ContentPart with Type "text").
func TextMessage(role, text string) Message {
	return Message{
		Role:    role,
		Content: []ContentPart{{Type: "text", Text: text}},
	}
}

// MultipartMessage creates a message with multiple content parts (text, images, etc.).
func MultipartMessage(role string, parts ...ContentPart) Message {
	return Message{
		Role:    role,
		Content: parts,
	}
}
