package contexty

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultJSONSerializer_RoundTrip(t *testing.T) {
	serializer := DefaultJSONSerializer{}
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentPart{
			{Type: ContentPartTypeText, Text: "summary"},
			{Type: ContentPartTypeImageURL, ImageURL: &ImageURL{URL: "https://example.com/image.png", Detail: "low"}},
		},
		Name:       "weather",
		ToolCallID: "tool-1",
		ToolCalls: []ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: FunctionCall{
				Name:      "lookup",
				Arguments: `{"city":"ho chi minh city"}`,
			},
		}},
		Metadata: map[string]any{
			"tags": []any{"travel", map[string]any{"locale": "vi-VN"}},
		},
	}

	data, err := serializer.Marshal(msg)
	require.NoError(t, err)

	var roundTripped Message
	require.NoError(t, serializer.Unmarshal(data, &roundTripped))
	assert.Equal(t, msg, roundTripped)
}

func TestDefaultJSONSerializer_UsesStandardEncodingJSON(t *testing.T) {
	msg := MultipartMessage(RoleUser,
		ContentPart{Type: ContentPartTypeText, Text: "hello"},
		ContentPart{Type: ContentPartTypeImageURL, ImageURL: &ImageURL{URL: "https://example.com/image.png", Detail: "high"}},
	)

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var roundTripped Message
	require.NoError(t, json.Unmarshal(data, &roundTripped))
	assert.Equal(t, msg, roundTripped)
}
