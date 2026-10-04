package evaluation

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type liveTestModel struct {
	calls  int
	inputs [][]contexty.Message
}

func (m *liveTestModel) Generate(_ context.Context, messages []contexty.Message) (ModelResult, error) {
	m.calls++
	m.inputs = append(m.inputs, cloneMessages(messages))
	return ModelResult{
		Answer: messageText(messages[0]),
		Usage:  notMeasured("provider tokens"),
		Cost:   notMeasured("money"),
	}, nil
}

type liveTestEvaluator struct{ calls int }

func (e *liveTestEvaluator) Evaluate(_ context.Context, _ Fixture, _ ModelResult) (Measurement, error) {
	e.calls++
	value := float64(0)
	return Measurement{Status: "measured", Value: &value, Unit: "score"}, nil
}

func liveTestAdapters(model *liveTestModel, evaluator *liveTestEvaluator, summaries *Callbacks) LiveAdapters {
	return LiveAdapters{
		Model:         model,
		ModelIdentity: identity("fake-host-model"),
		Summarizer: countedSummarizer{
			inner:     nil,
			callbacks: summaries,
		},
		SummarizerIdentity: identity("fake-host-summarizer"),
		Evaluator:          evaluator,
		EvaluatorIdentity:  identity("fake-host-evaluator"),
	}
}

func TestRunLiveRequiresExplicitPinnedAdaptersBeforeGenerate(t *testing.T) {
	for _, scenario := range []string{"opt-in", "missing-model", "missing-summary", "missing-evaluator", "model-identity",
		"summary-identity", "evaluator-identity", "zero-runs", "negative-runs", "model-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: every rejected configuration starts from complete explicit host ports.
			model := &liveTestModel{calls: 0, inputs: nil}
			evaluator := &liveTestEvaluator{calls: 0}
			counts := &Callbacks{Summaries: 0, ResourceReads: 0, BlobWrites: 0}
			adapters := liveTestAdapters(model, evaluator, counts)
			cfg := OfflineConfig()
			cfg.Profile.Model = adapters.ModelIdentity
			runs, optIn := 1, true
			switch scenario {
			case "opt-in":
				optIn = false
			case "missing-model":
				adapters.Model = nil
			case "missing-summary":
				adapters.Summarizer = nil
			case "missing-evaluator":
				adapters.Evaluator = nil
			case "model-identity":
				adapters.ModelIdentity.Revision = ""
			case "summary-identity":
				adapters.SummarizerIdentity.Revision = ""
			case "evaluator-identity":
				adapters.EvaluatorIdentity.Revision = ""
			case "zero-runs":
				runs = 0
			case "negative-runs":
				runs = -1
			case "model-mismatch":
				cfg.Profile.Model = identity("another-model")
			}

			// Act.
			report, err := RunLive(context.Background(), cfg, adapters, runs, optIn)

			// Assert: no provider, summarizer or evaluator callback escaped validation.
			require.Error(t, err)
			require.Empty(t, report.Rows)
			require.Zero(t, model.calls)
			require.Zero(t, evaluator.calls)
			require.Zero(t, counts.Summaries)
		})
	}
}

func TestRunLiveUsesAllStrategiesAndExplicitHostMeasurements(t *testing.T) {
	// Arrange: a real repeated-compaction fixture exercises the real strategy runner.
	model := &liveTestModel{calls: 0, inputs: nil}
	evaluator := &liveTestEvaluator{calls: 0}
	counts := &Callbacks{Summaries: 0, ResourceReads: 0, BlobWrites: 0}
	adapters := liveTestAdapters(model, evaluator, counts)
	cfg := OfflineConfig()
	cfg.Fixtures = []Fixture{repeatedCompaction()}
	cfg.Profile.Model = adapters.ModelIdentity

	// Act: explicit live mode invokes fake typed ports, never a real provider.
	report, err := RunLive(context.Background(), cfg, adapters, 2, true)

	// Assert: one final model/evaluator call per strategy per repetition; summaries are separate.
	require.NoError(t, err)
	require.Equal(t, "live-host", report.Mode)
	require.Equal(t, 2, report.Runs)
	require.Equal(t, adapters.ModelIdentity, report.Model)
	require.Equal(t, adapters.SummarizerIdentity, report.Summarizer)
	require.Equal(t, adapters.EvaluatorIdentity, report.Evaluator)
	require.Len(t, report.Rows, 8)
	require.Equal(t, 8, model.calls)
	require.Equal(t, 8, evaluator.calls)
	question := cfg.Fixtures[0].Messages[len(cfg.Fixtures[0].Messages)-1]
	for _, issued := range model.inputs {
		require.Equal(
			t,
			question,
			findMessage(issued, question.ID),
			"every strategy must issue the exact current question",
		)
	}
	require.Greater(t, counts.Summaries, 2)
	strategies := make(map[string]int)
	for _, row := range report.Rows {
		strategies[row.Strategy.ID]++
		require.Equal(t, cfg.Fixtures[0].Identity, row.Fixture)
		require.Equal(t, "measured", row.ProviderQuality.Status)
		require.NotNil(t, row.ProviderQuality.Value)
		require.Zero(t, *row.ProviderQuality.Value, "a zero score is an observation, not missing data")
		// Usage/cost belong only to the final Generate result. The runner must not
		// infer missing provider values from semantic estimates or summary calls.
		require.Equal(t, notMeasured("provider tokens"), row.ProviderUsage)
		require.Equal(t, notMeasured("money"), row.ProviderCost)
		if row.Strategy.ID == "rolling-summary" {
			require.Greater(t, row.Callbacks.Summaries, 1)
			foundFact := false
			for _, check := range row.Checks {
				if check.Kind == "fact:recovery.key" {
					foundFact = true
					require.True(t, check.Passed, "summary must retain the FACT and exact original source")
				}
			}
			require.True(t, foundFact, "runner must issue the expected fact/source check")
		}
	}
	require.Equal(
		t,
		map[string]int{"sliding-window": 2, "rolling-summary": 2, "offload": 2, "host-selected-retrieval": 2},
		strategies,
	)
}

