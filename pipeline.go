package contexty

import (
	"context"
	"fmt"
)

// Summarizer compresses a message block into a single summary message.
type Summarizer interface {
	Summarize(ctx context.Context, request SummaryRequest) (Message, error)
}

// BudgetConfig controls retention and a single summary or eviction execution.
type BudgetConfig struct {
	Retention        RetentionPolicy
	Compaction       *CompactionPolicy
	Budget           BudgetRequest
	Summarizer       Summarizer
	TruncateStrategy EvictionStrategy
	// DropHead configures drop-head truncation when TruncateStrategy is nil.
	DropHead DropHeadConfig
}

// BudgetPipeline preserves required content and applies compression or eviction.
type BudgetPipeline struct {
	cfg                 BudgetConfig
	estimator           TokenEstimator
	observer            Observer
	compaction          *CompactionProfile
	rolling             *RollingSummaryPolicy
	truncation          *Descriptor
	summarizer          *Descriptor
	estimatorDescriptor *Descriptor
}

// NewBudgetPipeline returns a pipeline with the given config and estimator.
func NewBudgetPipeline(cfg BudgetConfig, estimator TokenEstimator, opts ...BudgetPipelineOption) *BudgetPipeline {
	if estimator == nil {
		estimator = CharTokenEstimator{}
	}
	cfg.DropHead = cfg.DropHead.normalized()
	cfg.Retention = cfg.Retention.clone()
	cfg.Compaction = cloneCompactionPolicy(cfg.Compaction)
	p := &BudgetPipeline{cfg: cfg, estimator: freezeBuiltinEstimator(estimator), observer: nil, compaction: nil,
		rolling: nil, truncation: nil, summarizer: nil, estimatorDescriptor: nil}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// validateOutput verifies the final representation without running transforms again.
func (p *BudgetPipeline) validateOutput(ctx context.Context, msgs []Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	limit, err := p.cfg.Budget.Resolve()
	if err != nil {
		return err
	}
	n, err := p.estimator.Estimate(ctx, estimatorCallbackInput(p.estimator, msgs))
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return fmt.Errorf("contexty: final budget estimate: %w: %w", ErrTokenCountFailed, err)
	}
	if n < 0 || n > limit {
		return ErrBudgetExceeded
	}
	recordFinalBudgetEstimate(ctx, n)
	return nil
}

func (p *BudgetPipeline) summarize(
	ctx context.Context,
	before []Message,
	originalTokens, tokenLimit, targetTokens int,
) ([]Message, int, error) {
	ctx = p.withSummarizerIdentity(ctx)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	request := SummaryRequest{
		Messages:     cloneMessageSlice(before),
		MaxTokens:    tokenLimit,
		TargetTokens: targetTokens,
		Purpose:      p.summaryPurpose(),
	}
	ctx = context.WithValue(ctx, summaryRequestKey{}, request.budget())
	summary, err := p.cfg.Summarizer.Summarize(ctx, request)
	if canceled := ctx.Err(); canceled != nil {
		return nil, 0, canceled
	}
	if err != nil {
		return nil, 0, fmt.Errorf("contexty: budget summarize: %w", err)
	}
	summary, err = ensureMessageIDFromContext(ctx, budgetIdentitySegmentFrom(ctx), 0, summary)
	if err != nil {
		return nil, 0, fmt.Errorf("contexty: budget summarize identity: %w", err)
	}
	traced, err := traceStage(ctx, "summarize", before, []Message{summary}, true)
	if err != nil {
		return nil, 0, err
	}
	recordSummarizeReplaceCtx(ctx, before, traced[0])
	tokens, err := p.estimateSummary(ctx, traced[0], tokenLimit)
	if err != nil {
		return nil, 0, fmt.Errorf("contexty: budget: %w: %w", ErrTokenCountFailed, err)
	}
	if tokens > tokenLimit {
		return nil, 0, ErrBudgetExceeded
	}
	emitContextSummarized(ctx, originalTokens, tokens)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return traced, tokens, nil
}

func emitContextSummarized(ctx context.Context, beforeTokens, afterTokens int) {
	obs := observerFrom(ctx)
	if obs == nil || afterTokens <= 0 {
		return
	}
	obs.OnContextSummarized(ctx, float64(beforeTokens)/float64(afterTokens))
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
