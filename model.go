package contexty

import "context"

// Common chat roles. Use these instead of string literals so typos are caught at compile time.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Common ContentPart.Type values (library does not validate Type).
const (
	ContentPartTypeText     = "text"
	ContentPartTypeImageURL = "image_url"
)

// ContentPart represents a single part of message content (text or image).
// Type is not validated by the library; typical values are "text", "image_url".
type ContentPart struct {
	Type     string    // "text", "image_url", or provider-specific
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL holds URL and optional detail level for image content.
// No URL validation or network checks are performed by the library.
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

// FunctionCall holds function name and arguments (JSON string; not validated by the library).
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message is the minimal unit of context: a single chat turn with role and content.
// Content is always []ContentPart; use TextMessage/MultipartMessage helpers.
// ToolCalls and Metadata support agents; no validation is performed by the library.
type Message struct {
	Role       string
	Content    []ContentPart // Always slice; text-only = one part with Type ContentPartTypeText
	Name       string        // Optional: function name for tool messages
	ToolCalls  []ToolCall
	ToolCallID string
	Metadata   map[string]any
}

// Clone returns a deep copy of the message suitable for safe reuse across builders and stores.
func (m Message) Clone() Message {
	return cloneMessage(m)
}

// TokenCounter counts tokens for a slice of messages.
// The library does not implement real tokenization; the caller injects an implementation.
// Count must account for message structure (role, content parts, tool calls) and any
// per-message overhead; no validation of content types or URLs is performed by the library.
// CountPerMessage returns one weight per message (same order as msgs); used for O(1) eviction loops.
// The context is passed from Build and may be used for cancellation or timeouts
// (e.g. when counting involves a network call to a tokenization service).
type TokenCounter interface {
	Count(ctx context.Context, msgs []Message) (int, error)
	CountPerMessage(ctx context.Context, msgs []Message) ([]int, error)
}

// EvictionStrategy defines how to shrink or trim a block to fit the remaining budget.
// Each MemoryBlock has its own strategy (strict, drop, drop-head, summarize).
//
// Apply receives originalTokens (pre-counted by Builder) for DRY; implementations must
// return messages whose total token count <= limit. Build re-counts output and
// returns ErrStrategyExceededBudget if the contract is violated.
type EvictionStrategy interface {
	// Apply returns a subset of msgs that fits within limit tokens, or an error.
	// originalTokens is the token count of msgs (from counter.Count(ctx, msgs)); use it to avoid re-counting.
	// Returned messages must have total token count <= limit; Build enforces this.
	Apply(ctx context.Context, msgs []Message, originalTokens int, limit int, counter TokenCounter) ([]Message, error)
}

// Summarizer compresses a slice of messages into a single summary message.
// Typically implemented via a cheap/fast LLM call; used by SummarizeStrategy.
type Summarizer interface {
	Summarize(ctx context.Context, msgs []Message) (Message, error)
}

// MemoryBlock is a logical group of messages with an EvictionStrategy.
// MaxTokens is optional: when > 0 and less than the remaining global budget, Apply receives
// this value as the limit so the block is capped locally.
type MemoryBlock struct {
	Strategy  EvictionStrategy
	Messages  []Message
	MaxTokens int // Optional: hard per-block token limit (0 = no limit)
}

// NamedBlock pairs a block snapshot with its registration name.
// Names are preserved in registration order and are available to formatters.
type NamedBlock struct {
	Name  string
	Block MemoryBlock
}

// Formatter turns post-eviction block snapshots into a final message slice.
// Build passes the caller's context to support cancellation, tracing, and
// request-scoped formatter behavior.
type Formatter interface {
	Format(ctx context.Context, blocks []NamedBlock) ([]Message, error)
}

// EvictionMiddleware wraps an EvictionStrategy.
type EvictionMiddleware func(EvictionStrategy) EvictionStrategy

// FormatterMiddleware wraps a Formatter.
type FormatterMiddleware func(Formatter) Formatter

// TextMessage creates a simple text-only message (single ContentPart with Type "text").
func TextMessage(role, text string) Message {
	return Message{
		Role:    role,
		Content: []ContentPart{{Type: ContentPartTypeText, Text: text}},
	}
}

// MultipartMessage creates a message with multiple content parts (text, images, etc.).
func MultipartMessage(role string, parts ...ContentPart) Message {
	return Message{
		Role:    role,
		Content: parts,
	}
}