type reusedMeasurementModel struct {
	calls int
	usage float64
	cost  float64
}

func (m *reusedMeasurementModel) Generate(_ context.Context, messages []contexty.Message) (ModelResult, error) {
	m.calls++
	m.usage, m.cost = float64(m.calls), float64(m.calls)/10
	// Provider adapters may reuse measurement storage or mutate their request copy.
	messages[0].Parts[0] = contexty.TextPart{Text: "adapter-owned mutation"}
	return ModelResult{Answer: "host answer", Usage: Measurement{Status: "measured", Value: &m.usage, Unit: "tokens"},
		Cost: Measurement{Status: "measured", Value: &m.cost, Unit: "USD"}}, nil
}

type mutatingLiveEvaluator struct {
	calls   int
	quality float64
}

func (e *mutatingLiveEvaluator) Evaluate(_ context.Context, fixture Fixture, answer ModelResult) (Measurement, error) {
	e.calls++
	e.quality = float64(e.calls)
	fixture.Messages[0].Parts[0] = contexty.TextPart{Text: "fixture mutation"}
	fixture.Messages[0].SourceRefs[0].ID = "mutated-source"
	fixture.RequiredIDs[0] = "mutated-required"
	fixture.QuerySourceIDs[0] = "mutated-query"
	fixture.ExpectedFacts["recovery.key"] = "mutated-fact"
	fixture.ExpectedSources["recovery.key"][0].ID = "mutated-evidence"
	fixture.FactVersions["recovery.key"] = "mutated-version"
	*answer.Usage.Value, *answer.Cost.Value = 999, 999
	return Measurement{Status: "measured", Value: &e.quality, Unit: "score"}, nil
}

func TestRunLiveOwnsFixtureInputsAndAcceptedMeasurements(t *testing.T) {
	// Arrange: adapters reuse result pointers and actively mutate evaluator inputs.
	model := &reusedMeasurementModel{calls: 0, usage: 0, cost: 0}
	evaluator := &mutatingLiveEvaluator{calls: 0, quality: 0}
	counts := &Callbacks{Summaries: 0, ResourceReads: 0, BlobWrites: 0}
	adapters := LiveAdapters{Model: model, ModelIdentity: identity("reused-host-model"),
		Summarizer: countedSummarizer{inner: nil, callbacks: counts}, SummarizerIdentity: identity("fake-host-summary"),
		Evaluator: evaluator, EvaluatorIdentity: identity("mutating-host-evaluator")}
	cfg := OfflineConfig()
	fixture, expected := repeatedCompaction(), repeatedCompaction()
	fixture.RequiredIDs, expected.RequiredIDs = []string{"recovery-v1"}, []string{"recovery-v1"}
	cfg.Fixtures = []Fixture{fixture}
	cfg.Profile.Model = adapters.ModelIdentity

	// Act.
	report, err := RunLive(context.Background(), cfg, adapters, 2, true)
	model.usage, model.cost, evaluator.quality = 777, 777, 777

	// Assert: each row owns its observation, independently of later calls and adapters.
	require.NoError(t, err)
	require.Len(t, report.Rows, 8)
	require.Equal(t, []Fixture{expected}, cfg.Fixtures)
	for index, row := range report.Rows {
		require.InDelta(t, float64(index+1), *row.ProviderUsage.Value, 0)
		require.InDelta(t, float64(index+1)/10, *row.ProviderCost.Value, 0)
		require.InDelta(t, float64(index+1), *row.ProviderQuality.Value, 0)
		for _, selected := range row.Selected {
			require.NotContains(t, selected.Message.TextContent(), "adapter-owned mutation")
		}
	}
}

func TestValidateMeasurementRejectsInvalidOrInventedObservations(t *testing.T) {
	negative, nan, positiveInfinity, negativeInfinity, zero := -1.0, math.NaN(), math.Inf(1), math.Inf(-1), 0.0
	for _, scenario := range []struct {
		name        string
		measurement Measurement
		valid       bool
	}{
		{name: "missing", measurement: notMeasured("money"), valid: true},
		{name: "zero-observed", measurement: Measurement{Status: "measured", Value: &zero, Unit: "score"}, valid: true},
		{name: "unknown-status", measurement: Measurement{Status: "estimated", Value: &zero, Unit: "money"}, valid: false},
		{name: "unobserved-value", measurement: Measurement{Status: "not measured", Value: &zero, Unit: "money"}, valid: false},
		{name: "missing-measured-value", measurement: Measurement{Status: "measured", Value: nil, Unit: "money"}, valid: false},
		{name: "missing-unit", measurement: Measurement{Status: "measured", Value: &zero, Unit: ""}, valid: false},
		{name: "missing-unobserved-unit", measurement: notMeasured(""), valid: false},
		{name: "negative", measurement: Measurement{Status: "measured", Value: &negative, Unit: "money"}, valid: false},
		{name: "nan", measurement: Measurement{Status: "measured", Value: &nan, Unit: "money"}, valid: false},
		{name: "positive-infinity", measurement: Measurement{Status: "measured", Value: &positiveInfinity, Unit: "money"}, valid: false},
		{name: "negative-infinity", measurement: Measurement{Status: "measured", Value: &negativeInfinity, Unit: "money"}, valid: false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange.
			measurement := scenario.measurement
			// Act.
			err := validateMeasurement(measurement)
			// Assert.
			if scenario.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
