package contexty

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextMessage(t *testing.T) {
	msg := TextMessage(RoleUser, "hello")
	assert.Equal(t, "user", msg.Role)
	require.Len(t, msg.Content, 1)
	assert.Equal(t, ContentPartTypeText, msg.Content[0].Type)
	assert.Equal(t, "hello", msg.Content[0].Text)
}

func TestMultipartMessage(t *testing.T) {
	msg := MultipartMessage(
		"user",
		ContentPart{Type: ContentPartTypeText, Text: "hello"},
		ContentPart{
			Type:     ContentPartTypeImageURL,
			ImageURL: &ImageURL{URL: "https://example.com/image.png", Detail: "high"},
		},
	)
	assert.Equal(t, "user", msg.Role)
	require.Len(t, msg.Content, 2)
	assert.Equal(t, "hello", msg.Content[0].Text)
	require.NotNil(t, msg.Content[1].ImageURL)
	assert.Equal(t, "https://example.com/image.png", msg.Content[1].ImageURL.URL)
}

func TestMessageClone_DeepCopiesNestedFields(t *testing.T) {
	original := Message{
		Role: RoleAssistant,
		Content: []ContentPart{
			{Type: ContentPartTypeText, Text: "hello"},
			{Type: ContentPartTypeImageURL, ImageURL: &ImageURL{URL: "https://example.com/a.png", Detail: "high"}},
		},
		ToolCalls: []ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: FunctionCall{
				Name:      "lookup",
				Arguments: `{"city":"saigon"}`,
			},
		}},
		Metadata: map[string]any{
			"nested": map[string]any{
				"items": []any{"a", map[string]any{"b": "c"}},
			},
		},
	}

	cloned := original.Clone()
	cloned.Content[0].Text = "changed"
	cloned.Content[1].ImageURL.URL = "https://example.com/b.png"
	cloned.ToolCalls[0].Function.Arguments = `{"city":"hanoi"}`
	cloned.Metadata["nested"].(map[string]any)["items"].([]any)[1].(map[string]any)["b"] = "changed"

	assert.Equal(t, "hello", original.Content[0].Text)
	require.NotNil(t, original.Content[1].ImageURL)
	assert.Equal(t, "https://example.com/a.png", original.Content[1].ImageURL.URL)
	assert.JSONEq(t, `{"city":"saigon"}`, original.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "c", original.Metadata["nested"].(map[string]any)["items"].([]any)[1].(map[string]any)["b"])
}
