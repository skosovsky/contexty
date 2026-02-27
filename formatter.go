package contexty

import (
	"html"
	"strings"
)

// InjectIntoSystem merges auxiliary text blocks into a single system message
// using XML tags for structured separation. Block content is XML-escaped to prevent
// injection of </fact> or </context>. If blocks is empty, returns systemMsg unchanged.
func InjectIntoSystem(systemMsg Message, blocks ...Message) Message {
	if len(blocks) == 0 {
		return systemMsg
	}
	var buf strings.Builder
	buf.WriteString(systemMsg.Content)
	buf.WriteString("\n<context>\n")
	for _, m := range blocks {
		buf.WriteString("  <fact>")
		buf.WriteString(html.EscapeString(m.Content))
		buf.WriteString("</fact>\n")
	}
	buf.WriteString("</context>")
	return Message{
		Role:    systemMsg.Role,
		Content: buf.String(),
		Name:    systemMsg.Name,
	}
}
