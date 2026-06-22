package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestEstimate_Codec(t *testing.T) {
	// Arrange: a valid pinned report contains refs, not source payloads.
	report := fixtureReportWithWire(t)
	// Act.
	wire, err := contexty.EncodeEstimateReport(report)
	require.NoError(t, err)
	restored, err := contexty.DecodeEstimateReport(wire)
	// Assert: complete evidence survives strict codec round-trip.
	require.NoError(t, err)
	require.Equal(t, report, restored)
	require.NotContains(t, string(wire), "secret")
	for _, mutate := range []func(*contexty.EstimateReport){
		func(r *contexty.EstimateReport) { r.Total++ },
		func(r *contexty.EstimateReport) { r.Segments[0].PerMessage[0] = -1 },
		func(r *contexty.EstimateReport) { r.Segments = append(r.Segments, r.Segments[0]) },
		func(r *contexty.EstimateReport) { r.Segments[0].Coverage = nil },
		func(r *contexty.EstimateReport) { r.Quality = contexty.EstimateCounted },
		func(r *contexty.EstimateReport) { r.Profile.Model.Revision = "changed" },
		func(r *contexty.EstimateReport) { r.EffectiveLimit++ },
		func(r *contexty.EstimateReport) { r.RequestDigest = "wrong" },
	} {
		broken, cloneErr := report.Clone()
		require.NoError(t, cloneErr)
		mutate(&broken)
		invalidWire, marshalErr := json.Marshal(broken)
		require.NoError(t, marshalErr)
		decoded, decodeErr := contexty.DecodeEstimateReport(invalidWire)
		require.Error(t, decodeErr)
		require.Zero(t, decoded)
	}
	for _, invalid := range [][]byte{append(wire, []byte(` {}`)...), []byte(`{"unknown":true}`)} {
		decoded, decodeErr := contexty.DecodeEstimateReport(invalid)
		require.ErrorIs(t, decodeErr, contexty.ErrInvalidEstimateReport)
		require.Zero(t, decoded)
	}
	// Arrange / Act / Assert: structural coverage holes fail even with a recomputed checksum.
	missing, err := report.Clone()
	require.NoError(t, err)
	missing.Segments[0].Coverage = nil
	fixtureResignEstimate(t, &missing)
	require.ErrorIs(t, missing.Validate(), contexty.ErrInvalidEstimateReport)
}

