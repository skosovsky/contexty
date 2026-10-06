package evaluation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/skosovsky/contexty"
)

// Run executes all four strategies with the same pinned inputs and capacity.
func Run(ctx context.Context, cfg Config) (Report, error) {
	cfg.Fixtures = cloneFixtures(cfg.Fixtures)
	cfg.Profile = cloneProfile(cfg.Profile)
	reporter, err := validateConfig(cfg)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		Schema:     identity("context-evaluation-report"),
		Model:      cfg.Profile.Model,
		Summarizer: cfg.SummarizerIdentity,
		Evaluator:  cfg.EvaluatorIdentity,
		Estimator:  cfg.Profile,
		Runs:       1,
		Mode:       "offline",
		Limitations: []string{"Deterministic fixture preservation only; LLM quality is not measured.",
			"Elapsed host timing is diagnostic; CPU benchmarks run separately with go test -bench.",
			"Process-local archive/blob fixtures are not durable provider/backend integrations."},
		Rows: nil, Behaviors: nil,
	}
	for _, fixture := range cfg.Fixtures {
		checks, checkErr := behaviorChecks(ctx, fixture, cfg.Budget)
		if checkErr != nil {
			return Report{}, fmt.Errorf("fixture %s behavior: %w", fixture.Identity.ID, checkErr)
		}
		report.Behaviors = append(
			report.Behaviors,
			BehaviorEvidence{Fixture: fixture.Identity, Executions: 1, Checks: checks},
		)
		for _, strategy := range []string{slidingStrategy, rollingStrategy, offloadStrategy, retrievalStrategy} {
			row, rowErr := runRow(ctx, cfg, reporter, fixture, strategy)
			if rowErr != nil {
				return Report{}, fmt.Errorf("fixture %s strategy %s: %w", fixture.Identity.ID, strategy, rowErr)
			}

			report.Rows = append(report.Rows, row)
		}
	}
	return report, nil
}

func validateConfig(cfg Config) (*contexty.EstimateReporter, error) {
	if cfg.Budget <= 0 || len(cfg.Fixtures) == 0 || cfg.Estimator == nil ||
		cfg.SummarizerIdentity.Validate() != nil || cfg.EvaluatorIdentity.Validate() != nil {
		return nil, errors.New("evaluation: invalid pinned configuration")
	}
	seen := make(map[string]bool)
	for _, fixture := range cfg.Fixtures {
		if fixture.Identity.Validate() != nil || seen[fixture.Identity.ID] {
			return nil, errors.New("evaluation: duplicate or invalid fixture identity")
		}
		seen[fixture.Identity.ID] = true
	}
	return contexty.NewEstimateReporter(cfg.Estimator, cfg.Profile, contexty.DefaultJSONSerializer())
}

