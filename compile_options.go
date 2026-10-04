package contexty

import (
	"context"
	"errors"
	"fmt"
	"maps"
)

// CompileOption configures ephemeral compile-time behavior on CompileRequest.
type CompileOption func(*compileOptions)

var (
	ErrInvalidTextReplacement   = errors.New("contexty: invalid text replacement")
	ErrMissingReplacementTarget = errors.New("contexty: text replacement target missing")
)

// TextReplacement addresses an exact compile-only message by its ID.
// It retains non-text parts and metadata. Source and persistence remain unchanged.
type TextReplacement struct {
	Segment   SegmentName `json:"segment"`
	MessageID string      `json:"message_id"`
	Text      string      `json:"text"`
}

type compileOptions struct {
	replacements        []TextReplacement
	resolveVars         map[string]string
	historicalArguments []HistoricalArgumentProjection
}

// WithHistoricalArgumentProjection applies a confirmed offload only to prompt
// history before budgeting. CompileRequest.History remains the original source.
func WithHistoricalArgumentProjection(projection HistoricalArgumentProjection) CompileOption {
	frozen := cloneHistoricalArguments(projection)
	return func(o *compileOptions) {
		o.historicalArguments = append(o.historicalArguments, cloneHistoricalArguments(frozen))
	}
}

// WithTextReplacement replaces text on an exact message ID in the selected segment.
// History replacements run after budget; other segments run before hooks/budget.
// Missing targets fail rather than falling back to another message. Prefer
// CurrentTurn for the active turn's prompt-safe projection.
func WithTextReplacement(replacement TextReplacement) CompileOption {
	return func(o *compileOptions) {
		o.replacements = append(o.replacements, replacement)
	}
}

// WithResolveVar injects a read-only variable for DeferredBlock.Resolve via CompileResolveVarFromContext.
func WithResolveVar(key, value string) CompileOption {
	return func(o *compileOptions) {
		if o.resolveVars == nil {
			o.resolveVars = make(map[string]string)
		}
		o.resolveVars[key] = value
	}
}

func applyCompileOptions(opts []CompileOption) compileOptions {
	var out compileOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&out)
		}
	}
	return out
}

type compileResolveVarsKey struct{}

func withCompileResolveVars(ctx context.Context, vars map[string]string) context.Context {
	if len(vars) == 0 {
		return ctx
	}
	cp := make(map[string]string, len(vars))
	maps.Copy(cp, vars)
	return context.WithValue(ctx, compileResolveVarsKey{}, cp)
}

// CompileResolveVarFromContext returns a copy of resolve vars from CompileRequest.Options.
func CompileResolveVarFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	vars, ok := ctx.Value(compileResolveVarsKey{}).(map[string]string)
	if !ok || len(vars) == 0 {
		return nil
	}
	return maps.Clone(vars)
}

type patchPhase int

const (
	patchPhasePreBudget patchPhase = iota
	patchPhasePostBudget
)

func applyCompileReplacements(ctx context.Context, snapshot ConversationSnapshot,
	options compileOptions, phase patchPhase,
) (ConversationSnapshot, error) {
	updated, err := applyTextReplacements(ctx, snapshot, options, phase)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	if len(options.replacements) == 0 {
		return updated, nil
	}
	return traceSnapshot(ctx, "patch", snapshot, updated)
}

func applyTextReplacements(
	ctx context.Context,
	snap ConversationSnapshot,
	opts compileOptions,
	phase patchPhase,
) (ConversationSnapshot, error) {
	next := snap
	for _, replacement := range opts.replacements {
		if err := ctx.Err(); err != nil {
			return ConversationSnapshot{}, err
		}
		seg := replacement.Segment
		if phase == patchPhasePreBudget && seg == SegmentHistory {
			continue
		}
		if phase == patchPhasePostBudget && seg != SegmentHistory {
			continue
		}
		msgs := next.Segment(seg)
		index := -1
		for i, msg := range msgs {
			if msg.ID == replacement.MessageID {
				index = i
				break
			}
		}
		if index < 0 {
			return ConversationSnapshot{}, fmt.Errorf(
				"%w: segment %s, ID %s",
				ErrMissingReplacementTarget,
				seg,
				replacement.MessageID,
			)
		}
		updated := cloneMessageSlice(msgs)
		cloned := patchTextOnMessage(updated[index], replacement.Text)
		recordTextReplacementCtx(ctx, updated[index], cloned)
		updated[index] = cloned
		next = next.WithSegment(seg, updated)
	}
	return next, nil
}

func patchTextOnMessage(m Message, text string) Message {
	cloned := m.Clone()
	var nonText []ContentPart
	for _, p := range cloned.Parts {
		if _, ok := p.(TextPart); !ok {
			nonText = append(nonText, p)
		}
	}
	cloned.Parts = append([]ContentPart{TextPart{Text: text}}, nonText...)
	return cloned
}

func recordTextReplacementCtx(ctx context.Context, before, after Message) {
	rec := transformRecorderFrom(ctx)
	if rec == nil || before.ID == "" {
		return
	}
	if MessageEqual(before, after) {
		return
	}
	rec.introduceIfAbsent(before)
	rec.set(before.ID, ActionFormatted, ReasonTextReplacement)
}

func outputOptions(options compileOptions, snap ConversationSnapshot) compileOptions {
	out := options
	out.replacements = nil
	for _, replacement := range options.replacements {
		for _, message := range snap.Segment(replacement.Segment) {
			if message.ID == replacement.MessageID {
				out.replacements = append(out.replacements, replacement)
				break
			}
		}
	}
	return out
}
