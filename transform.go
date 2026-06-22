package contexty

import (
	"context"
	"fmt"
	"regexp"
)

// TransformHook mutates a snapshot copy-on-write; original store state is untouched.
type TransformHook interface {
	Transform(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error)
}

// TransformPipeline runs hooks in order, each receiving the previous output.
func TransformPipeline(
	ctx context.Context,
	snap ConversationSnapshot,
	hooks ...TransformHook,
) (ConversationSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ConversationSnapshot{}, err
	}
	cur := snap
	for i, hook := range hooks {
		if hook == nil {
			continue
		}
		stageCtx := withRecordingComponent(ctx, recordingKey(RecordingHook, "", "", i), "hook")
		if err := ctx.Err(); err != nil {
			return ConversationSnapshot{}, err
		}
		next, err := hook.Transform(stageCtx, cur)
		if canceled := ctx.Err(); canceled != nil {
			return ConversationSnapshot{}, canceled
		}
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: transform hook %d: %w", i, err)
		}
		if _, compiling := compileIdentityFromContext(ctx); compiling {
			next, err = normalizeSnapshotMessageIDs(ctx, next)
			if err != nil {
				return ConversationSnapshot{}, err
			}
		}
		if traceFromContext(ctx) != nil {
			next, err = traceSnapshot(stageCtx, "hook", cur, next)
			if err != nil {
				return ConversationSnapshot{}, err
			}
		}
		recordSnapshotHookTransforms(ctx, cur, next)
		cur = next
	}
	return cur, nil
}

// RedactionHook masks PII-like patterns in text parts (copy-on-write).
type RedactionHook struct {
	Replacer func(string) string
}

// NewRedactionHook returns a hook that replaces email-like substrings.
func NewRedactionHook() RedactionHook {
	return RedactionHook{Replacer: maskEmailLike}
}

var emailPattern = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

func maskEmailLike(s string) string {
	if emailPattern.MatchString(s) {
		return emailPattern.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// Transform applies redaction across all segments without mutating the input snapshot.
//
//nolint:gocognit // per-segment and per-part copy-on-write traversal.
func (h RedactionHook) Transform(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ConversationSnapshot{}, fmt.Errorf("contexty: redaction: %w", err)
	}
	replacer := h.Replacer
	if replacer == nil {
		replacer = maskEmailLike
	}
	next := snap
	for _, name := range snap.SegmentNames() {
		msgs := snap.Segment(name)
		out := make([]Message, len(msgs))
		segChanged := false
		for i, m := range msgs {
			out[i] = m
			newParts := make([]ContentPart, len(m.Parts))
			msgChanged := false
			for j, p := range m.Parts {
				if t, ok := p.(TextPart); ok {
					masked := replacer(t.Text)
					if masked != t.Text {
						msgChanged = true
						newParts[j] = TextPart{Text: masked}
					} else {
						newParts[j] = p
					}
				} else {
					newParts[j] = p
				}
			}
			if msgChanged {
				out[i].Parts = newParts
				segChanged = true
			}
		}
		if segChanged {
			next = next.WithSegment(name, out)
		}
	}
	return next, nil
}
