package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestCompaction_Records(t *testing.T) {
	// Arrange: proposed summary stores only refs to raw private inputs.
	proposed := fixtureCompactionFixture(t)
	// Act: explicit host acceptance, codec round-trip, and pure replay.
	accepted, err := proposed.Accept("host-accept")
	require.NoError(t, err)
	wire, err := contexty.EncodeCompactionRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeCompactionRecord(wire)
	require.NoError(t, err)
	expected := contexty.ContentRef{ID: accepted.ID, Digest: accepted.Digest}
	message, err := contexty.ReplayCompaction(
		context.Background(),
		restored,
		expected,
		accepted.Profile,
		accepted.Covered,
		accepted.Budget,
		contexty.DefaultJSONSerializer(),
	)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, "safe", message.TextContent())
	require.Equal(t, "summary", message.ID)
	require.Equal(t, contexty.RecordProposed, proposed.State)
	require.Equal(t, contexty.RecordAccepted, accepted.State)
	require.Equal(t, proposed.ID, accepted.ID)
	require.NotEqual(t, proposed.Digest, accepted.Digest)
	require.Equal(t, accepted, restored)
	require.NotContains(t, string(wire), "private-source-secret")
	require.NotContains(t, string(wire), "other private source")
	superseded, err := accepted.Supersede("host-new-summary")
	require.NoError(t, err)
	require.Equal(t, contexty.RecordSuperseded, superseded.State)
	require.NotEqual(t, accepted.Digest, superseded.Digest)
	_, err = contexty.ReplayCompaction(
		context.Background(),
		superseded,
		contexty.ContentRef{
			ID:     superseded.ID,
			Digest: superseded.Digest,
		},
		superseded.Profile,
		superseded.Covered,
		superseded.Budget,
		contexty.DefaultJSONSerializer(),
	)
	require.ErrorIs(t, err, contexty.ErrUnsupportedReplay)
	accepted.Result.Wire[0] = 'x'
	require.NoError(t, proposed.Validate())
	require.NoError(t, restored.Validate())
	accepted.Estimate.Segments[0].PerMessage[0]++
	require.Equal(t, 4, proposed.Estimate.Segments[0].PerMessage[0])
}

func TestCompaction_PartialCannotAccept(t *testing.T) {
	// Arrange: host privacy can omit result bytes or cost evidence from a proposal.
	complete := fixtureCompactionFixture(t)
	for _, missingResult := range []bool{true, false} {
		t.Run(map[bool]string{true: "deleted content", false: "missing estimate"}[missingResult], func(t *testing.T) {
			result, estimate := complete.Result, complete.Estimate
			if missingResult {
				result = nil
			} else {
				estimate = nil
			}
			partial, err := contexty.NewCompactionRecord(complete.ID, complete.Profile, complete.Covered,
				complete.Output, complete.Lineage, complete.Budget, result, estimate)
			require.NoError(t, err)
			// Act.
			accepted, err := partial.Accept("host-accept")
			// Assert.
			require.Zero(t, accepted)
			require.ErrorIs(
				t,
				err,
				map[bool]error{true: contexty.ErrMissingReplayDependency, false: contexty.ErrMissingEstimateReport}[missingResult],
			)
			require.NoError(t, partial.Validate())
		})
	}
}

