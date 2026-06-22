package contexty

import (
	"context"
	"fmt"
	"strings"
)

// MessageIdentityContext describes where an ID is needed during normalization.
type MessageIdentityContext struct {
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

// StableMessageIdentityPolicy returns deterministic IDs derived from message position and content.
type StableMessageIdentityPolicy struct {
	Prefix string
}

// NewStableMessageIdentityPolicy builds a deterministic identity policy.
func NewStableMessageIdentityPolicy(prefix string) StableMessageIdentityPolicy {
	return StableMessageIdentityPolicy{Prefix: prefix}
}

// ResolveMessageID implements MessageIdentityPolicy.
func (p StableMessageIdentityPolicy) ResolveMessageID(ctx MessageIdentityContext, msg Message) (string, error) {
	prefix := strings.TrimSpace(p.Prefix)
	if prefix == "" {
		prefix = "msg"
	}
	segment := strings.TrimSpace(string(ctx.Segment))
	if segment == "" {
		segment = "message"
	}
	scope := segment
	if ctx.TargetName != "" {
		scope = "target:" + strings.ReplaceAll(ctx.TargetName, " ", "_") + ":" + segment
	}
	if ctx.CurrentTurn {
		scope = "current_turn"
	}
	if ctx.PromptProjection {
		scope += ":projection"
	}
	return fmt.Sprintf("%s:%s:%d:%s", prefix, scope, ctx.Index, messageFingerprint(msg)), nil
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
		return EnsureMessageID(msg), nil
	}
	normalized, _, err := normalizeMessageForCompile(
		msg,
		MessageIdentityContext{
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
