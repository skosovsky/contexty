// Package evaluation demonstrates host-owned context strategy measurement.
package evaluation

import (
	"context"
	"time"

	"github.com/skosovsky/contexty"
)

const (
	offlineBudget     = 640
	slidingStrategy   = "sliding-window"
	rollingStrategy   = "rolling-summary"
	offloadStrategy   = "offload"
	retrievalStrategy = "host-selected-retrieval"
	compactionTrigger = 85
	compactionTarget  = 65
	maxFixtureBody    = 1 << 20
	inlineThreshold   = 256
	previewBound      = 128
)

// BehaviorEvidence records one independent mechanical preflight per fixture.
type BehaviorEvidence struct {
	Fixture    contexty.Descriptor `json:"fixture"`
	Executions int                 `json:"executions"`
	Checks     []Check             `json:"checks"`
}

// Check is mechanical fixture evidence, never a provider quality assessment.
type Check struct {
	Kind   string `json:"kind"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// Callbacks counts strategy callback invocations. Independent behavior preflights
// are recorded separately in Report.Behaviors.
type Callbacks struct {
	Summaries     int `json:"summaries"`
	ResourceReads int `json:"resource_reads"`
	BlobWrites    int `json:"blob_writes"`
}

// SelectedContent pins an issued semantic message and its host source attribution.
type SelectedContent struct {
	Reference contexty.ContentRef `json:"reference"`
	Message   contexty.Message    `json:"message"`
}

// Measurement distinguishes observed values from absent provider measurements.
type Measurement struct {
	Status string   `json:"status"`
	Value  *float64 `json:"value,omitempty"`
	Unit   string   `json:"unit"`
}

// Row reports one fixture/strategy result under a shared profile.
type Row struct {
	Fixture          contexty.Descriptor              `json:"fixture"`
	Strategy         contexty.Descriptor              `json:"strategy"`
	Budget           int                              `json:"budget"`
	Selected         []SelectedContent                `json:"selected"`
	Excluded         []contexty.ContentRef            `json:"excluded"`
	Estimate         int                              `json:"semantic_estimate"`
	EstimateQuality  contexty.EstimateQuality         `json:"estimate_quality"`
	EstimateReport   contexty.EstimateReport          `json:"estimate_report"`
	PolicyParameters map[string]int                   `json:"policy_parameters"`
	Policies         []contexty.Descriptor            `json:"policies"`
	BudgetDecisions  []contexty.CompileBudgetDecision `json:"budget_decisions"`
	Callbacks        Callbacks                        `json:"callbacks"`
	Checks           []Check                          `json:"checks"`
	Duration         time.Duration                    `json:"host_wall_nanoseconds"`
	ProviderQuality  Measurement                      `json:"provider_quality"`
	ProviderUsage    Measurement                      `json:"provider_usage"`
	ProviderCost     Measurement                      `json:"provider_cost"`
}

// Report contains reproducible structural results and separate diagnostic timing.
type Report struct {
	Schema      contexty.Descriptor      `json:"schema"`
	Model       contexty.Descriptor      `json:"model"`
	Summarizer  contexty.Descriptor      `json:"summarizer"`
	Evaluator   contexty.Descriptor      `json:"evaluator"`
	Estimator   contexty.EstimateProfile `json:"estimator"`
	Runs        int                      `json:"runs"`
	Mode        string                   `json:"mode"`
	Limitations []string                 `json:"limitations"`
	Behaviors   []BehaviorEvidence       `json:"behaviors"`
	Rows        []Row                    `json:"rows"`
}

// Config pins host dataset, budget and estimator for every strategy.
type Config struct {
	Fixtures           []Fixture
	Budget             int
	Estimator          contexty.TokenEstimator
	Profile            contexty.EstimateProfile
	Summarizer         contexty.Summarizer
	SummarizerIdentity contexty.Descriptor
	EvaluatorIdentity  contexty.Descriptor
}

// OfflineConfig supplies deterministic fixture ports without provider access.
func OfflineConfig() Config {
	return Config{
		Fixtures:           Corpus(),
		Budget:             offlineBudget,
		Estimator:          contexty.CharTokenEstimator{},
		Profile:            OfflineProfile(),
		Summarizer:         nil,
		SummarizerIdentity: identity("fixture-marker-summary"),
		EvaluatorIdentity:  identity("fixture-facts-sources"),
	}
}

// OfflineProfile honestly labels the fixture character estimator as estimated.
func OfflineProfile() contexty.EstimateProfile {
	return contexty.EstimateProfile{ //nolint:exhaustruct_v5 // No fallback or extension costs are implied.
		Model: identity(
			"offline-no-model",
		),
		Estimator: identity("rune-counter"),
		Method:    identity("character-estimate"),
		Encoding:  identity("contexty-typed-json"),
		Capabilities: map[contexty.EstimateKind]contexty.EstimateQuality{
			contexty.EstimateText: contexty.EstimateEstimated, contexty.EstimateToolCall: contexty.EstimateEstimated,
			contexty.EstimateToolResult: contexty.EstimateEstimated, contexty.EstimateImage: contexty.EstimateUnknown,
			contexty.EstimateMedia: contexty.EstimateUnknown, contexty.EstimateExtension: contexty.EstimateUnknown},
	}
}

func identity(id string) contexty.Descriptor {
	return contexty.Descriptor{ID: id, Revision: "fixture-v1"}
}

func notMeasured(unit string) Measurement {
	return Measurement{Status: "not measured", Value: nil, Unit: unit}
}

// Structural returns a comparison snapshot without wall-clock values.
// Nested evidence is shared and must be treated as immutable.
func (r Report) Structural() Report {
	r.Rows = append([]Row(nil), r.Rows...)
	for i := range r.Rows {
		r.Rows[i].Duration = 0
	}
	return r
}

// Model executes a host-owned provider request only during an explicit live run.
type Model interface {
	Generate(context.Context, []contexty.Message) (ModelResult, error)
}

// ModelResult carries an answer and measurements actually returned by a provider.
type ModelResult struct {
	Answer string
	Usage  Measurement
	Cost   Measurement
}

// Evaluator grades an actual provider answer against a fixture task.
type Evaluator interface {
	Evaluate(context.Context, Fixture, ModelResult) (Measurement, error)
}

// LiveAdapters are explicit host ports; core has no credentials, SDK or registry.
type LiveAdapters struct {
	Model              Model
	ModelIdentity      contexty.Descriptor
	Summarizer         contexty.Summarizer
	SummarizerIdentity contexty.Descriptor
	Evaluator          Evaluator
	EvaluatorIdentity  contexty.Descriptor
}
