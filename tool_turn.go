package contexty

// Canonical tool-turn layout for atomic truncation:
// RoleAssistant with ToolCallPart(s), then zero or more RoleTool messages whose
// ToolResultPart.ToolCallID values match those calls. In-message ToolResultPart
// on the assistant message is supported for serialization but is not part of the
// canonical multi-message turn block used by DropHead/DropTail atomicity.

// ToolTurnUsesCanonicalLayout reports whether assistantIdx starts a complete
// canonical tool turn in msgs (all call IDs satisfied by following RoleTool msgs).
func ToolTurnUsesCanonicalLayout(msgs []Message, assistantIdx int) bool {
	if assistantIdx < 0 || assistantIdx >= len(msgs) {
		return false
	}
	msg := msgs[assistantIdx]
	if msg.Role != RoleAssistant || !msg.HasToolCalls() {
		return false
	}
	expected := make(map[string]bool)
	for _, c := range msg.ToolCallParts() {
		if c.ID != "" {
			expected[c.ID] = true
		}
	}
	if len(expected) == 0 {
		return true
	}
	for j := assistantIdx + 1; j < len(msgs); j++ {
		if msgs[j].Role != RoleTool {
			break
		}
		for _, tr := range msgs[j].ToolResultParts() {
			delete(expected, tr.ToolCallID)
		}
	}
	return len(expected) == 0
}
