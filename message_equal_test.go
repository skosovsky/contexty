package contexty_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skosovsky/contexty"
)

func TestMessageEqual_Parts(t *testing.T) {
	a := contexty.Message{
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "ok"}},
	}
	b := contexty.Message{
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "ok"}},
	}
	assert.True(t, contexty.MessageEqual(a, b))
	b.Parts[0] = contexty.TextPart{Text: "no"}
	assert.False(t, contexty.MessageEqual(a, b))
}
