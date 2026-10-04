package evaluation

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/skosovsky/contexty"
)

// RunLive performs explicit provider calls through injected host adapters.
// It is never invoked by the offline command or default validation suite.
func RunLive(ctx context.Context, cfg Config, adapters LiveAdapters, runs int, explicitOptIn bool) (Report, error) {
	if !explicitOptIn || runs <= 0 || adapters.Model == nil || adapters.Summarizer == nil ||
		adapters.Evaluator == nil ||
		adapters.ModelIdentity.Validate() != nil ||
		adapters.SummarizerIdentity.Validate() != nil ||
		adapters.EvaluatorIdentity.Validate() != nil {
		return Report{}, errors.New(
			"evaluation: live run requires explicit opt-in, pinned adapters and positive run count",
		)
	}
	if cfg.Profile.Model != adapters.ModelIdentity {
		return Report{}, errors.New("evaluation: estimator model must match live model identity")
	}
	cfg.Summarizer, cfg.SummarizerIdentity, cfg.EvaluatorIdentity = adapters.Summarizer, adapters.SummarizerIdentity, adapters.EvaluatorIdentity
	cfg.Fixtures = cloneFixtures(cfg.Fixtures)
	cfg.Profile = cloneProfile(cfg.Profile)
	var report Report
	for run := range runs {
		current, err := Run(ctx, cfg)
		if err != nil {
			return Report{}, err
		}
		current, err = evaluateLiveRows(ctx, cfg, adapters, current)
		if err != nil {
			return Report{}, err
		}
		if run == 0 {
			report = current
		} else {
			report.Rows = append(report.Rows, current.Rows...)
			report.Behaviors = append(report.Behaviors, current.Behaviors...)
		}
	}
	report.Mode, report.Runs, report.Model = "live-host", runs, adapters.ModelIdentity
	report.Limitations = []string{
		"Quality and usage are host-adapter observations for this pinned model, evaluator and dataset only.",
		"Usage and cost cover final answer generation only; summarizer and evaluator provider usage/cost are not measured.",
		"No pricing is inferred from semantic estimates. Missing provider measurements remain not measured.",
		"Host elapsed timing is diagnostic, not a benchmark or provider latency measurement.",
	}
	return report, nil
}

func validateMeasurement(measurement Measurement) error {
	if measurement.Status == "not measured" && measurement.Value == nil && measurement.Unit != "" {
		return nil
	}
	if measurement.Status != "measured" || measurement.Value == nil || measurement.Unit == "" ||
		math.IsNaN(*measurement.Value) || math.IsInf(*measurement.Value, 0) || *measurement.Value < 0 {
		return errors.New("evaluation: invalid provider measurement")
	}
	return nil
}

func liveFixture(fixtures []Fixture, id string) (Fixture, bool) {
	for _, fixture := range fixtures {
		if fixture.Identity.ID == id {
			return fixture, true
		}
	}
	return Fixture{}, false
}

func evaluateLiveRows(ctx context.Context, cfg Config, adapters LiveAdapters, report Report) (Report, error) {
	for i := range report.Rows {
		row := &report.Rows[i]
		fixture, ok := liveFixture(cfg.Fixtures, row.Fixture.ID)
		if !ok {
			return Report{}, errors.New("evaluation: missing live fixture")
		}
		messages := make([]contexty.Message, len(row.Selected))
		for j, selected := range row.Selected {
			messages[j] = selected.Message.Clone()
		}
		answer, modelErr := adapters.Model.Generate(ctx, messages)
		if canceled := ctx.Err(); canceled != nil {
			return Report{}, canceled
		}
		if modelErr != nil {
			return Report{}, fmt.Errorf("live model: %w", modelErr)
		}
		answer = cloneModelResult(answer)
		quality, evaluatorErr := adapters.Evaluator.Evaluate(ctx, cloneFixture(fixture), cloneModelResult(answer))
		if canceled := ctx.Err(); canceled != nil {
			return Report{}, canceled
		}
		if evaluatorErr != nil {
			return Report{}, fmt.Errorf("live evaluator: %w", evaluatorErr)
		}
		for _, measurement := range []Measurement{quality, answer.Usage, answer.Cost} {
			if err := validateMeasurement(measurement); err != nil {
				return Report{}, err
			}
		}
		row.ProviderQuality, row.ProviderUsage, row.ProviderCost = cloneMeasurement(
			quality,
		), cloneMeasurement(
			answer.Usage,
		), cloneMeasurement(
			answer.Cost,
		)
	}
	return report, nil
}
