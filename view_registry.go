package contexty

import (
	"context"
	"fmt"
	"strings"
)

// ViewConfiguration defines a named read-only projection over a snapshot segment.
type ViewConfiguration struct {
	SourceSegment SegmentName
	Budget        *BudgetPipeline // nil = no limit
	Formatter     SegmentFormatter
}

// WithNamedView registers a named view on the engine.
func WithNamedView(name string, cfg ViewConfiguration) EngineOption {
	return func(e *Engine) {
		if e.views == nil {
			e.views = make(map[string]ViewConfiguration)
		}
		e.views[name] = cfg
	}
}

func defaultViewRegistry() map[string]ViewConfiguration {
	return map[string]ViewConfiguration{}
}

func builtinViewFormatter(name string) (ViewFormatter, bool) {
	switch name {
	case string(ViewLLMXML):
		return LLMXMLFormatter{}, true
	case string(ViewFlatClassifier):
		return FlatClassifierFormatter{}, true
	default:
		return nil, false
	}
}

// RenderView projects snap through a registered named view without mutating snap.
func (e *Engine) RenderView(ctx context.Context, snap ConversationSnapshot, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("contexty: render view: %w", err)
	}
	if f, ok := builtinViewFormatter(name); ok {
		projected, err := e.applyRoleProjection(ctx, snap)
		if err != nil {
			return "", err
		}
		return f.Format(ctx, projected)
	}
	cfg, ok := e.resolveView(name)
	if !ok {
		return "", fmt.Errorf("contexty: unknown view %q", name)
	}
	seg := cfg.SourceSegment
	if seg == "" {
		seg = SegmentHistory
	}
	msgs := snap.Segment(seg)
	working := cloneMessageSlice(msgs)
	if e.roleProjection != nil {
		for i := range working {
			role, err := e.roleProjection.ProjectRole(working[i])
			if err != nil {
				return "", fmt.Errorf("contexty: render view role projection: %w", err)
			}
			working[i].Role = role
		}
	}
	if cfg.Budget != nil {
		trimmed, err := cfg.Budget.Apply(ctx, working)
		if err != nil {
			return "", fmt.Errorf("contexty: render view budget: %w", err)
		}
		working = trimmed
	}
	if cfg.Formatter != nil {
		formatted, err := cfg.Formatter(ctx, working)
		if err != nil {
			return "", fmt.Errorf("contexty: render view formatter: %w", err)
		}
		working = formatted
	}
	var b strings.Builder
	for _, m := range working {
		b.WriteString(formatPartsPlain(m.Parts))
	}
	return b.String(), nil
}

func (e *Engine) resolveView(name string) (ViewConfiguration, bool) {
	if e.views != nil {
		if cfg, ok := e.views[name]; ok {
			return cfg, true
		}
	}
	return ViewConfiguration{}, false
}
