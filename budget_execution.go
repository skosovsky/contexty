package contexty

import "context"

// Apply executes a budget plan once and returns its output and decision.
func (p *BudgetPipeline) Apply(ctx context.Context, messages []Message) (BudgetResult, error) {
	limit, err := p.cfg.Budget.Resolve()
	if err != nil {
		return BudgetResult{}, err
	}
	return p.ApplyWithLimit(ctx, messages, limit)
}

// ApplyWithLimit uses the actual remaining capacity after compile reservations.
func (p *BudgetPipeline) ApplyWithLimit(ctx context.Context, messages []Message, limit int) (BudgetResult, error) {
	if err := p.validateBudgetInvocation(ctx, limit); err != nil {
		return BudgetResult{}, err
	}
	var err error
	ctx = ensureBudgetObservation(ctx, p.observer)
	if err = ctx.Err(); err != nil {
		return BudgetResult{}, err
	}
	cur, err := ownCompileMessages(messages)
	if err != nil {
		return BudgetResult{}, err
	}
	rounds, err := p.inspectBudgetRounds(cur)
	if err != nil {
		return BudgetResult{}, err
	}
	selected, err := p.selectRequired(ctx, cur, rounds)
	if err != nil {
		return BudgetResult{}, err
	}
	decision := BudgetDecision{HardLimit: limit, TriggerTokens: limit, TargetTokens: limit,
		BeforeTokens: 0, AfterTokens: 0, Required: nil, Summary: nil, Compacted: false, TargetReached: false}
	if p.cfg.Compaction != nil {
		decision.TriggerTokens = budgetPercent(limit, p.cfg.Compaction.TriggerPercent)
		decision.TargetTokens = budgetPercent(limit, p.cfg.Compaction.TargetPercent)
	}
	decision.BeforeTokens, err = p.observeBudgetMessages(ctx, cur)
	if err != nil {
		return BudgetResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return BudgetResult{}, err
	}
	p.protectCompileBudgetSuffix(cur, selected, rounds)
	if err = validateUniqueMessageIDs(cur); err != nil {
		return BudgetResult{}, err
	}
	required, _ := splitRequired(cur, selected)
	explicit, requiredErr := p.explicitRequiredMessages(ctx, required)
	if requiredErr != nil {
		return BudgetResult{}, requiredErr
	}
	decision.Required, err = p.requiredRefs(ctx, explicit)
	if err != nil {
		return BudgetResult{}, err
	}
	if decision.BeforeTokens > decision.TriggerTokens {
		cur, err = p.executeOverflow(ctx, cur, selected, &decision)
		if err != nil {
			return BudgetResult{}, err
		}
	} else {
		decision.AfterTokens = decision.BeforeTokens
	}
	decision.TargetReached = decision.AfterTokens <= decision.TargetTokens
	if err = p.validateRequiredOutput(ctx, cur, decision.Required); err != nil {
		return BudgetResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return BudgetResult{}, err
	}
	recordBudgetDecision(ctx, decision)
	return BudgetResult{Messages: cur, Decision: decision.clone()}, nil
}

func (p *BudgetPipeline) observeBudgetMessages(ctx context.Context, messages []Message) (int, error) {
	cost, err := p.estimateBudgetMessages(ctx, messages)
	if err != nil {
		return 0, err
	}
	observeBudgetInput(ctx, cost)
	return cost, nil
}

func observeBudgetInput(ctx context.Context, tokens int) {
	if observer := observerFrom(ctx); observer != nil {
		blockID := budgetBlockIDFrom(ctx)
		if blockID == "" {
			blockID = string(EvictionReasonBudget)
		}
		observer.OnTokensEstimated(ctx, blockID, tokens)
	}
}

func (p *BudgetPipeline) executeOverflow(
	ctx context.Context,
	before []Message,
	selected []bool,
	decision *BudgetDecision,
) ([]Message, error) {
	required, evictable := splitRequired(before, selected)
	if out, handled, evictionErr := p.tryCompleteEviction(ctx, before, selected, required, decision); handled {
		return out, evictionErr
	}
	reserved := 0
	var err error
	if len(required) > 0 {
		reserved, err = p.estimateBudgetMessages(ctx, required)
		if err != nil {
			return nil, err
		}
	}
	if reserved > decision.HardLimit {
		return nil, p.requiredOverflowError(before)
	}
	if len(evictable) == 0 {
		decision.AfterTokens = decision.BeforeTokens
		if decision.AfterTokens > decision.HardLimit {
			return nil, p.requiredOverflowError(before)
		}
		return cloneMessageSlice(before), nil
	}
	maxTokens := decision.HardLimit - reserved
	targetTokens := max(0, decision.TargetTokens-reserved)
	var output []Message
	if p.cfg.Summarizer != nil {
		output, err = p.summarizeOptional(ctx, before, evictable, maxTokens, targetTokens, decision)
	} else {
		output, err = p.evictOptional(ctx, evictable, maxTokens)
	}
	if err != nil {
		return nil, err
	}
	out, err := mergeRequired(before, selected, output, decision.Compacted)
	if err != nil {
		return nil, err
	}
	if _, err = p.inspectBudgetRounds(out); err != nil {
		return nil, err
	}
	decision.AfterTokens, err = p.estimateBudgetMessages(ctx, out)
	if err != nil {
		return nil, err
	}
	if decision.AfterTokens > decision.HardLimit {
		return nil, ErrBudgetExceeded
	}
	return out, nil
}

