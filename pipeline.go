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
		seg := budgetIdentitySegmentFrom(ctx)
		summary, err = ensureMessageIDFromContext(ctx, seg, 0, summary)
		if err != nil {
			return nil, fmt.Errorf("contexty: budget summarize identity: %w", err)
		}
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
func enforceToolPairAtomicity(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]Message, 0, len(msgs))
	for i := 0; i < len(msgs); {
		switch {
		case msgs[i].Role == RoleTool:
			i++
		case msgs[i].Role == RoleAssistant && msgs[i].HasToolCalls():
			round, err := ToolRoundFromMessages(msgs, i)
			end := contiguousToolBlockEnd(msgs, i)
			if err == nil {
				out = append(out, round.Assistant)
				out = append(out, round.Results...)
			}
			i = end + 1
		default:
			out = append(out, msgs[i].Clone())
			i++
		}
	}
	return out
}

func contiguousToolBlockEnd(msgs []Message, assistantIdx int) int {
	end := assistantIdx
	for idx := assistantIdx + 1; idx < len(msgs); idx++ {
		if msgs[idx].Role != RoleTool {
			break
		}
		end = idx
	}
	return end
}

func toolRoundEndIndex(msgs []Message, assistantIdx int) int {
	round, err := ToolRoundFromMessages(msgs, assistantIdx)
	if err != nil {
		return assistantIdx
	}
	return assistantIdx + len(round.Results)
}

func activeToolRoundEndIndex(cur []Message, startIdx int, deleted []bool) int {
	if startIdx < 0 || startIdx >= len(cur) {
		return startIdx
	}
	visible := make([]Message, 0, len(cur)-startIdx)
	indexes := make([]int, 0, len(cur)-startIdx)
	for idx := startIdx; idx < len(cur); idx++ {
		if deleted != nil && deleted[idx] {
			continue
		}
		if idx != startIdx && cur[idx].Role != RoleTool {
			break
		}
		visible = append(visible, cur[idx].Clone())
		indexes = append(indexes, idx)
	}
	round, err := ToolRoundFromMessages(visible, 0)
	if err != nil {
		return startIdx
	}
	lastVisible := len(round.Results)
	if lastVisible >= len(indexes) {
		return startIdx
	}
	return indexes[lastVisible]
}

func toolRoundStartForTail(msgs []Message) int {
	last := len(msgs) - 1
	start := last
	for start > 0 && msgs[start-1].Role == RoleTool {
		start--
	}
	if start > 0 && msgs[start-1].Role == RoleAssistant && msgs[start-1].HasToolCalls() {
		assistantIdx := start - 1
		if toolRoundEndIndex(msgs, assistantIdx) == last {
			return assistantIdx
		}
	}
	return last
}