func runRow(ctx context.Context, cfg Config, reporter *contexty.EstimateReporter, fixture Fixture,
	strategy string) (Row, error) {
	started := time.Now()
	callbacks := Callbacks{}            //nolint:exhaustruct_v5 // Counters start at zero.
	request := contexty.CompileRequest{ //nolint:exhaustruct_v5 // Only history is needed initially.
		History: cloneMessages(fixture.Messages),
	}
	var err error
	if strategy == offloadStrategy {
		request, err = prepareOffload(ctx, fixture, &callbacks)
	}
	if strategy == retrievalStrategy {
		request, err = prepareRetrieval(ctx, fixture, reporter, cfg.Budget, &callbacks)
	}
	if err != nil {
		return Row{}, err
	}
	pipeline := budgetPipeline(cfg, reporter, fixture, strategy, &callbacks)
	var result contexty.CompileResult
	if strategy == rollingStrategy {
		result, err = compileRolling(ctx, request, pipeline)
	} else {
		result, err = compileRequest(ctx, request, pipeline)
	}
	if err != nil {
		return Row{}, err
	}
	messages := payloadMessages(result.Payload)
	estimateReport, err := reporter.Report(
		ctx,
		contexty.EstimateRequest{ //nolint:exhaustruct_v5 // No manifest or provider wire binding.
			Segments: []contexty.EstimateSegment{{Name: "issued", Messages: messages}},
			Budget:   contexty.EffectiveInputBudget(cfg.Budget),
		},
	)
	if err != nil {
		return Row{}, err
	}
	estimate := estimateReport.Total
	if estimate > cfg.Budget {
		return Row{}, contexty.ErrBudgetExceeded
	}
	selected, excluded, err := selectedEvidence(messages, fixture.Messages)
	if err != nil {
		return Row{}, err
	}
	checks := evaluateFixture(fixture, messages)
	if strategy == retrievalStrategy {
		for _, instruction := range fixture.ForbiddenInstructions {
			delivered := false
			for _, message := range messages {
				delivered = delivered ||
					(strings.Contains(messageText(message), instruction) && message.Role == contexty.RoleUser)
			}
			checks = append(checks, Check{
				Kind:   "retrieved-instruction-delivered-as-data",
				Passed: delivered,
				Detail: "Adversarial instruction bytes actually entered a user-role resource message; model behavior is not measured",
			})
		}
	}

	checks = append(checks, Check{Kind: "issued-budget", Passed: estimate <= cfg.Budget,
		Detail: fmt.Sprintf("actual semantic estimate %d <= %d", estimate, cfg.Budget)})
	return Row{
		Fixture:          fixture.Identity,
		Strategy:         identity(strategy),
		Budget:           cfg.Budget,
		Selected:         selected,
		Excluded:         excluded,
		Estimate:         estimate,
		EstimateQuality:  estimateReport.Quality,
		EstimateReport:   estimateReport,
		Policies:         strategyPolicies(strategy, cfg),
		PolicyParameters: strategyParameters(strategy),
		BudgetDecisions:  result.BudgetDecisions,
		Callbacks:        callbacks,
		Checks:           checks,
		Duration:         time.Since(started),
		ProviderQuality: notMeasured(
			"score",
		),
		ProviderUsage: notMeasured("provider tokens"),
		ProviderCost:  notMeasured("money"),
	}, nil
}

func budgetPipeline(cfg Config, reporter *contexty.EstimateReporter, fixture Fixture, strategy string,
	callbacks *Callbacks) *contexty.BudgetPipeline {
	budget := contexty.BudgetConfig{ //nolint:exhaustruct_v5 // Default drop-head strategy.
		Retention: contexty.RetentionPolicy{
			MessageIDs:  slices.Clone(fixture.RequiredIDs),
			ContentRefs: nil,
			Roles:       nil,
		},
		Budget: contexty.EffectiveInputBudget(cfg.Budget),
	}
	opts := []contexty.BudgetPipelineOption{contexty.WithTruncationDescriptor(identity("fixture-drop-head"))}
	if strategy == rollingStrategy {
		budget.Summarizer = countedSummarizer{inner: cfg.Summarizer, callbacks: callbacks}
		budget.Compaction = &contexty.CompactionPolicy{
			Descriptor:     identity("fixture-compaction"),
			TriggerPercent: compactionTrigger,
			TargetPercent:  compactionTarget,
		}
		opts = append(
			opts,
			contexty.WithRollingSummary(
				contexty.RollingSummaryPolicy{Descriptor: identity("fixture-rolling"), RecentMessages: 1},
			),
			contexty.WithSummarizerDescriptor(cfg.SummarizerIdentity),
		)
	}
	return contexty.NewBudgetPipeline(budget, reporter, opts...)
}

func compileRequest(ctx context.Context, request contexty.CompileRequest,
	pipeline *contexty.BudgetPipeline) (contexty.CompileResult, error) {
	engine := contexty.NewEngine(contexty.WithBudgetPipeline(pipeline),
		contexty.WithArtifactMaterialization(materialization()))
	return engine.CompileSnapshot(ctx, request)
}

func compileRolling(ctx context.Context, request contexty.CompileRequest,
	pipeline *contexty.BudgetPipeline) (contexty.CompileResult, error) {
	var working []contexty.Message
	var result contexty.CompileResult
	steps, err := chronologicalSteps(request.History)
	if err != nil {
		return contexty.CompileResult{}, err
	}
	var decisions []contexty.CompileBudgetDecision
	for _, step := range steps {
		working = append(working, cloneMessages(step)...)
		request.History = working
		result, err = compileRequest(ctx, request, pipeline)
		if err != nil {
			return contexty.CompileResult{}, err
		}
		decisions = append(decisions, result.BudgetDecisions...)
		working = cloneMessages(result.Payload.History)
	}
	result.BudgetDecisions = decisions
	return result, nil
}

