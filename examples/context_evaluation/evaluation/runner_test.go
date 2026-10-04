package evaluation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestRunReproducibleStructuralEvidence(t *testing.T) {
	// Arrange: identical owned corpus and pinned profile, with no provider adapters.
	cfg := OfflineConfig()
	before := Corpus()
	// Act: timing may differ; every structural value must be identical.
	first, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	second, err := Run(t.Context(), OfflineConfig())
	require.NoError(t, err)
	// Assert: all strategy rows share budget/profile and exact fixture evidence.
	require.Equal(t, first.Structural(), second.Structural())
	require.Equal(t, before, cfg.Fixtures, "callbacks must not mutate archive inputs")
	require.Len(t, first.Rows, len(cfg.Fixtures)*4)
	for _, row := range first.Rows {
		require.Equal(t, cfg.Budget, row.Budget)
		require.LessOrEqual(t, row.Estimate, cfg.Budget)
		require.Equal(t, row.Estimate, row.EstimateReport.Total)
		require.Equal(t, row.EstimateQuality, row.EstimateReport.Quality)
		require.Equal(t, cfg.Profile, row.EstimateReport.Profile)
		require.Equal(t, "not measured", row.ProviderQuality.Status)
		require.Equal(t, "not measured", row.ProviderUsage.Status)
		require.Equal(t, "not measured", row.ProviderCost.Status)
		require.NotEmpty(t, row.Policies)
		for _, selected := range row.Selected {
			actual, refErr := contexty.MessageContentRef(selected.Message, contexty.DefaultJSONSerializer())
			require.NoError(t, refErr)
			require.Equal(t, actual, selected.Reference)
		}
	}
}

func TestRunShowsStrategyLossAndRecovery(t *testing.T) {
	// Arrange: independent expected facts belong to the original archive corpus.
	cfg := OfflineConfig()
	// Act.
	report, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	// Assert: negative baseline is visible, never silently scored as universal PASS.
	sliding := reportRow(t, report, "corpus.repeated-compaction", slidingStrategy)
	rolling := reportRow(t, report, "corpus.repeated-compaction", rollingStrategy)
	retrieval := reportRow(t, report, "corpus.repeated-compaction", retrievalStrategy)
	require.False(t, findCheck(t, sliding, "fact:recovery.key").Passed)
	require.True(t, findCheck(t, rolling, "fact:recovery.key").Passed)
	require.GreaterOrEqual(t, rolling.Callbacks.Summaries, 2)
	require.True(t, findCheck(t, retrieval, "fact:recovery.key").Passed)
	require.Equal(t, 1, retrieval.Callbacks.ResourceReads)
	offload := reportRow(t, report, "corpus.distracting-tools", offloadStrategy)
	require.Equal(t, 1, offload.Callbacks.BlobWrites)
	previewPresent := false
	for _, selected := range offload.Selected {
		if selected.Message.ID == "artifact:evidence-read-result" {
			previewPresent = true
		}
	}
	require.True(t, previewPresent, "stored evidence must have an actually issued preview")
	for _, row := range report.Rows {
		for _, check := range row.Checks {
			if row.Strategy.ID == rollingStrategy || row.Strategy.ID == retrievalStrategy ||
				check.Kind[:min(len(check.Kind), 5)] != "fact:" {
				require.True(t, check.Passed, "%s/%s: %s", row.Fixture.ID, row.Strategy.ID, check.Kind)
			}
		}
	}
}

func TestRunRejectsInvalidInputsAndCancellation(t *testing.T) {
	// Arrange.
	cfg := OfflineConfig()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	// Act / Assert: no partial report is accepted.
	report, err := Run(canceled, cfg)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, report)
	cfg.Fixtures = append(cfg.Fixtures, cfg.Fixtures[0])
	report, err = Run(t.Context(), cfg)
	require.Error(t, err)
	require.Zero(t, report)
	cfg = OfflineConfig()
	cfg.Budget = 1
	report, err = Run(t.Context(), cfg)
	require.Error(t, err)
	require.Zero(t, report)
}

func reportRow(t *testing.T, report Report, fixtureID, strategyID string) Row {
	t.Helper()
	for _, row := range report.Rows {
		if row.Fixture.ID == fixtureID && row.Strategy.ID == strategyID {
			return row
		}
	}
	t.Fatalf("missing fixture %s strategy %s", fixtureID, strategyID)
	return Row{}
}

func findCheck(t *testing.T, row Row, kind string) Check {
	t.Helper()
	for _, check := range row.Checks {
		if check.Kind == kind {
			return check
		}
	}
	t.Fatalf("missing check %s", kind)
	return Check{}
}

func BenchmarkOfflineStrategies(b *testing.B) {
	// Arrange: CPU benchmark is independent from provider measurements.
	cfg := OfflineConfig()
	b.ReportAllocs()
	b.ResetTimer()
	// Act.
	for b.Loop() {
		_, err := Run(b.Context(), cfg)
		if err != nil {
			b.Fatal(err)
		}
	}
}
