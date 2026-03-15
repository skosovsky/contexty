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

func TestXMLFormatter(t *testing.T) {
	f := XMLFormatter("context")
	got := f([]Message{
		{Content: []ContentPart{{Type: "text", Text: "A"}}},
		{Content: []ContentPart{{Type: "text", Text: "B & C"}}},
	})
	require.Contains(t, got, "<context>")
	require.Contains(t, got, "</context>")
	require.Contains(t, got, "<fact>A</fact>")
	require.Contains(t, got, "B &amp; C")
}

func TestMarkdownListFormatter(t *testing.T) {
	f := MarkdownListFormatter("Context:")
	got := f([]Message{
		{Content: []ContentPart{{Type: "text", Text: "Line1\nLine2"}}},
		{Content: []ContentPart{{Type: "text", Text: "Single"}}},
	})
	require.Contains(t, got, "### Context:\n")
	require.Contains(t, got, "- Line1\n  Line2\n")
	require.Contains(t, got, "- Single\n")
}

func TestInjectIntoSystem(t *testing.T) {
	xmlF := XMLFormatter("context")
	t.Run("empty facts returns systemMsg as-is", func(t *testing.T) {
		sys := TextMessage("system", "You are helpful.")
		got := InjectIntoSystem(sys, xmlF)
		assert.Equal(t, sys.Role, got.Role)
		require.Len(t, got.Content, 1)
		assert.Equal(t, "You are helpful.", got.Content[0].Text)
	})

	t.Run("one block", func(t *testing.T) {
		sys := TextMessage("system", "Rules.")
		got := InjectIntoSystem(sys, xmlF, TextMessage("system", "User likes cats."))
		text := messageText(got)
		require.Contains(t, text, "Rules.")
		require.Contains(t, text, "<context>")
		require.Contains(t, text, "<fact>User likes cats.</fact>")
		require.Contains(t, text, "</context>")
		assert.Equal(t, "system", got.Role)
	})

	t.Run("two blocks appends part with separator", func(t *testing.T) {
		sys := TextMessage("system", "Base")
		got := InjectIntoSystem(sys, xmlF,
			Message{Content: []ContentPart{{Type: "text", Text: "Fact1"}}},
			Message{Content: []ContentPart{{Type: "text", Text: "Fact2"}}},
		)
		require.Len(t, got.Content, 2)
		assert.Equal(t, "Base", got.Content[0].Text)
		text := got.Content[1].Text
		assert.True(t, strings.HasPrefix(text, "\n\n<context>"))
		assert.Contains(t, text, "<fact>Fact1</fact>")
		assert.Contains(t, text, "<fact>Fact2</fact>")
		assert.True(t, strings.HasSuffix(text, "</context>"))
	})

	t.Run("block with empty Content", func(t *testing.T) {
		sys := TextMessage("system", "X")
		got := InjectIntoSystem(sys, xmlF, Message{Content: []ContentPart{{Type: "text", Text: ""}}})
		require.Contains(t, messageText(got), "<fact></fact>")
	})

	t.Run("block content is XML-escaped", func(t *testing.T) {
		sys := TextMessage("system", "Base")
		got := InjectIntoSystem(sys, xmlF, Message{Content: []ContentPart{{Type: "text", Text: "Break </fact> and </context> here"}}})
		text := messageText(got)
		require.Contains(t, text, "&lt;/fact&gt;")
		require.Contains(t, text, "&lt;/context&gt;")
	})

	t.Run("multimodal: preserves existing content and appends text part", func(t *testing.T) {
		sys := TextMessage("system", "You are helpful.")
		block := Message{
			Content: []ContentPart{
				{Type: "text", Text: "Fact with text"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/huge.png", Detail: "high"}},
			},
		}
		got := InjectIntoSystem(sys, xmlF, block)
		require.Len(t, got.Content, 2)
		assert.Equal(t, "You are helpful.", got.Content[0].Text)
		text := messageText(got)
		require.Contains(t, text, "Fact with text")
		require.NotContains(t, text, "https://example.com")
		require.NotContains(t, text, "huge.png")
	})

	t.Run("MarkdownListFormatter", func(t *testing.T) {
		sys := TextMessage("system", "Base.")
		got := InjectIntoSystem(sys, MarkdownListFormatter("Facts:"),
			Message{Content: []ContentPart{{Type: "text", Text: "One"}}},
			Message{Content: []ContentPart{{Type: "text", Text: "Two"}}},
		)
		text := messageText(got)
		require.Contains(t, text, "Base.\n")
		require.Contains(t, text, "### Facts:\n")
		require.Contains(t, text, "- One\n")
		require.Contains(t, text, "- Two\n")
	})
}
