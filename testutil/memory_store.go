package testutil

import "github.com/skosovsky/contexty"

// MemoryConversationStateStore is an alias for the in-memory ConversationStateStore.
type MemoryConversationStateStore = contexty.MemoryConversationStateStore

// NewMemoryConversationStateStore returns an empty in-memory ConversationStateStore.
func NewMemoryConversationStateStore(opts ...contexty.MemoryStateStoreOption) *MemoryConversationStateStore {
	return contexty.NewMemoryConversationStateStore(opts...)
}
