package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestArtifact_EstimateRecord(t *testing.T) {
	// Arrange: local admission differs from final output participation.
	compiled, calls := fixtureArtifactEstimateFixture(t)
	baseline := *calls
	// Act: serialize accepted record and replay without an engine/estimator.
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), restored, expected, contexty.DefaultJSONSerializer())
	// Assert: excluded budget cost remains recorded; inactive/unbounded has no local count.
	require.NoError(t, err)
	require.Equal(t, baseline, *calls)
	require.Len(t, compiled.ArtifactEstimates, 2)
	require.Len(t, compiled.Manifest.ArtifactBudgets, 2)
	require.Equal(t, compiled.ArtifactEstimates, compiled.Manifest.ArtifactEstimates)
	require.Equal(t, compiled.ArtifactEstimates, replayed.Manifest.ArtifactEstimates)
	require.Equal(t, 3, compiled.ArtifactEstimates[0].Tokens)
	require.Equal(t, 5, compiled.ArtifactEstimates[1].Tokens)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, compiled.ArtifactEstimates[1].Report.OverflowReason)
	require.Equal(t, 7, compiled.Estimates[0].Report.Total)
	require.Len(t, replayed.Artifacts, 2)
	compiled.ArtifactEstimates[0].Report.Segments[0].PerMessage[0] = 99
	require.Equal(t, 3, compiled.Manifest.ArtifactEstimates[0].Report.Segments[0].PerMessage[0])
	require.Equal(t, 3, replayed.Manifest.ArtifactEstimates[0].Report.Segments[0].PerMessage[0])
	expected.ArtifactBudgets[0].TokenLimit++
	require.Equal(t, 3, compiled.Manifest.ArtifactBudgets[0].TokenLimit)
	failed, err := contexty.Replay(context.Background(), restored, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, failed)
}

func TestArtifact_EstimateValidation(t *testing.T) {
	// Arrange: checks reject holes before relying only on a stale checksum.
	compiled, _ := fixtureArtifactEstimateFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*contexty.CompileManifest)
		want   error
	}{
		{name: "missing evidence", mutate: func(m *contexty.CompileManifest) { m.ArtifactEstimates = nil }, want: contexty.ErrMissingEstimateReport},
		{name: "missing report", mutate: func(m *contexty.CompileManifest) { m.ArtifactEstimates[0].Report = nil }, want: contexty.ErrMissingEstimateReport},
		{name: "duplicate evidence", mutate: func(m *contexty.CompileManifest) {
			m.ArtifactEstimates = append(m.ArtifactEstimates, m.ArtifactEstimates[0])
		}, want: contexty.ErrInvalidEstimateReport},
		{name: "wrong input", mutate: func(m *contexty.CompileManifest) { m.ArtifactEstimates[0].Input = m.ArtifactEstimates[1].Input }, want: contexty.ErrInvalidEstimateReport},
		{name: "wrong message", mutate: func(m *contexty.CompileManifest) { m.ArtifactEstimates[0].Message.ID = "unrelated" }, want: contexty.ErrInvalidEstimateReport},
		{name: "wrong limit", mutate: func(m *contexty.CompileManifest) { m.ArtifactEstimates[0].TokenLimit++ }, want: contexty.ErrInvalidEstimateReport},
		{name: "negative cost", mutate: func(m *contexty.CompileManifest) { m.ArtifactEstimates[0].Tokens = -1 }, want: contexty.ErrInvalidEstimateReport},
		{name: "contradictory admission", mutate: func(m *contexty.CompileManifest) { m.ArtifactEstimates[0].Tokens = 4 }, want: contexty.ErrInvalidEstimateReport},
		{name: "contradictory exclusion", mutate: func(m *contexty.CompileManifest) { m.ArtifactEstimates[1].Tokens = 1 }, want: contexty.ErrInvalidEstimateReport},
		{name: "unknown budget request", mutate: func(m *contexty.CompileManifest) { m.ArtifactBudgets[0].Input.ID = "unknown" }, want: contexty.ErrInvalidEstimateReport},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := compiled.Manifest.Clone()
			require.NoError(t, err)
			// Act.
			tc.mutate(&manifest)
			err = manifest.Validate()
			// Assert.
			require.ErrorIs(t, err, tc.want)
		})
	}
}
