package contexty

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidToolRound       = errors.New("contexty: invalid tool round layout")
	ErrUnknownToolRoundState  = errors.New("contexty: unknown tool round state or declaration")
	ErrToolRoundStateConflict = errors.New("contexty: tool round state contradicts content")
)

type ToolRoundState string

const (
	ToolRoundComplete    ToolRoundState = "complete"
	ToolRoundPending     ToolRoundState = "pending"
	ToolRoundInterrupted ToolRoundState = "interrupted"
)

// ToolRoundObservation describes local evidence, not execution/approval status.
// Start and End are inclusive indexes into the original messages.
type ToolRoundObservation struct {
	AssistantID    string         `json:"assistant_id"`
	Start          int            `json:"start"`
	End            int            `json:"end"`
	State          ToolRoundState `json:"state"`
	MissingCallIDs []string       `json:"missing_call_ids"`
}

// InspectToolRoundStates does not repair, summarize, mutate or drop messages.
// Declarations are keyed by assistant message ID, not globally reused call IDs.
func InspectToolRoundStates(
	messages []Message,
	declarations map[string]ToolRoundState,
) ([]ToolRoundObservation, error) {
	var observations []ToolRoundObservation
	seen := make(map[string]bool)
	for index := 0; index < len(messages); index++ {
		message := messages[index]
		if message.Role == RoleTool || len(message.ToolResultParts()) > 0 {
			return nil, ErrInvalidToolRound
		}
		if !message.HasToolCalls() {
			continue
		}
		if message.Role != RoleAssistant {
			return nil, ErrInvalidToolRound
		}
		if message.ID != "" && seen[message.ID] {
			return nil, ErrInvalidToolRound
		}
		end := contiguousToolBlockEnd(messages, index)
		observation, err := inspectToolRound(message, messages[index+1:end+1], index, end, declarations)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
		seen[message.ID] = true
		index = end
	}
	for id, state := range declarations {
		if id == "" || !seen[id] || !validToolRoundState(state) {
			return nil, ErrUnknownToolRoundState
		}
	}
	return observations, nil
}

func inspectToolRound(
	assistant Message,
	results []Message,
	start, end int,
	declarations map[string]ToolRoundState,
) (ToolRoundObservation, error) {
	calls := assistant.ToolCallParts()
	expected, err := toolRoundCallIDs(calls)
	if err != nil {
		return ToolRoundObservation{}, fmt.Errorf("%w: %w", ErrInvalidToolRound, err)
	}
	seen, err := validateToolRoundResults(results, expected)
	if err != nil {
		return ToolRoundObservation{}, fmt.Errorf("%w: %w", ErrInvalidToolRound, err)
	}
	for _, result := range results {
		if result.HasToolCalls() {
			return ToolRoundObservation{}, ErrInvalidToolRound
		}
	}
	observation := ToolRoundObservation{
		AssistantID:    assistant.ID,
		Start:          start,
		End:            end,
		State:          ToolRoundComplete,
		MissingCallIDs: nil,
	}
	for _, call := range calls {
		if _, found := seen[call.ID]; !found {
			observation.MissingCallIDs = append(observation.MissingCallIDs, call.ID)
		}
	}
	if len(observation.MissingCallIDs) > 0 {
		observation.State = ToolRoundPending
	}
	if declared, found := declarations[assistant.ID]; found {
		if !validToolRoundState(declared) {
			return ToolRoundObservation{}, ErrUnknownToolRoundState
		}
		if declared == ToolRoundInterrupted && observation.State == ToolRoundPending {
			observation.State = declared
		} else if declared != observation.State {
			return ToolRoundObservation{}, ErrToolRoundStateConflict
		}
	}
	return observation, nil
}

func validToolRoundState(state ToolRoundState) bool {
	switch state {
	case ToolRoundComplete, ToolRoundPending, ToolRoundInterrupted:
		return true
	default:
		return false
	}
}
