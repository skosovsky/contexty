package contexty

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// messageText returns the concatenated text from all text parts of m.Content (for tests).
func messageText(m Message) string {
	var b strings.Builder
	for _, p := range m.Content {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func TestInjectIntoSystem(t *testing.T) {
	t.Run("empty blocks returns systemMsg as-is", func(t *testing.T) {
		sys := TextMessage("system", "You are helpful.")
		got := InjectIntoSystem(sys)
		assert.Equal(t, sys.Role, got.Role)
		require.Len(t, got.Content, 1)
		assert.Equal(t, "You are helpful.", got.Content[0].Text)
	})

	t.Run("one block", func(t *testing.T) {
		sys := TextMessage("system", "Rules.")
		got := InjectIntoSystem(sys, TextMessage("system", "User likes cats."))
		text := messageText(got)
		require.Contains(t, text, "Rules.")
		require.Contains(t, text, "<context>")
		require.Contains(t, text, "<fact>User likes cats.</fact>")
		require.Contains(t, text, "</context>")
		assert.Equal(t, "system", got.Role)
	})

	t.Run("two blocks", func(t *testing.T) {
		sys := TextMessage("system", "Base")
		got := InjectIntoSystem(sys,
			Message{Content: []ContentPart{{Type: "text", Text: "Fact1"}}},
			Message{Content: []ContentPart{{Type: "text", Text: "Fact2"}}},
		)
		text := messageText(got)
		assert.True(t, strings.HasPrefix(text, "Base\n<context>"))
		assert.Contains(t, text, "<fact>Fact1</fact>")
		assert.Contains(t, text, "<fact>Fact2</fact>")
		assert.True(t, strings.HasSuffix(text, "</context>"))
	})

	t.Run("block with empty Content", func(t *testing.T) {
		sys := TextMessage("system", "X")
		got := InjectIntoSystem(sys, Message{Content: []ContentPart{{Type: "text", Text: ""}}})
		require.Contains(t, messageText(got), "<fact></fact>")
	})

	t.Run("block content is XML-escaped", func(t *testing.T) {
		sys := TextMessage("system", "Base")
		got := InjectIntoSystem(sys, Message{Content: []ContentPart{{Type: "text", Text: "Break </fact> and </context> here"}}})
		text := messageText(got)
		require.Contains(t, text, "&lt;/fact&gt;")
		require.Contains(t, text, "&lt;/context&gt;")
	})
}