func chronologicalSteps(messages []contexty.Message) ([][]contexty.Message, error) {
	rounds, err := contexty.InspectToolRoundStates(messages, nil)
	if err != nil {
		return nil, err
	}
	ends := make(map[int]int, len(rounds))
	for _, round := range rounds {
		ends[round.Start] = round.End
	}
	var steps [][]contexty.Message
	for i := 0; i < len(messages); i++ {
		end := i
		if roundEnd, ok := ends[i]; ok {
			end = roundEnd
			// A completed result is compiled with its next ordinary host step.
			if end+1 < len(messages) && !messages[end+1].HasToolCalls() && messages[end+1].Role != contexty.RoleTool {
				end++
			}
		}
		steps = append(steps, cloneMessages(messages[i:end+1]))
		i = end
	}
	return steps, nil
}

func payloadMessages(payload contexty.AbstractPayload) []contexty.Message {
	var messages []contexty.Message
	for _, segment := range [][]contexty.Message{payload.System, payload.History, payload.Tools, payload.Memory} {
		messages = append(messages, cloneMessages(segment)...)
	}
	return messages
}

func cloneMessages(messages []contexty.Message) []contexty.Message {
	out := make([]contexty.Message, len(messages))
	for i, message := range messages {
		out[i] = message.Clone()
	}
	return out
}

func materialization() contexty.ArtifactMaterializationPolicy {
	return contexty.ArtifactMaterializationPolicy{
		Identity: identity("fixture-artifact-user-data"),
		Materialize: func(_ context.Context, artifact contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
			parts, err := contexty.ArtifactContentParts(artifact)
			return contexty.ArtifactRepresentation{Role: contexty.RoleUser, Parts: parts}, err
		},
	}
}

func selectedEvidence(messages, originals []contexty.Message) ([]SelectedContent, []contexty.ContentRef, error) {
	selected := make([]SelectedContent, 0, len(messages))
	refs := make(map[contexty.ContentRef]bool, len(messages))
	for _, message := range messages {
		ref, err := contexty.MessageContentRef(message, contexty.DefaultJSONSerializer())
		if err != nil {
			return nil, nil, err
		}
		refs[ref] = true
		selected = append(selected, SelectedContent{Reference: ref, Message: message.Clone()})
	}
	var excluded []contexty.ContentRef
	for _, original := range originals {
		ref, err := contexty.MessageContentRef(original, contexty.DefaultJSONSerializer())
		if err != nil {
			return nil, nil, err
		}
		if !refs[ref] {
			excluded = append(excluded, ref)
		}
	}
	return selected, excluded, nil
}

func strategyPolicies(strategy string, cfg Config) []contexty.Descriptor {
	policies := []contexty.Descriptor{identity("fixture-artifact-user-data"), identity("fixture-drop-head")}
	if strategy == rollingStrategy {
		policies = append(policies, identity("fixture-compaction"), identity("fixture-rolling"), cfg.SummarizerIdentity)
	}
	if strategy == offloadStrategy {
		policies = append(
			policies,
			identity("fixture-explicit-offload"),
			identity("fixture-blob-preview"),
			identity("fixture-blob-authorize"),
		)
	}
	if strategy == retrievalStrategy {
		policies = append(
			policies,
			identity("fixture-source-selection"),
			identity("fixture-archive-reader"),
			identity("fixture-source-chunk"),
			identity("fixture-resource-authorize"),
		)
	}
	return policies
}

func strategyParameters(strategy string) map[string]int {
	parameters := map[string]int{"recent_messages": 0}
	if strategy == rollingStrategy {
		parameters["recent_messages"] = 1
		parameters["trigger_percent"] = compactionTrigger
		parameters["target_percent"] = compactionTarget
	}
	if strategy == offloadStrategy {
		parameters["max_inline_bytes"] = inlineThreshold
		parameters["max_blob_bytes"] = maxFixtureBody
		parameters["max_preview_bytes"] = previewBound
	}
	if strategy == retrievalStrategy {
		parameters["max_body_bytes"] = maxFixtureBody
	}
	return parameters
}
