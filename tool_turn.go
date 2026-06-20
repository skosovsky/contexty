package contexty

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Canonical tool-turn layout for atomic truncation:
// RoleAssistant with ToolCallPart(s), then zero or more RoleTool messages whose
// ToolResultPart.ToolCallID values match those calls. In-message ToolResultPart
// on the assistant message is supported for serialization but is not part of the
// canonical multi-message turn block used by DropHead/DropTail atomicity.

// ToolTurnUsesCanonicalLayout reports whether assistantIdx starts a complete
// canonical tool turn in msgs (all call IDs satisfied by following RoleTool msgs).
func ToolTurnUsesCanonicalLayout(msgs []Message, assistantIdx int) bool {
	_, err := ToolRoundFromMessages(msgs, assistantIdx)
	return err == nil
}

// ToolRound is the first-class canonical assistant-call/result grouping.
type ToolRound struct {
	Assistant Message   `json:"assistant"`
	Results   []Message `json:"results"`
}

// Clone returns a deep copy.
func (r ToolRound) Clone() ToolRound {
	return ToolRound{
		Assistant: r.Assistant.Clone(),
		Results:   cloneMessageSlice(r.Results),
	}
}

// Messages returns the canonical assistant-call/result messages in payload order.
func (r ToolRound) Messages() []Message {
	out := make([]Message, 0, 1+len(r.Results))
	out = append(out, r.Assistant.Clone())
	out = append(out, cloneMessageSlice(r.Results)...)
	return out
}

func marshalToolRoundWithRegistries(
	round *ToolRound,
	reg *ProvenanceRegistry,
	extReg *ExtensionRegistry,
) (json.RawMessage, error) {
	if round == nil {
		return nil, nil
	}
	if err := round.Validate(); err != nil {
		return nil, err
	}
	return marshalMessagesWithRegistries(round.Messages(), reg, extReg)
}

func unmarshalToolRoundWithRegistries(
	data json.RawMessage,
	reg *ProvenanceRegistry,
	extReg *ExtensionRegistry,
) (ToolRound, bool, error) {
	if len(data) == 0 || string(data) == jsonNullLiteral {
		return ToolRound{}, false, nil
	}
	msgs, err := unmarshalMessagesWithRegistries(data, reg, extReg)
	if err != nil {
		return ToolRound{}, false, err
	}
	round, err := ToolRoundFromMessages(msgs, 0)
	if err != nil {
		return ToolRound{}, false, err
	}
	if len(round.Messages()) != len(msgs) {
		return ToolRound{}, false, errors.New("contexty: tool round wire has trailing messages")
	}
	return round, true, nil
}

func toolRoundsFromMessages(msgs []Message) []ToolRound {
	if len(msgs) == 0 {
		return nil
	}
	var rounds []ToolRound
	for i := 0; i < len(msgs); i++ {
		if msgs[i].Role != RoleAssistant || !msgs[i].HasToolCalls() {
			continue
		}
		round, err := ToolRoundFromMessages(msgs, i)
		if err != nil {
			continue
		}
		rounds = append(rounds, round)
		i += len(round.Results)
	}
	return rounds
}

// Validate reports whether the round has one assistant tool-call message and
// result messages for every call ID.
func (r ToolRound) Validate() error {
	if r.Assistant.Role != RoleAssistant {
		return fmt.Errorf("contexty: tool round assistant role is %q", r.Assistant.Role)
	}
	calls := r.Assistant.ToolCallParts()
	if len(calls) == 0 {
		return errors.New("contexty: tool round assistant has no tool calls")
	}
	expected, err := toolRoundCallIDs(calls)
	if err != nil {
		return err
	}
	seenResults, err := validateToolRoundResults(r.Results, expected)
	if err != nil {
		return err
	}
	if len(seenResults) != len(expected) {
		return errors.New("contexty: tool round incomplete")
	}
	return nil
}

func toolRoundCallIDs(calls []ToolCallPart) (map[string]struct{}, error) {
	expected := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		if call.ID == "" {
			return nil, errors.New("contexty: tool round call missing id")
		}
		if _, duplicate := expected[call.ID]; duplicate {
			return nil, fmt.Errorf("contexty: tool round duplicate call id %q", call.ID)
		}
		expected[call.ID] = struct{}{}
	}
	return expected, nil
}

func validateToolRoundResults(
	results []Message,
	expected map[string]struct{},
) (map[string]struct{}, error) {
	seenResults := make(map[string]struct{}, len(expected))
	for _, result := range results {
		if result.Role != RoleTool {
			return nil, fmt.Errorf("contexty: tool round result role is %q", result.Role)
		}
		parts := result.ToolResultParts()
		if len(parts) == 0 {
			return nil, errors.New("contexty: tool round result message has no tool result parts")
		}
		if err := validateToolRoundResultParts(parts, expected, seenResults); err != nil {
			return nil, err
		}
	}
	return seenResults, nil
}

func validateToolRoundResultParts(
	parts []ToolResultPart,
	expected map[string]struct{},
	seenResults map[string]struct{},
) error {
	for _, part := range parts {
		if part.ToolCallID == "" {
			return errors.New("contexty: tool round result missing call id")
		}
		if _, ok := expected[part.ToolCallID]; !ok {
			return fmt.Errorf("contexty: tool round result references unknown call id %q", part.ToolCallID)
		}
		if _, duplicate := seenResults[part.ToolCallID]; duplicate {
			return fmt.Errorf("contexty: tool round duplicate result for call id %q", part.ToolCallID)
		}
		seenResults[part.ToolCallID] = struct{}{}
	}
	return nil
}

// ToolRoundFromMessages builds a ToolRound from an assistant index and
// following contiguous tool messages.
func ToolRoundFromMessages(msgs []Message, assistantIdx int) (ToolRound, error) {
	if assistantIdx < 0 || assistantIdx >= len(msgs) {
		return ToolRound{}, errors.New("contexty: tool round assistant index out of range")
	}
	round := ToolRound{Assistant: msgs[assistantIdx].Clone(), Results: nil}
	for i := assistantIdx + 1; i < len(msgs) && msgs[i].Role == RoleTool; i++ {
		round.Results = append(round.Results, msgs[i].Clone())
	}
	if err := round.Validate(); err != nil {
		return ToolRound{}, err
	}
	return round, nil
}
