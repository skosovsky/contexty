package contexty

import (
	"html"
	"strings"
)

// textFromContent returns the concatenated text of all ContentPart with Type "text".
// Non-text parts are safely ignored (no error, no panic).
func textFromContent(parts []ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// InjectIntoSystem merges auxiliary text blocks into a single system message
// using XML tags for structured separation. Only text parts (Type "text") are
// included; non-text parts are ignored. Content is XML-escaped to prevent
// injection. If blocks is empty, returns systemMsg unchanged.
func InjectIntoSystem(systemMsg Message, blocks ...Message) Message {
	if len(blocks) == 0 {
		return systemMsg
	}
	var buf strings.Builder
	buf.WriteString(textFromContent(systemMsg.Content))
	buf.WriteString("\n<context>\n")
	for _, m := range blocks {
		t := textFromContent(m.Content)
		buf.WriteString("  <fact>")
		buf.WriteString(html.EscapeString(t))
		buf.WriteString("</fact>\n")
	}
	buf.WriteString("</context>")
	return Message{
		Role:       systemMsg.Role,
		Content:    []ContentPart{{Type: "text", Text: buf.String()}},
		Name:       systemMsg.Name,
		ToolCalls:  systemMsg.ToolCalls,
		ToolCallID: systemMsg.ToolCallID,
		Metadata:   systemMsg.Metadata,
	}
}
