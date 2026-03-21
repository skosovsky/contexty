package contexty

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultFormatter_ConcatenatesBlocks(t *testing.T) {
	formatter := DefaultFormatter{}
	got, err := formatter.Format(context.Background(), []NamedBlock{
		{
			Name: "first",
			Block: MemoryBlock{
				Messages: []Message{
					TextMessage(RoleSystem, "a"),
					TextMessage(RoleSystem, "b"),
				},
			},
		},
		{
			Name: "second",
			Block: MemoryBlock{
				Messages: []Message{
					TextMessage(RoleSystem, "c"),
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"a", "b", "c"}, []string{
		got[0].Content[0].Text,
		got[1].Content[0].Text,
		got[2].Content[0].Text,
	})
}

func TestDefaultFormatter_ReturnsDetachedMessages(t *testing.T) {
	blocks := []NamedBlock{{
		Name: "first",
		Block: MemoryBlock{
			Messages: []Message{TextMessage(RoleSystem, "original")},
		},
	}}

	got, err := DefaultFormatter{}.Format(context.Background(), blocks)
	require.NoError(t, err)
	got[0].Content[0].Text = "mutated"
	assert.Equal(t, "original", blocks[0].Block.Messages[0].Content[0].Text)
}
