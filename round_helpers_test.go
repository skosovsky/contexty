package contexty_test

import (
	"github.com/skosovsky/contexty"
)

func fixtureRepairPolicy() contexty.InterruptedRoundRepairPolicy {
	return contexty.InterruptedRoundRepairPolicy{
		Descriptor: contexty.Descriptor{ID: "host-projection", Revision: "opaque-policy"},
		Encoding:   contexty.Descriptor{ID: "semantic-json", Revision: "host-codecs"},
		Decisions:  map[string]string{"assistant": "host-decision"},
	}
}

func fixtureRoundMessages() []contexty.Message {
	return []contexty.Message{
		{ID: "assistant", Role: contexty.RoleAssistant, Parts: []contexty.ContentPart{
			contexty.ToolCallPart{
				ID:   "first",
				Name: "opaque-a",
			}, contexty.ToolCallPart{ID: "second", Name: "opaque-b"}}},
		{ID: "result", Role: contexty.RoleTool, Parts: []contexty.ContentPart{
			contexty.ToolResultPart{ToolCallID: "first", Payload: contexty.TextPayload("done")}}},
	}
}
