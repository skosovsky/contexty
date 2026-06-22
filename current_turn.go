package contexty

import "context"

// CurrentTurnPersistencePolicy controls how the current turn is persisted.
type CurrentTurnPersistencePolicy string

const (
	CurrentTurnPersistRaw        CurrentTurnPersistencePolicy = "persist_raw"
	CurrentTurnPersistPromptSafe CurrentTurnPersistencePolicy = "persist_prompt_safe"
	CurrentTurnPersistNone       CurrentTurnPersistencePolicy = "skip"
)

// CurrentTurn models the active user turn without mutating historical messages.
type CurrentTurn struct {
	Raw         Message
	PromptSafe  Message
	Persistence CurrentTurnPersistencePolicy
}

// NewCurrentTurn builds a current-turn contract that persists the raw message by default.
func NewCurrentTurn(raw Message) CurrentTurn {
	return CurrentTurn{
		Raw:         raw,
		PromptSafe:  Message{},
		Persistence: CurrentTurnPersistRaw,
	}
}

// WithPromptSafe returns a copy with a compile-only prompt projection.
func (t CurrentTurn) WithPromptSafe(msg Message) CurrentTurn {
	t.PromptSafe = msg
	return t
}

// WithPersistence returns a copy with explicit persistence semantics.
func (t CurrentTurn) WithPersistence(policy CurrentTurnPersistencePolicy) CurrentTurn {
	t.Persistence = policy
	return t
}

func (t CurrentTurn) clone() CurrentTurn {
	return CurrentTurn{
		Raw:         t.Raw.Clone(),
		PromptSafe:  t.PromptSafe.Clone(),
		Persistence: t.Persistence,
	}
}

func cloneCurrentTurnPtr(in *CurrentTurn) *CurrentTurn {
	if in == nil || !in.hasRaw() {
		return nil
	}
	cp := in.clone()
	return &cp
}

func (t CurrentTurn) hasRaw() bool {
	return messageHasContent(t.Raw)
}

func (t CurrentTurn) hasPromptSafe() bool {
	return messageHasContent(t.PromptSafe)
}

func (t CurrentTurn) promptMessage() (Message, bool) {
	if !t.hasRaw() {
		return Message{}, false
	}
	if t.hasPromptSafe() {
		return t.PromptSafe.Clone(), true
	}
	return t.Raw.Clone(), true
}

func (t CurrentTurn) persistedMessage() (Message, bool) {
	if !t.hasRaw() {
		return Message{}, false
	}
	switch t.Persistence {
	case CurrentTurnPersistNone:
		return Message{}, false
	case CurrentTurnPersistPromptSafe:
		return t.promptMessage()
	case CurrentTurnPersistRaw, "":
		return t.Raw.Clone(), true
	default:
		return Message{}, false
	}
}

func (t CurrentTurn) validate() error {
	if !t.hasRaw() {
		return nil
	}
	switch t.Persistence {
	case "", CurrentTurnPersistRaw, CurrentTurnPersistPromptSafe, CurrentTurnPersistNone:
		return nil
	default:
		return ErrInvalidCurrentTurnPersistencePolicy
	}
}

func messageHasContent(msg Message) bool {
	return msg.ID != "" || msg.Role != "" || len(msg.Parts) > 0 ||
		msg.Actor != nil || msg.Annotations.Timestamp != nil || len(msg.SourceRefs) > 0 ||
		len(msg.Extensions) > 0 || msg.Origin != nil || msg.LLMCache != nil || msg.Provenance != nil
}

func recordCurrentTurnProjectionCtx(ctx context.Context, turn CurrentTurn) {
	rec := transformRecorderFrom(ctx)
	if rec == nil || !turn.hasRaw() {
		return
	}
	prompt, ok := turn.promptMessage()
	if !ok || turn.Raw.ID == "" {
		return
	}
	if MessageEqual(turn.Raw, prompt) {
		return
	}
	rec.setUnlessFinal(turn.Raw.ID, ActionFormatted, ReasonCurrentTurnProjection)
}
