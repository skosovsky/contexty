package contexty

import (
	"context"
	"fmt"
)

// Summarizer compresses a message block into a single summary message.
type Summarizer interface {
	Summarize(ctx context.Context, msgs []Message) (Message, error)
}

// BudgetConfig controls unified summarize + truncate pipeline.
type BudgetConfig struct {
	TokenLimit       int
	Summarizer       Summarizer
	TruncateStrategy EvictionStrategy
	// DropHead configures drop-head truncation when TruncateStrategy is nil.
	DropHead DropHeadConfig
}

// BudgetPipeline applies semantic compression then mechanical truncation.
type BudgetPipeline struct {
	cfg       BudgetConfig
	estimator TokenEstimator
	observer  Observer
}

// NewBudgetPipeline returns a pipeline with the given config and estimator.
func NewBudgetPipeline(cfg BudgetConfig, estimator TokenEstimator, opts ...BudgetPipelineOption) *BudgetPipeline {
	if estimator == nil {
		estimator = CharTokenEstimator{}
	}
	p := &BudgetPipeline{cfg: cfg, estimator: estimator, observer: nil}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Apply runs summarize (if configured) then truncate until within limit.
func (p *BudgetPipeline) Apply(ctx context.Context, msgs []Message) ([]Message, error) {
	return p.ApplyWithLimit(ctx, msgs, p.cfg.TokenLimit)
}

// ApplyWithLimit runs the budget pipeline against an effective token limit (e.g. history slice after preflight).
func (p *BudgetPipeline) ApplyWithLimit(ctx context.Context, msgs []Message, tokenLimit int) ([]Message, error) {
	ctx = ensureBudgetObservation(ctx, p.observer)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: budget: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	cur := cloneMessageSlice(msgs)
	tokens, err := p.estimator.Estimate(ctx, cur)
	if err != nil {
		return nil, fmt.Errorf("contexty: budget: %w: %w", ErrTokenCountFailed, err)
	}
	if obs := observerFrom(ctx); obs != nil {
		blockID := budgetBlockIDFrom(ctx)
		if blockID == "" {
			blockID = "budget"
		}
		obs.OnTokensEstimated(ctx, blockID, tokens)
	}
	if tokens <= tokenLimit {
		return cur, nil
	}
	if p.cfg.Summarizer != nil {
		originalTokens := tokens
		beforeSum := cur
		summary, sumErr := p.cfg.Summarizer.Summarize(ctx, cur)
		if sumErr != nil {
			return nil, fmt.Errorf("contexty: budget summarize: %w", sumErr)
		}
		summary = EnsureMessageID(summary)
		recordSummarizeReplaceCtx(ctx, beforeSum, summary)
		sumTokens, estErr := p.estimator.Estimate(ctx, []Message{summary})
		if estErr != nil {
			return nil, fmt.Errorf("contexty: budget: %w: %w", ErrTokenCountFailed, estErr)
		}
		emitContextSummarized(ctx, originalTokens, sumTokens)
		if sumTokens <= tokenLimit {
			return []Message{summary}, nil
		}
		cur = []Message{summary}
		tokens = sumTokens
	}
	strategy := p.cfg.TruncateStrategy
	if strategy == nil {
		strategy = NewDropHeadStrategy(p.cfg.DropHead)
	}
	out, truncErr := strategy.Apply(ctx, cur, tokens, tokenLimit, p.estimator)
	if truncErr != nil {
		return nil, truncErr
	}
	if out != nil {
		beforeRepair := out
		out = enforceToolPairAtomicity(out)
		reportEvictions(ctx, beforeRepair, out, EvictionReasonOrphanRepair)
	}
	return out, nil
}

func emitContextSummarized(ctx context.Context, beforeTokens, afterTokens int) {
	obs := observerFrom(ctx)
	if obs == nil || afterTokens <= 0 {
		return
	}
	obs.OnContextSummarized(ctx, float64(beforeTokens)/float64(afterTokens))
}

// enforceToolPairAtomicity drops orphan tool results or assistant calls without results.
//
//nolint:gocognit // scans assistant/tool runs for paired tool call IDs.
func enforceToolPairAtomicity(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]Message, len(msgs))
	copy(out, msgs)
	out = stripOrphanToolMessages(out)
	for i := 0; i < len(out); i++ {
		if out[i].Role != RoleAssistant || !out[i].HasToolCalls() {
			continue
		}
		calls := out[i].ToolCallParts()
		expected := make(map[string]bool, len(calls))
		for _, c := range calls {
			if c.ID != "" {
				expected[c.ID] = true
			}
		}
		end := i
		for j := i + 1; j < len(out); j++ {
			if out[j].Role != RoleTool {
				break
			}
			for _, tr := range out[j].ToolResultParts() {
				delete(expected, tr.ToolCallID)
			}
			end = j
		}
		if len(expected) > 0 {
			out = append(out[:i], out[end+1:]...)
			i--
		} else {
			i = end
		}
	}
	return out
}

func stripOrphanToolMessages(msgs []Message) []Message {
	out := msgs
	for i := 0; i < len(out); {
		if out[i].Role != RoleTool {
			i++
			continue
		}
		if i == 0 || out[i-1].Role != RoleAssistant || !out[i-1].HasToolCalls() {
			out = append(out[:i], out[i+1:]...)
			continue
		}
		i++
	}
	return out
}
