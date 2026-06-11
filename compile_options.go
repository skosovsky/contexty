package contexty

import (
	"context"
	"maps"
)

// CompileOption configures ephemeral compile-time behavior on CompileRequest.
type CompileOption func(*compileOptions)

// MessagePosition selects which messages match a selector.
type MessagePosition int

const (
	PositionFirst MessagePosition = iota
	PositionLast
	PositionAll
)

// MessageSelector targets messages for ephemeral patches (compile-only, not persisted).
type MessageSelector struct {
	Segment  SegmentName
	Role     Role // zero = any role
	Position MessagePosition
}

type compileOptions struct {
	patches     []ephemeralPatch
	resolveVars map[string]string
}

type ephemeralPatch struct {
	selector MessageSelector
	text     string
}

// WithEphemeralPatch applies text replacement to messages matching sel during compile only.
func WithEphemeralPatch(sel MessageSelector, text string) CompileOption {
	return func(o *compileOptions) {
		o.patches = append(o.patches, ephemeralPatch{selector: sel, text: text})
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

func applyEphemeralPatches(
	ctx context.Context,
	snap ConversationSnapshot,
	opts compileOptions,
	phase patchPhase,
) ConversationSnapshot {
	if len(opts.patches) == 0 {
		return snap
	}
	next := snap
	for _, patch := range opts.patches {
		seg := patch.selector.Segment
		if seg == "" {
			continue
		}
		if phase == patchPhasePreBudget && seg == SegmentHistory {
			continue
		}
		if phase == patchPhasePostBudget && seg != SegmentHistory {
			continue
		}
		msgs := next.Segment(seg)
		indices := resolveSelectorIndices(msgs, patch.selector)
		if len(indices) == 0 {
			continue
		}
		updated := cloneMessageSlice(msgs)
		for _, idx := range indices {
			cloned := patchTextOnMessage(updated[idx], patch.text)
			recordEphemeralPatchCtx(ctx, updated[idx], cloned)
			updated[idx] = cloned
		}
		next = next.WithSegment(seg, updated)
	}
	return next
}

func resolveSelectorIndices(msgs []Message, sel MessageSelector) []int {
	if len(msgs) == 0 {
		return nil
	}
	var candidates []int
	for i, m := range msgs {
		if sel.Role != "" && m.Role != sel.Role {
			continue
		}
		candidates = append(candidates, i)
	}
	if len(candidates) == 0 {
		return nil
	}
	switch sel.Position {
	case PositionFirst:
		return []int{candidates[0]}
	case PositionLast:
		return []int{candidates[len(candidates)-1]}
	case PositionAll:
		return candidates
	default:
		return []int{candidates[len(candidates)-1]}
	}
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

func recordEphemeralPatchCtx(ctx context.Context, before, after Message) {
	rec := transformRecorderFrom(ctx)
	if rec == nil || before.ID == "" {
		return
	}
	if MessageEqual(before, after) {
		return
	}
	rec.introduceIfAbsent(before)
	rec.setUnlessFinal(before.ID, ActionFormatted, ReasonEphemeralPatch)
}
