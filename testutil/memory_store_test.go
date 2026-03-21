package testutil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestMemoryStore_LoadReturnsDeepCopy(t *testing.T) {
	store := NewMemoryStore()
	msg := contexty.Message{
		Role: contexty.RoleUser,
		Content: []contexty.ContentPart{
			{Type: contexty.ContentPartTypeText, Text: "hello"},
		},
		Metadata: map[string]any{
			"nested": map[string]any{"value": "a"},
		},
	}
	s0, err := store.Load(context.Background(), "thread-1")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), "thread-1", s0.Version, []contexty.Message{msg}))

	loaded, err := store.Load(context.Background(), "thread-1")
	require.NoError(t, err)
	require.Len(t, loaded.Messages, 1)

	loaded.Messages[0].Content[0].Text = "changed"
	loaded.Messages[0].Metadata["nested"].(map[string]any)["value"] = "b"

	reloaded, err := store.Load(context.Background(), "thread-1")
	require.NoError(t, err)
	assert.Equal(t, "hello", reloaded.Messages[0].Content[0].Text)
	assert.Equal(t, "a", reloaded.Messages[0].Metadata["nested"].(map[string]any)["value"])
}

func TestMemoryStore_AppendAndSaveCopyInput(t *testing.T) {
	store := NewMemoryStore()
	msg := contexty.Message{
		Role: contexty.RoleAssistant,
		Content: []contexty.ContentPart{
			{Type: contexty.ContentPartTypeText, Text: "answer"},
		},
	}

	s0, err := store.Load(context.Background(), "thread-1")
	require.NoError(t, err)
	require.NoError(t, store.Append(context.Background(), "thread-1", s0.Version, msg))
	msg.Content[0].Text = "mutated"

	loaded, err := store.Load(context.Background(), "thread-1")
	require.NoError(t, err)
	require.Len(t, loaded.Messages, 1)
	assert.Equal(t, "answer", loaded.Messages[0].Content[0].Text)

	s1, err := store.Load(context.Background(), "thread-1")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), "thread-1", s1.Version, nil))
	loaded, err = store.Load(context.Background(), "thread-1")
	require.NoError(t, err)
	assert.Empty(t, loaded.Messages)
}