func (p *BudgetPipeline) summarizeOptional(ctx context.Context, before, evictable []Message,
	maxTokens, targetTokens int, decision *BudgetDecision) ([]Message, error) {
	cost, err := p.estimateBudgetMessages(ctx, evictable)
	if err != nil {
		return nil, err
	}
	summaryBudget := SummaryBudget{MaxTokens: maxTokens, TargetTokens: targetTokens, Purpose: p.summaryPurpose()}
	decision.Summary = &summaryBudget
	ctx = context.WithValue(ctx, compactionExecutionKey{}, CompactionExecution{
		Summary:   summaryBudget,
		Retention: p.cfg.Retention.clone(),
		Policy:    cloneCompactionPolicy(p.cfg.Compaction),
		Required:  decision.Required,
	})
	output, _, err := p.summarize(ctx, evictable, cost, maxTokens, targetTokens)
	decision.Compacted = true
	if err != nil {
		return nil, err
	}
	if err = validateEvictableRounds(output); err != nil {
		return nil, err
	}
	if p.rolling != nil {
		if err = validateRollingSummaryIdentity(output[0], before); err != nil {
			return nil, err
		}
	}
	return output, nil
}

func (p *BudgetPipeline) evictOptional(ctx context.Context, messages []Message, limit int) ([]Message, error) {
	cost, err := p.estimateBudgetMessages(ctx, messages)
	if err != nil {
		return nil, err
	}
	if cost <= limit {
		return messages, nil
	}
	strategy := p.cfg.TruncateStrategy
	if strategy == nil {
		strategy = NewDropHeadStrategy(p.cfg.DropHead)
	}
	output, err := strategy.Apply(ctx, evictionCallbackInput(strategy, messages), cost, limit, p.estimator)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, err
	}
	if err = validateEvictableRounds(output); err != nil {
		return nil, err
	}
	return ownCompileMessages(output)
}

func (p *BudgetPipeline) requiredOverflowError(messages []Message) error {
	if len(p.cfg.Retention.MessageIDs)+len(p.cfg.Retention.ContentRefs)+len(p.cfg.Retention.Roles) > 0 {
		return ErrRetentionExceedsBudget
	}
	if p.rolling != nil {
		return ErrRecentTailExceedsBudget
	}
	if rounds, err := InspectToolRoundStates(messages, nil); err == nil {
		for _, round := range rounds {
			if round.State == ToolRoundPending {
				return ErrPendingExceedsBudget
			}
		}
	}
	return ErrBudgetExceeded
}

func (p *BudgetPipeline) tryCompleteEviction(
	ctx context.Context,
	before []Message,
	selected []bool,
	required []Message,
	decision *BudgetDecision,
) ([]Message, bool, error) {
	if p.cfg.Summarizer != nil || len(required) == 0 || intrinsicAdditiveEstimator(p.estimator) {
		return nil, false, nil
	}
	out, handled, err := p.evictCompleteCandidate(ctx, before, selected, decision.HardLimit)
	if !handled || err != nil {
		return out, handled, err
	}
	if _, roundErr := p.inspectBudgetRounds(out); roundErr != nil {
		return nil, true, roundErr
	}
	decision.AfterTokens, err = p.estimateBudgetMessages(ctx, out)
	if err == nil && decision.AfterTokens > decision.HardLimit {
		return nil, true, ErrBudgetExceeded
	}
	return out, true, err
}

func (p *BudgetPipeline) validateBudgetInvocation(ctx context.Context, limit int) error {
	if err := p.validateBudgetPolicy(); err != nil {
		return err
	}
	hard, err := p.cfg.Budget.Resolve()
	if err != nil {
		return err
	}
	if limit < 0 || limit > hard {
		return ErrInvalidBudgetRequest
	}
	return p.validateCompactionContext(ctx)
}

func evictionCallbackInput(strategy EvictionStrategy, messages []Message) []Message {
	switch strategy.(type) {
	case *dropHeadStrategy, *dropTailStrategy, *dropStrategy, *strictStrategy:
		return messages
	default:
		return cloneMessageSlice(messages)
	}
}
