package contexty_test

import (
	"github.com/skosovsky/contexty"
)

func fixtureProtectedHistory() []contexty.Message {
	prefix := []contexty.Message{
		{ID: "old", Role: contexty.RoleUser, Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}}},
		{ID: "mid", Role: contexty.RoleUser, Parts: []contexty.ContentPart{contexty.TextPart{Text: "mid"}}},
		{ID: "keep", Role: contexty.RoleUser, Parts: []contexty.ContentPart{contexty.TextPart{Text: "keep"}}},
	}
	return append(prefix, fixtureRoundMessages()...)
}
