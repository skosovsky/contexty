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
	Budget           BudgetRequest
	Summarizer       Summarizer
	TruncateStrategy EvictionStrategy
	// DropHead configures drop-head truncation when TruncateStrategy is nil.
	DropHead DropHeadConfig
}

// BudgetPipeline applies semantic compression then mechanical truncation.
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

// Apply runs summarize (if configured) then truncate until within limit.
func (p *BudgetPipeline) Apply(ctx context.Context, msgs []Message) ([]Message, error) {
	limit, err := p.cfg.Budget.Resolve()
	if err != nil {
		return nil, err
	}
	return p.ApplyWithLimit(ctx, msgs, limit)
}

// ApplyWithLimit runs the budget pipeline against an effective token limit (e.g. history slice after preflight).
func (p *BudgetPipeline) ApplyWithLimit(ctx context.Context, msgs []Message, tokenLimit int) ([]Message, error) {
	limit, err := p.cfg.Budget.Resolve()
	if err != nil {
		return nil, err
	}
	if tokenLimit < 0 || tokenLimit > limit {
		return nil, ErrInvalidBudgetRequest
	}
	if err = p.validateRollingSummary(); err != nil {
		return nil, err
	}
	if err = p.validateCompactionContext(ctx); err != nil {
		return nil, err
	}
	ctx = ensureBudgetObservation(ctx, p.observer)
	if err = ctx.Err(); err != nil {
		return nil, fmt.Errorf("contexty: budget: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	cur := cloneMessageSlice(msgs)
	rounds, err := InspectToolRoundStates(cur, nil)
	if err != nil {
		return nil, err
	}
	tokens, err := p.estimateBudgetMessages(ctx, cur)
	if err != nil {
		return nil, err
	}
	if obs := observerFrom(ctx); obs != nil {
		blockID := budgetBlockIDFrom(ctx)
		if blockID == "" {
			blockID = string(EvictionReasonBudget)
		}
		obs.OnTokensEstimated(ctx, blockID, tokens)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if tokens <= tokenLimit {
		return cur, nil
	}
	return p.applyOverflowingHistory(ctx, cur, tokens, tokenLimit, rounds)
}

func (p *BudgetPipeline) applyOverflowingHistory(ctx context.Context, cur []Message,
	tokens, tokenLimit int, rounds []ToolRoundObservation,
) ([]Message, error) {
	if p.rolling != nil {
		start := rollingTailStart(len(cur), *p.rolling, rounds)
		return p.applyProtectedSuffix(ctx, cur, start, tokenLimit)
	}
	for _, round := range rounds {
		if round.State == ToolRoundPending {
			return p.applyProtectedSuffix(ctx, cur, round.Start, tokenLimit)
		}
	}
	return p.applyEvictable(ctx, cur, tokens, tokenLimit)
}

func (p *BudgetPipeline) applyEvictable(ctx context.Context, cur []Message, tokens, tokenLimit int) ([]Message, error) {
	var err error
	if p.cfg.Summarizer != nil {
		cur, tokens, err = p.summarize(ctx, cur, tokens, tokenLimit)
		if err != nil {
			return nil, err
		}
	}
	if tokens <= tokenLimit {
		if err = validateEvictableRounds(cur); err != nil {
			return nil, err
		}
		return cur, nil
	}
	strategy := p.cfg.TruncateStrategy
	if strategy == nil {
		strategy = NewDropHeadStrategy(p.cfg.DropHead)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, truncErr := strategy.Apply(ctx, cur, tokens, tokenLimit, p.estimator)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if truncErr != nil {
		return nil, truncErr
	}
	return p.validateEvictionOutput(ctx, out, tokenLimit)
}

func (p *BudgetPipeline) validateEvictionOutput(ctx context.Context, output []Message, limit int) ([]Message, error) {
	owned := cloneMessageSlice(output)
	if err := validateEvictableRounds(owned); err != nil {
		return nil, err
	}
	cost, err := p.estimateBudgetMessages(ctx, owned)
	if err != nil {
		return nil, err
	}
	if cost > limit {
		return nil, ErrBudgetExceeded
	}
	return owned, nil
}

func (p *BudgetPipeline) summarize(
	ctx context.Context,
	before []Message,
	originalTokens, tokenLimit int,
) ([]Message, int, error) {
	ctx = p.withSummarizerIdentity(ctx)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	summary, err := p.cfg.Summarizer.Summarize(ctx, cloneMessageSlice(before))
	if canceled := ctx.Err(); canceled != nil {
		return nil, 0, canceled
	}
	if err != nil {
		return nil, 0, fmt.Errorf("contexty: budget summarize: %w", err)
	}
	summary = summary.Clone()
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
