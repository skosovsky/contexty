package contexty

import (
	"context"
	"fmt"
	"strings"
)

// ViewType selects a projection formatter.
type ViewType string

const (
	ViewLLMXML         ViewType = "llm_xml"
	ViewFlatClassifier ViewType = "flat_classifier"
)

// ViewFormatter renders a snapshot without mutating it.
type ViewFormatter interface {
	Format(ctx context.Context, snap ConversationSnapshot) (string, error)
}

// Render projects a snapshot through the given view.
func Render(ctx context.Context, snap ConversationSnapshot, view ViewType) (string, error) {
	var formatter ViewFormatter
	switch view {
	case ViewLLMXML:
		formatter = LLMXMLFormatter{}
	case ViewFlatClassifier:
		formatter = FlatClassifierFormatter{}
	default:
		return "", fmt.Errorf("contexty: unknown view type %q", view)
	}
	return formatter.Format(ctx, snap)
}

// LLMXMLFormatter wraps messages in XML-like tags per role.
type LLMXMLFormatter struct{}

// Format renders segments in registration order: system, history, tools, memory.
func (LLMXMLFormatter) Format(ctx context.Context, snap ConversationSnapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("contexty: llm xml format: %w", err)
	}
	order := []SegmentName{SegmentSystem, SegmentHistory, SegmentTools, SegmentMemory}
	var b strings.Builder
	for _, seg := range order {
		for _, msg := range snap.Segment(seg) {
			b.WriteString("<")
			b.WriteString(string(msg.Role))
			b.WriteString(">")
			b.WriteString(formatPartsPlain(msg.Parts))
			b.WriteString("</")
			b.WriteString(string(msg.Role))
			b.WriteString(">\n")
		}
	}
	return b.String(), nil
}

// FlatClassifierFormatter produces a flat role: text line per message.
type FlatClassifierFormatter struct{}

// Format renders one line per message across all segments.
func (FlatClassifierFormatter) Format(ctx context.Context, snap ConversationSnapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("contexty: flat format: %w", err)
	}
	order := []SegmentName{SegmentSystem, SegmentHistory, SegmentTools, SegmentMemory}
	var b strings.Builder
	for _, seg := range order {
		for _, msg := range snap.Segment(seg) {
			b.WriteString(string(msg.Role))
			b.WriteString(": ")
			b.WriteString(formatPartsPlain(msg.Parts))
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

func formatPartsPlain(parts []ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		switch v := p.(type) {
		case TextPart:
			b.WriteString(v.Text)
		case ImagePart:
			b.WriteString("[image:")
			b.WriteString(v.URL)
			b.WriteString("]")
		case ToolCallPart:
			b.WriteString("[tool_call:")
			b.WriteString(v.Name)
			b.WriteString("]")
		case ToolResultPart:
			b.WriteString("[tool_result:")
			b.WriteString(v.Content)
			b.WriteString("]")
		}
	}
	return b.String()
}
