package contexty

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// MessageIdentityContext describes where an ID is needed during normalization.
type MessageIdentityContext struct {
	// Pending identifies a new event in the current host turn, not persisted history.
	Pending bool
	// Ordinal is the event position within its turn, independent of history length.
	Ordinal          int
	Segment          SegmentName
	Index            int
	TurnID           string
	TargetName       string
	CurrentTurn      bool
	PromptProjection bool
}

// MessageIdentityPolicy assigns durable IDs to messages that arrive without one.
type MessageIdentityPolicy interface {
	ResolveMessageID(ctx MessageIdentityContext, msg Message) (string, error)
}

// MessageIdentityFunc adapts a function to MessageIdentityPolicy.
type MessageIdentityFunc func(ctx MessageIdentityContext, msg Message) (string, error)

// ResolveMessageID implements MessageIdentityPolicy.
func (f MessageIdentityFunc) ResolveMessageID(ctx MessageIdentityContext, msg Message) (string, error) {
	return f(ctx, msg)
}

// StableMessageIdentityPolicy assigns logical event IDs to Pending/CurrentTurn.
// Historical, static and transform-generated messages need explicit IDs or a host
// identity policy; their current snapshot positions are not durable identities.
type StableMessageIdentityPolicy struct {
	Prefix string
}

// NewStableMessageIdentityPolicy builds an event identity policy.
func NewStableMessageIdentityPolicy(prefix string) StableMessageIdentityPolicy {
	return StableMessageIdentityPolicy{Prefix: prefix}
}

// ResolveMessageID binds a new event to host TurnID, event kind and turn ordinal.
// Content and prompt projections do not change logical identity.
func (p StableMessageIdentityPolicy) ResolveMessageID(ctx MessageIdentityContext, _ Message) (string, error) {
	if ctx.TurnID == "" || ctx.Ordinal < 0 || (!ctx.CurrentTurn && !ctx.Pending) {
		return "", ErrMissingEventIdentity
	}
	kind := "pending_event"
	if ctx.CurrentTurn {
		kind = "current_turn"
	}
	prefix := strings.TrimSpace(p.Prefix)
	if prefix == "" {
		prefix = "msg"
	}
	encoded, err := json.Marshal([]any{prefix, ctx.TurnID, kind, ctx.Ordinal})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "event:" + hex.EncodeToString(digest[:]), nil
}

type compileIdentityKey struct{}

type compileIdentitySettings struct {
	policy         MessageIdentityPolicy
	requireDurable bool
	turnID         string
	targetName     string
}

func withCompileIdentity(
	ctx context.Context,
	policy MessageIdentityPolicy,
	requireDurable bool,
	turnID string,
	targetName string,
) context.Context {
	if policy == nil && !requireDurable && turnID == "" && targetName == "" {
		return ctx
	}
	return context.WithValue(ctx, compileIdentityKey{}, compileIdentitySettings{
		policy:         policy,
		requireDurable: requireDurable,
		turnID:         turnID,
		targetName:     targetName,
	})
}

func compileIdentityFromContext(ctx context.Context) (compileIdentitySettings, bool) {
	if ctx == nil {
		return compileIdentitySettings{}, false
	}
	settings, ok := ctx.Value(compileIdentityKey{}).(compileIdentitySettings)
	return settings, ok
}

func ensureMessageIDsFromContext(
	ctx context.Context,
	seg SegmentName,
	indexOffset int,
	msgs []Message,
) ([]Message, error) {
	if len(msgs) == 0 {
		return nil, nil
	}
	out := make([]Message, len(msgs))
	for i, msg := range msgs {
		normalized, err := ensureMessageIDFromContext(ctx, seg, indexOffset+i, msg)
		if err != nil {
			return nil, err
		}
		out[i] = normalized
	}
	return out, nil
}

func ensureMessageIDFromContext(
	ctx context.Context,
	seg SegmentName,
	index int,
	msg Message,
) (Message, error) {
	settings, ok := compileIdentityFromContext(ctx)
	if !ok {
		owned, err := ownCompileMessage(msg)
		if err != nil {
			return Message{}, err
		}
		return EnsureMessageID(owned), nil
	}
	normalized, _, err := normalizeMessageForCompile(
		msg,
		MessageIdentityContext{
			Pending: false, Ordinal: index,
			Segment:          seg,
			Index:            index,
			TurnID:           settings.turnID,
			TargetName:       settings.targetName,
			CurrentTurn:      false,
			PromptProjection: settings.targetName != "",
		},
		settings.policy,
		settings.requireDurable,
	)
	return normalized, err
}

// MessageIdentityWriteback records an ID assigned during request normalization.
type MessageIdentityWriteback struct {
	Segment     SegmentName
	Index       int
	ID          string
	Before      Message
	After       Message
	CurrentTurn bool
}

func cloneIdentityWritebacks(in []MessageIdentityWriteback) []MessageIdentityWriteback {
	if len(in) == 0 {
		return nil
	}
	out := make([]MessageIdentityWriteback, len(in))
	for i, wb := range in {
		out[i] = MessageIdentityWriteback{
			Segment:     wb.Segment,
			Index:       wb.Index,
			ID:          wb.ID,
			Before:      wb.Before.Clone(),
			After:       wb.After.Clone(),
			CurrentTurn: wb.CurrentTurn,
		}
	}
	return out
}

// CompileWritebackIntent describes normalized state the caller may persist.
type CompileWritebackIntent struct {
	Snapshot ConversationSnapshot
	Messages []MessageIdentityWriteback
}

func (w CompileWritebackIntent) clone() CompileWritebackIntent {
	return CompileWritebackIntent{
		Snapshot: w.Snapshot.AllSegmentsSnapshot(),
		Messages: cloneIdentityWritebacks(w.Messages),
	}
}
