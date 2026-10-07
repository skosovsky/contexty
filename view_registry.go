package contexty

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ViewConfiguration defines a named read-only projection over a snapshot segment.
type ViewConfiguration struct {
	SourceSegment SegmentName
	Budget        *BudgetPipeline // nil = no limit
	Formatter     SegmentFormatter
}

// WithNamedView registers a read-only snapshot view on the engine.
// Prefer CompileRequest.Targets for projections that must share the compile pipeline.
func WithNamedView(name string, cfg ViewConfiguration) EngineOption {
	return func(e *Engine) {
		_, reserved := builtinViewFormatter(name)
		_, duplicate := e.views[name]
		if name == "" || reserved || duplicate || (cfg.SourceSegment != "" && !isKnownSegment(cfg.SourceSegment)) {
			e.configurationErr = errors.Join(
				e.configurationErr,
				fmt.Errorf("%w: invalid or duplicate view %q", ErrInvalidCompileConfiguration, name),
			)
			return
		}
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

// RenderView projects an already-materialized snapshot without mutating snap.
// It does not run compile hooks, current-turn projection, or compile targets.
func (e *Engine) RenderView(ctx context.Context, snap ConversationSnapshot, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("contexty: render view: %w", err)
	}
	if err := validateSnapshotRoles(snap); err != nil {
		return "", err
	}
	if e.configurationErr != nil {
		return "", e.configurationErr
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
	return e.renderNamedView(ctx, snap, cfg)
}

func (e *Engine) renderNamedView(
	ctx context.Context,
	snap ConversationSnapshot,
	cfg ViewConfiguration,
) (string, error) {
	seg := cfg.SourceSegment
	if seg == "" {
		seg = SegmentHistory
	}
	msgs := snap.Segment(seg)
	working := cloneMessageSlice(msgs)
	var required []ContentRef
	if err := e.projectMessageRoles(ctx, working); err != nil {
		return "", err
	}
	if cfg.Budget != nil {
		trimmed, err := cfg.Budget.Apply(ctx, working)
		if err != nil {
			return "", fmt.Errorf("contexty: render view budget: %w", err)
		}
		working = trimmed.Messages
		required = trimmed.Decision.Required
	}
	if cfg.Formatter != nil {
		formatted, err := formatViewMessages(ctx, working, cfg.Formatter)
		if err != nil {
			return "", err
		}
		working = formatted
	}
	if cfg.Budget != nil {
		if err := cfg.Budget.validateRequiredOutput(ctx, working, required); err != nil {
			return "", err
		}
		if err := cfg.Budget.validateOutput(ctx, working); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, m := range working {
		b.WriteString(formatPartsPlain(m.Parts))
	}
	return b.String(), nil
}

func (e *Engine) projectMessageRoles(ctx context.Context, messages []Message) error {
	if e.roleProjection == nil {
		return nil
	}
	for i := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		role, err := e.roleProjection.ProjectRole(messages[i].Clone())
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil {
			return fmt.Errorf("contexty: role projection: %w", err)
		}
		if err := role.Validate(); err != nil {
			return err
		}
		messages[i].Role = role
	}
	return nil
}

func (e *Engine) resolveView(name string) (ViewConfiguration, bool) {
	if e.views != nil {
		if cfg, ok := e.views[name]; ok {
			return cfg, true
		}
	}
	return ViewConfiguration{}, false
}

func formatViewMessages(ctx context.Context, messages []Message, formatter SegmentFormatter) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	formatted, err := formatter(ctx, cloneMessageSlice(messages))
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, fmt.Errorf("contexty: render view formatter: %w", err)
	}
	return ownCompileMessages(formatted)
}
