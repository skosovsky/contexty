package contexty

import (
	"html"
	"slices"
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

// FactFormatter converts a slice of fact messages into a single string for injection into a system message.
type FactFormatter func(facts []Message) string

// XMLFormatter returns a FactFormatter that wraps facts in XML: <wrapperTag>, each fact as <fact> escaped text </fact>.
func XMLFormatter(wrapperTag string) FactFormatter {
	return func(facts []Message) string {
		var buf strings.Builder
		buf.WriteString("<" + wrapperTag + ">\n")
		for _, m := range facts {
			t := textFromContent(m.Content)
			buf.WriteString("  <fact>")
			buf.WriteString(html.EscapeString(t))
			buf.WriteString("</fact>\n")
		}
		buf.WriteString("</" + wrapperTag + ">")
		return buf.String()
	}
}

// MarkdownListFormatter returns a FactFormatter that outputs a "### header" section and a markdown list;
// multi-line fact text gets continuation lines indented with two spaces so they stay under the list item.
func MarkdownListFormatter(header string) FactFormatter {
	return func(facts []Message) string {
		var buf strings.Builder
		buf.WriteString("### " + header + "\n")
		for _, m := range facts {
			text := textFromContent(m.Content)
			text = strings.ReplaceAll(text, "\n", "\n  ")
			buf.WriteString("- " + text + "\n")
		}
		return buf.String()
	}
}

// InjectIntoSystem appends formatted facts to the system message as a new text ContentPart.
// Non-destructive for multimodal content: existing Content parts are preserved; a new part with
// "\n\n" + injected text is appended so existing text and images are unchanged.
// If facts is empty, returns systemMsg unchanged.
func InjectIntoSystem(systemMsg Message, formatter FactFormatter, facts ...Message) Message {
	if len(facts) == 0 {
		return systemMsg
	}
	injectedText := formatter(facts)
	content := slices.Clone(systemMsg.Content)
	content = append(content, ContentPart{Type: "text", Text: "\n\n" + injectedText})
	return Message{
		Role:         systemMsg.Role,
		Content:      content,
		Name:         systemMsg.Name,
		ToolCalls:    systemMsg.ToolCalls,
		ToolCallID:   systemMsg.ToolCallID,
		Metadata:     systemMsg.Metadata,
		CacheControl: systemMsg.CacheControl,
	}
}
