package contexty

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectIntoSystem(t *testing.T) {
	t.Run("empty blocks returns systemMsg as-is", func(t *testing.T) {
		sys := Message{Role: "system", Content: "You are helpful."}
		got := InjectIntoSystem(sys)
		assert.Equal(t, sys, got)
	})

	t.Run("one block", func(t *testing.T) {
		sys := Message{Role: "system", Content: "Rules."}
		got := InjectIntoSystem(sys, Message{Role: "system", Content: "User likes cats."})
		require.Contains(t, got.Content, "Rules.")
		require.Contains(t, got.Content, "<context>")
		require.Contains(t, got.Content, "<fact>User likes cats.</fact>")
		require.Contains(t, got.Content, "</context>")
		assert.Equal(t, "system", got.Role)
	})

	t.Run("two blocks", func(t *testing.T) {
		sys := Message{Role: "system", Content: "Base"}
		got := InjectIntoSystem(sys,
			Message{Content: "Fact1"},
			Message{Content: "Fact2"},
		)
		assert.True(t, strings.HasPrefix(got.Content, "Base\n<context>"))
		assert.Contains(t, got.Content, "<fact>Fact1</fact>")
		assert.Contains(t, got.Content, "<fact>Fact2</fact>")
		assert.True(t, strings.HasSuffix(got.Content, "</context>"))
	})

	t.Run("block with empty Content", func(t *testing.T) {
		sys := Message{Role: "system", Content: "X"}
		got := InjectIntoSystem(sys, Message{Content: ""})
		assert.Contains(t, got.Content, "<fact></fact>")
	})

	t.Run("block content is XML-escaped", func(t *testing.T) {
		sys := Message{Role: "system", Content: "Base"}
		got := InjectIntoSystem(sys, Message{Content: "Break </fact> and </context> here"})
		require.Contains(t, got.Content, "&lt;/fact&gt;")
		require.Contains(t, got.Content, "&lt;/context&gt;")
	})
}