func TestEstimate_Identity(t *testing.T) {
	// Arrange: a provider count is approximate unless its counter guarantees accuracy.
	report := fixtureReportWithWire(t)
	history, err := contexty.NewEstimateHistory(report)
	require.NoError(t, err)
	evidence := contexty.WireCountEvidence{
		ID:            "wire-count",
		Counter:       contexty.Descriptor{ID: "counter", Revision: "pinned"},
		RequestDigest: report.RequestDigest,
		ProfileDigest: report.ProfileDigest,
		WireRef:       *report.WireRef,
		Tokens:        14,
		Quality:       contexty.EstimateEstimated,
	}
	// Act.
	withWire, err := history.WithWireCount(evidence)
	// Assert: compare full wire cost against 15, not the semantic capacity 12.
	require.NoError(t, err)
	require.Empty(t, history.Wire)
	require.Equal(t, 15, withWire.Wire[0].InputLimit)
	require.Empty(t, withWire.Wire[0].OverflowReason)
	require.Equal(t, contexty.EstimateEstimated, withWire.Wire[0].Evidence.Quality)
	require.Equal(t, report, withWire.Estimate)
	usage := contexty.UsageEvidence{ID: "usage", Source: evidence.Counter,
		RequestDigest: report.RequestDigest, ProfileDigest: report.ProfileDigest, WireRef: *report.WireRef,
		InputTokens: 16, OutputTokens: 2}
	withUsage, err := withWire.WithUsage(usage)
	require.NoError(t, err)
	require.Empty(t, withWire.Usage)
	require.Equal(t, report, withUsage.Estimate)
	require.Equal(t, 16, withUsage.Usage[0].InputTokens)
	require.NotEqual(t, withWire.Digest, withUsage.Digest)
	wire, err := contexty.EncodeEstimateHistory(withUsage)
	require.NoError(t, err)
	restored, err := contexty.DecodeEstimateHistory(wire)
	require.NoError(t, err)
	require.Equal(t, withUsage, restored)
	restored.Usage[0].InputTokens++
	require.ErrorIs(t, restored.Validate(), contexty.ErrInvalidEstimateReport)
	restored.Estimate.Segments[0].PerMessage[0] = 999
	require.Equal(t, 6, withUsage.Estimate.Total)
	_, err = withUsage.WithUsage(usage)
	require.ErrorIs(t, err, contexty.ErrDuplicateEstimateObservation)
	_, err = withUsage.WithWireCount(evidence)
	require.ErrorIs(t, err, contexty.ErrDuplicateEstimateObservation)
	// Arrange / Act / Assert: stale request/profile/wire, bad quality and negative cost fail.
	for _, mutate := range []func(*contexty.WireCountEvidence){
		func(e *contexty.WireCountEvidence) { e.RequestDigest = "changed" },
		func(e *contexty.WireCountEvidence) { e.ProfileDigest = "changed" },
		func(e *contexty.WireCountEvidence) { e.WireRef.ID = "changed" },
	} {
		bad := evidence
		mutate(&bad)
		result, appendErr := history.WithWireCount(bad)
		require.ErrorIs(t, appendErr, contexty.ErrStaleEstimate)
		require.Zero(t, result)
	}
	evidence.Quality = contexty.EstimateCounted
	_, err = history.WithWireCount(evidence)
	require.ErrorIs(t, err, contexty.ErrInvalidEstimateReport)
	evidence.Guaranteed, evidence.Tokens = true, 16
	guaranteed, err := history.WithWireCount(evidence)
	require.NoError(t, err)
	require.Equal(t, contexty.EstimateCounted, guaranteed.Wire[0].Evidence.Quality)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, guaranteed.Wire[0].OverflowReason)
	usage.InputTokens = -1
	_, err = history.WithUsage(usage)
	require.ErrorIs(t, err, contexty.ErrInvalidEstimateReport)
	// Arrange / Act / Assert: even matching semantic identity needs an explicit wire ref.
	reporter, err := contexty.NewEstimateReporter(contexty.CharTokenEstimator{}, fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	withoutWire, err := reporter.Report(
		context.Background(),
		contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(10)},
	)
	require.NoError(t, err)
	emptyHistory, err := contexty.NewEstimateHistory(withoutWire)
	require.NoError(t, err)
	evidence.RequestDigest, evidence.ProfileDigest = withoutWire.RequestDigest, withoutWire.ProfileDigest
	_, err = emptyHistory.WithWireCount(evidence)
	require.ErrorIs(t, err, contexty.ErrStaleEstimate)
}

func TestEstimate_FallbackEvidence(t *testing.T) {
	// Arrange: one unsupported part incurs the pinned eleven-token fallback.
	message := contexty.TextMessage(contexty.RoleUser, "")
	message.ID = "image"
	message.Parts = []contexty.ContentPart{contexty.ImagePart{URL: "private-url"}}
	profile := fixtureEstimateProfile()
	profile.Fallback = &contexty.EstimateFallback{
		Policy: contexty.Descriptor{ID: "fallback", Revision: "pinned"},
		Tokens: 11,
	}
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		profile,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	report, err := reporter.Report(
		context.Background(),
		contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(20),
			Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}},
	)
	require.NoError(t, err)
	// Act / Assert: a forged consistent total cannot lower the configured fallback cost.
	broken, err := report.Clone()
	require.NoError(t, err)
	broken.Total, broken.Segments[0].Tokens, broken.Segments[0].PerMessage[0] = 1, 1, 1
	broken.Segments[0].Coverage[0].FallbackTokens = 1
	fixtureResignEstimate(t, &broken)
	require.ErrorIs(t, broken.Validate(), contexty.ErrInconsistentEstimate)
	// Arrange / Act / Assert: unknown cost cannot become silent zero after round-trip.
	broken.Total, broken.Segments[0].Tokens, broken.Segments[0].PerMessage[0] = 0, 0, 0
	broken.Segments[0].Coverage[0].FallbackTokens = 0
	fixtureResignEstimate(t, &broken)
	wire, err := json.Marshal(broken)
	require.NoError(t, err)
	decoded, err := contexty.DecodeEstimateReport(wire)
	require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
	require.Zero(t, decoded)
}
