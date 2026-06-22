package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// DeltaOperation describes one immutable conversation state transition.
type DeltaOperation string

const (
	DeltaAppendMessages  DeltaOperation = "append_messages"
	DeltaReplaceSegment  DeltaOperation = "replace_segment"
	DeltaRemoveMessages  DeltaOperation = "remove_messages"
	DeltaClearSegment    DeltaOperation = "clear_segment"
	DeltaClearState      DeltaOperation = "clear_state"
	DeltaUpsertArtifact  DeltaOperation = "upsert_artifact"
	DeltaRemoveArtifact  DeltaOperation = "remove_artifact"
	DeltaAppendToolRound DeltaOperation = "append_tool_round"
)

// ConversationDelta is a deterministic transition over conversation state.
type ConversationDelta struct {
	Operation  DeltaOperation   `json:"operation"`
	Segment    SegmentName      `json:"segment,omitempty"`
	Messages   []Message        `json:"-"`
	MessageIDs []string         `json:"message_ids,omitempty"`
	Artifact   *ContextArtifact `json:"artifact,omitempty"`
	ToolRound  *ToolRound       `json:"-"`
}

// ApplyDelta applies a single transition without mutating state.
func ApplyDelta(state ConversationState, delta ConversationDelta) (ConversationState, error) {
	switch delta.Operation {
	case DeltaAppendMessages:
		return applyAppendMessages(state, delta), nil
	case DeltaReplaceSegment:
		return state.WithSegment(delta.Segment, delta.Messages), nil
	case DeltaRemoveMessages:
		return applyRemoveMessages(state, delta)
	case DeltaClearSegment:
		return state.WithSegment(delta.Segment, nil), nil
	case DeltaClearState:
		return EmptyState().WithVersion(state.Version()), nil
	case DeltaUpsertArtifact:
		if delta.Artifact == nil {
			return ConversationState{}, errors.New("contexty: apply delta: missing artifact")
		}
		return state.WithArtifact(*delta.Artifact), nil
	case DeltaRemoveArtifact:
		return applyRemoveArtifacts(state, delta.MessageIDs), nil
	case DeltaAppendToolRound:
		return applyAppendToolRound(state, delta.ToolRound)
	case "":
		return state, nil
	default:
		return ConversationState{}, fmt.Errorf("contexty: apply delta: unknown operation %q", delta.Operation)
	}
}

func applyAppendMessages(state ConversationState, delta ConversationDelta) ConversationState {
	existing := state.Segment(delta.Segment)
	combined := cloneMessageSlice(existing)
	combined = append(combined, cloneMessageSlice(delta.Messages)...)
	return state.WithSegment(delta.Segment, combined)
}

func applyRemoveMessages(state ConversationState, delta ConversationDelta) (ConversationState, error) {
	remove := make(map[string]struct{}, len(delta.MessageIDs))
	for _, id := range delta.MessageIDs {
		remove[id] = struct{}{}
	}
	existing := state.Segment(delta.Segment)
	if delta.Segment == SegmentHistory {
		if err := validateToolRoundRemoval(existing, remove); err != nil {
			return ConversationState{}, err
		}
	}
	next := existing[:0:0]
	for _, msg := range existing {
		if _, ok := remove[msg.ID]; !ok {
			next = append(next, msg)
		}
	}
	return state.WithSegment(delta.Segment, next), nil
}

func applyRemoveArtifacts(state ConversationState, ids []string) ConversationState {
	next := state
	for _, id := range ids {
		next = next.WithoutArtifact(id)
	}
	return next
}

func applyAppendToolRound(state ConversationState, round *ToolRound) (ConversationState, error) {
	if round == nil {
		return ConversationState{}, errors.New("contexty: apply delta: missing tool round")
	}
	if err := round.Validate(); err != nil {
		return ConversationState{}, err
	}
	history := append(state.Segment(SegmentHistory), round.Messages()...)
	return state.WithSegment(SegmentHistory, history), nil
}

// ApplyDeltas applies transitions in order.
func ApplyDeltas(state ConversationState, deltas ...ConversationDelta) (ConversationState, error) {
	var err error
	next := state
	for _, delta := range deltas {
		next, err = ApplyDelta(next, delta)
		if err != nil {
			return ConversationState{}, err
		}
	}
	return next, nil
}

// ConversationStateStore persists immutable state transitions with OCC.
type ConversationStateStore interface {
	LoadState(ctx context.Context, conversationID string) (ConversationState, error)
	ApplyDelta(ctx context.Context, conversationID string, expectedVersion int64, delta ConversationDelta) error
	ClearState(ctx context.Context, conversationID string, expectedVersion int64) error
}

// ConversationStateCodec serializes state and deltas for checkpoints.
type ConversationStateCodec struct {
	Provenance *ProvenanceRegistry
	Extensions *ExtensionRegistry
}