func TestCompaction_Validation(t *testing.T) {
	// Arrange.
	baseline := fixtureCompactionFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*contexty.CompactionRecord)
		want   error
	}{
		{name: "coverage hole", mutate: func(r *contexty.CompactionRecord) { r.Covered = r.Covered[:1] }, want: contexty.ErrInvalidCoverage},
		{name: "duplicate covered ID", mutate: func(r *contexty.CompactionRecord) { r.Covered = append(r.Covered, r.Covered[0]) }, want: contexty.ErrInvalidCoverage},
		{name: "summary ID reused", mutate: func(r *contexty.CompactionRecord) { r.Output.ID = r.Covered[0].ID }, want: contexty.ErrInvalidCoverage},
		{name: "missing summary edge", mutate: func(r *contexty.CompactionRecord) { r.Lineage.Records = nil }, want: contexty.ErrInvalidCoverage},
		{name: "wrong summarizer", mutate: func(r *contexty.CompactionRecord) { r.Profile.Summarizer.Revision = "changed" }, want: contexty.ErrInvalidCoverage},
		{name: "codec changed", mutate: func(r *contexty.CompactionRecord) { r.Profile.Encoding.Revision = "changed" }, want: contexty.ErrStaleEstimate},
		{name: "corrupt bytes", mutate: func(r *contexty.CompactionRecord) { r.Result.Wire[0] = 'x' }, want: contexty.ErrReplayContentMismatch},
		{name: "accept without decision", mutate: func(r *contexty.CompactionRecord) { r.State = contexty.RecordAccepted }, want: contexty.ErrInvalidRecordState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, err := baseline.Clone()
			require.NoError(t, err)
			// Act.
			tc.mutate(&record)
			err = record.Validate()
			// Assert.
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestCompaction_ReplayFailures(t *testing.T) {
	// Arrange.
	proposal := fixtureCompactionFixture(t)
	accepted, err := proposal.Accept("host-accept")
	require.NoError(t, err)
	expected := contexty.ContentRef{ID: accepted.ID, Digest: accepted.Digest}
	changed := accepted.Profile
	changed.Model.Revision = "changed"
	// Act and Assert: stale policy, coverage and deleted bytes never trigger recompute.
	result, err := contexty.ReplayCompaction(
		context.Background(),
		accepted,
		expected,
		changed,
		accepted.Covered,
		accepted.Budget,
		contexty.DefaultJSONSerializer(),
	)
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, result)
	result, err = contexty.ReplayCompaction(
		context.Background(),
		accepted,
		expected,
		accepted.Profile,
		accepted.Covered[:1],
		accepted.Budget,
		contexty.DefaultJSONSerializer(),
	)
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, result)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = contexty.ReplayCompaction(
		ctx,
		accepted,
		expected,
		accepted.Profile,
		accepted.Covered,
		accepted.Budget,
		contexty.DefaultJSONSerializer(),
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	accepted.Result = nil
	result, err = contexty.ReplayCompaction(
		context.Background(),
		accepted,
		expected,
		accepted.Profile,
		accepted.Covered,
		accepted.Budget,
		contexty.DefaultJSONSerializer(),
	)
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.Zero(t, result)
	wire, err := contexty.EncodeCompactionRecord(proposal)
	require.NoError(t, err)
	decoded, err := contexty.DecodeCompactionRecord(append(wire, []byte(" {}")...))
	require.ErrorIs(t, err, contexty.ErrInvalidCompaction)
	require.Zero(t, decoded)
}

func TestCompaction_OverflowAndTransitions(t *testing.T) {
	// Arrange: a valid proposal may describe an over-budget attempted compaction.
	baseline := fixtureCompactionFixture(t)
	var summary contexty.Message
	codec := contexty.DefaultJSONSerializer()
	require.NoError(t, codec.Unmarshal(baseline.Result.Wire, &summary))
	reporter, err := contexty.NewEstimateReporter(contexty.CharTokenEstimator{}, fixtureEstimateProfile(), codec)
	require.NoError(t, err)
	budget := contexty.EffectiveInputBudget(3)
	report, err := reporter.Report(context.Background(), contexty.EstimateRequest{Budget: budget,
		Segments: []contexty.EstimateSegment{{Name: "summary", Messages: []contexty.Message{summary}}}})
	require.NoError(t, err)
	proposal, err := contexty.NewCompactionRecord(baseline.ID, baseline.Profile, baseline.Covered, baseline.Output,
		baseline.Lineage, budget, baseline.Result, &report)
	require.NoError(t, err)
	// Act.
	accepted, err := proposal.Accept("host-accept")
	// Assert: accepting an oversized result cannot quietly truncate coverage.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Zero(t, accepted)
	require.NoError(t, proposal.Validate())
	_, err = baseline.Accept("")
	require.ErrorIs(t, err, contexty.ErrInvalidRecordState)
	_, err = baseline.Supersede("decision")
	require.ErrorIs(t, err, contexty.ErrInvalidRecordState)
	accepted, err = baseline.Accept("host-accept")
	require.NoError(t, err)
	_, err = accepted.Accept("second-decision")
	require.ErrorIs(t, err, contexty.ErrInvalidRecordState)
	result, err := contexty.ReplayCompaction(context.Background(), accepted,
		contexty.ContentRef{ID: accepted.ID, Digest: accepted.Digest}, accepted.Profile, accepted.Covered,
		contexty.WindowInputBudget(12, 2, 0), codec)
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, result)
}
