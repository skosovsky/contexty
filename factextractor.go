package contexty

import "context"

// FactExtractor analyzes conversation history and extracts new long-term facts.
// This interface is reserved for v2; the allocator does not use it yet.
// Implementations typically call an LLM with a prompt like "Extract new facts about the user."
//
// TODO: v2 — integrate with TierCore updates and diffing utilities.
type FactExtractor interface {
	Extract(ctx context.Context, history []Message) ([]string, error)
}
