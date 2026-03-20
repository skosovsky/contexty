package contexty

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextMessage(t *testing.T) {
	msg := TextMessage("user", "hello")
	assert.Equal(t, "user", msg.Role)
	require.Len(t, msg.Content, 1)
	assert.Equal(t, "text", msg.Content[0].Type)
	assert.Equal(t, "hello", msg.Content[0].Text)
}

func TestMultipartMessage(t *testing.T) {
	msg := MultipartMessage("user",
		ContentPart{Type: "text", Text: "hello"},
		ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/image.png", Detail: "high"}},
	)
	assert.Equal(t, "user", msg.Role)
	require.Len(t, msg.Content, 2)
	assert.Equal(t, "hello", msg.Content[0].Text)
	require.NotNil(t, msg.Content[1].ImageURL)
	assert.Equal(t, "https://example.com/image.png", msg.Content[1].ImageURL.URL)
}
