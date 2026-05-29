package testutil

import "github.com/skosovsky/contexty"

// MemoryConversationStore is an alias for the in-memory ConversationStore.
type MemoryConversationStore = contexty.MemoryConversationStore

// NewMemoryConversationStore returns an empty in-memory ConversationStore.
func NewMemoryConversationStore() *MemoryConversationStore {
	return contexty.NewMemoryConversationStore()
}