type conversationDeltaWire struct {
	Operation  DeltaOperation   `json:"operation"`
	Segment    SegmentName      `json:"segment,omitempty"`
	Messages   json.RawMessage  `json:"messages,omitempty"`
	MessageIDs []string         `json:"message_ids,omitempty"`
	Artifact   *ContextArtifact `json:"artifact,omitempty"`
	ToolRound  json.RawMessage  `json:"tool_round,omitempty"`
}

// EncodeState serializes conversation state.
func (c ConversationStateCodec) EncodeState(state ConversationState) ([]byte, error) {
	return ConversationCodec(c).Encode(state)
}

// DecodeState restores conversation state.
func (c ConversationStateCodec) DecodeState(data []byte) (ConversationState, error) {
	return ConversationCodec(c).Decode(data)
}

// EncodeDelta serializes a delta, including polymorphic message parts.
func (c ConversationStateCodec) EncodeDelta(delta ConversationDelta) ([]byte, error) {
	msgs, err := marshalMessagesWithRegistries(delta.Messages, c.Provenance, c.Extensions)
	if err != nil {
		return nil, err
	}
	toolRound, err := marshalToolRoundWithRegistries(delta.ToolRound, c.Provenance, c.Extensions)
	if err != nil {
		return nil, err
	}
	wire := conversationDeltaWire{
		Operation:  delta.Operation,
		Segment:    delta.Segment,
		Messages:   msgs,
		MessageIDs: slices.Clone(delta.MessageIDs),
		Artifact:   cloneArtifactPtr(delta.Artifact),
		ToolRound:  toolRound,
	}
	return json.Marshal(wire)
}

// DecodeDelta restores a delta.
func (c ConversationStateCodec) DecodeDelta(data []byte) (ConversationDelta, error) {
	var wire conversationDeltaWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return ConversationDelta{}, err
	}
	msgs, err := unmarshalMessagesWithRegistries(wire.Messages, c.Provenance, c.Extensions)
	if err != nil {
		return ConversationDelta{}, err
	}
	toolRound, hasToolRound, err := unmarshalToolRoundWithRegistries(wire.ToolRound, c.Provenance, c.Extensions)
	if err != nil {
		return ConversationDelta{}, err
	}
	var toolRoundPtr *ToolRound
	if hasToolRound {
		toolRoundPtr = &toolRound
	}
	return ConversationDelta{
		Operation:  wire.Operation,
		Segment:    wire.Segment,
		Messages:   msgs,
		MessageIDs: slices.Clone(wire.MessageIDs),
		Artifact:   cloneArtifactPtr(wire.Artifact),
		ToolRound:  toolRoundPtr,
	}, nil
}

func validateToolRoundRemoval(msgs []Message, remove map[string]struct{}) error {
	for i := 0; i < len(msgs); i++ {
		if msgs[i].Role != RoleAssistant || !msgs[i].HasToolCalls() {
			continue
		}
		round, err := ToolRoundFromMessages(msgs, i)
		if err != nil {
			continue
		}
		roundMessages := round.Messages()
		removed := 0
		for _, msg := range roundMessages {
			if _, ok := remove[msg.ID]; ok {
				removed++
			}
		}
		if removed > 0 && removed != len(roundMessages) {
			return fmt.Errorf("contexty: delta remove messages splits tool round at message %q", msgs[i].ID)
		}
		i += len(round.Results)
	}
	return nil
}

func cloneArtifactMap(in map[string]ContextArtifact) map[string]ContextArtifact {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ContextArtifact, len(in))
	for id, artifact := range in {
		out[id] = artifact.Clone()
	}
	return out
}

func cloneArtifactPtr(in *ContextArtifact) *ContextArtifact {
	if in == nil {
		return nil
	}
	cp := in.Clone()
	return &cp
}

func mergeArtifactMaps(base map[string]ContextArtifact, artifacts []ContextArtifact) map[string]ContextArtifact {
	next := cloneArtifactMap(base)
	if next == nil {
		next = make(map[string]ContextArtifact, len(artifacts))
	}
	for _, artifact := range artifacts {
		dropArtifactsReplacedByOrigin(next, artifact)
		if existing, ok := next[artifact.ID]; ok {
			next[artifact.ID] = mergeArtifact(existing, artifact)
			continue
		}
		next[artifact.ID] = artifact.Clone()
	}
	return next
}

func dropArtifactsReplacedByOrigin(artifacts map[string]ContextArtifact, incoming ContextArtifact) {
	if incoming.MergePolicy != PolicyReplaceByOrigin {
		return
	}
	replaceKey := artifactOriginKey(incoming)
	if replaceKey == "" {
		return
	}
	for id, existing := range artifacts {
		if id == incoming.ID {
			continue
		}
		if artifactOriginKey(existing) == replaceKey {
			delete(artifacts, id)
		}
	}
}

func artifactMapValues(in map[string]ContextArtifact) []ContextArtifact {
	if len(in) == 0 {
		return nil
	}
	out := make([]ContextArtifact, 0, len(in))
	for _, artifact := range in {
		out = append(out, artifact.Clone())
	}
	slices.SortFunc(out, func(a, b ContextArtifact) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}
