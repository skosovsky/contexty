package contexty

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHistoriesEqual_ExplicitComparison(t *testing.T) {
	a := []Message{
		TextMessage(RoleUser, "hello"),
		{
			Role: RoleAssistant,
			Content: []ContentPart{
				{Type: ContentPartTypeText, Text: "ok"},
			},
			Metadata: map[string]any{"k": float64(1)},
		},
	}
	b := []Message{
		TextMessage(RoleUser, "hello"),
		{
			Role: RoleAssistant,
			Content: []ContentPart{
				{Type: ContentPartTypeText, Text: "ok"},
			},
			Metadata: map[string]any{"k": float64(1)},
		},
	}
	assert.True(t, historiesEqual(a, b))

	b[1].Metadata["k"] = float64(2)
	assert.False(t, historiesEqual(a, b))
}

func TestAnyValuesEqual_DeepEqualNestedMaps(t *testing.T) {
	// Non-comparable map values must not panic; default path uses reflect.DeepEqual.
	a := map[string]any{"nested": map[string]any{"k": []int{1, 2}}}
	b := map[string]any{"nested": map[string]any{"k": []int{1, 2}}}
	assert.True(t, anyValuesEqual(a, b))

	c := map[string]any{"nested": map[string]any{"k": []int{1, 3}}}
	assert.False(t, anyValuesEqual(a, c))
}

func TestMessagesEqual_ImageURL(t *testing.T) {
	u1 := &ImageURL{URL: "https://x", Detail: "low"}
	u2 := &ImageURL{URL: "https://x", Detail: "low"}
	m1 := Message{Role: RoleUser, Content: []ContentPart{{Type: ContentPartTypeImageURL, ImageURL: u1}}}
	m2 := Message{Role: RoleUser, Content: []ContentPart{{Type: ContentPartTypeImageURL, ImageURL: u2}}}
	assert.True(t, messagesEqual(m1, m2))

	m3 := Message{Role: RoleUser, Content: []ContentPart{{Type: ContentPartTypeImageURL, ImageURL: &ImageURL{URL: "https://y"}}}}
	assert.False(t, messagesEqual(m1, m3))
}
