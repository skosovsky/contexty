package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestTruncation_Configuration(t *testing.T) {
	// Arrange: mutable caller config must not change a constructed pipeline.
	atomic := true
	roles := []string{"user", "system", "user", ""}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100),
		DropHead: contexty.DropHeadConfig{KeepTurnAtomicity: &atomic, MinMessages: 2, ProtectedRoles: roles}},
		contexty.CharTokenEstimator{})
	atomic = false
	roles[0] = "changed"
	// Act: no eviction is needed; config still participates in replay identity.
	compiled, err := fixtureTruncationEngine(pipe).CompileSnapshot(context.Background(),
		contexty.CompileRequest{CompilationID: "truncation"})
	require.NoError(t, err)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	expected.Budgets[0].Truncation.DropHead.MinMessages++
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	// Assert: exact normalized config and immutable replay expectation.
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, replayed)
	profile := compiled.Manifest.Budgets[0].Truncation
	require.True(t, *profile.DropHead.KeepTurnAtomicity)
	require.Equal(t, []string{"system", "user"}, profile.DropHead.ProtectedRoles)
	require.Equal(t, 2, profile.DropHead.MinMessages)
	*expected.Budgets[0].Truncation.DropHead.KeepTurnAtomicity = false
	expected.Budgets[0].Truncation.DropHead.ProtectedRoles[0] = "changed"
	require.NoError(t, accepted.Validate())
	require.NoError(t, compiled.Manifest.Validate())
}

func TestTruncation_HostIdentity(t *testing.T) {
	// Arrange: a custom strategy cannot borrow a built-in or aggregate identity.
	calls := 0
	cfg := contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100),
		TruncateStrategy: fixtureHostStrategy{calls: &calls}}
	missing := contexty.NewBudgetPipeline(cfg, contexty.CharTokenEstimator{})
	// Act.
	failed, err := fixtureTruncationEngine(missing).CompileSnapshot(context.Background(),
		contexty.CompileRequest{CompilationID: "missing"})
	// Assert: configuration fails even without an eviction call.
	require.ErrorIs(t, err, contexty.ErrInvalidRecordingComponent)
	require.Zero(t, failed)
	require.Zero(t, calls)
	reserved := contexty.NewBudgetPipeline(cfg, contexty.CharTokenEstimator{},
		contexty.WithTruncationDescriptor(contexty.Descriptor{ID: "contexty/truncate/strict", Revision: "contract"}))
	failed, err = fixtureTruncationEngine(reserved).CompileSnapshot(context.Background(),
		contexty.CompileRequest{CompilationID: "reserved"})
	require.ErrorIs(t, err, contexty.ErrInvalidRecordingComponent)
	require.Zero(t, failed)
	// Arrange/Act/Assert: explicit host identity is pinned and replay rejects a change.
	pipe := contexty.NewBudgetPipeline(cfg, contexty.CharTokenEstimator{},
		contexty.WithTruncationDescriptor(contexty.Descriptor{ID: "host/truncation", Revision: "pinned"}))
	compiled, err := fixtureTruncationEngine(pipe).CompileSnapshot(context.Background(),
		contexty.CompileRequest{CompilationID: "host"})
	require.NoError(t, err)
	require.Nil(t, compiled.Manifest.Budgets[0].Truncation.DropHead)
	require.Equal(t, "host/truncation", compiled.Manifest.Budgets[0].Truncation.Descriptor.ID)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	expected.Budgets[0].Truncation.Descriptor.Revision = "changed"
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Zero(t, replayed)
}

func TestTruncation_BuiltinProfiles(t *testing.T) {
	for _, tc := range []struct {
		name     string
		strategy contexty.EvictionStrategy
	}{
		{name: "strict", strategy: contexty.NewStrictStrategy()},
		{name: "drop", strategy: contexty.NewDropStrategy()},
		{name: "drop-tail", strategy: contexty.NewDropTailStrategy()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: irrelevant fallback config does not describe the selected strategy.
			pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100),
				TruncateStrategy: tc.strategy, DropHead: contexty.DropHeadConfig{MinMessages: 10}},
				contexty.CharTokenEstimator{})
			// Act.
			compiled, err := fixtureTruncationEngine(pipe).CompileSnapshot(context.Background(),
				contexty.CompileRequest{CompilationID: tc.name})
			// Assert.
			require.NoError(t, err)
			require.Equal(t, "contexty/truncate/"+tc.name, compiled.Manifest.Budgets[0].Truncation.Descriptor.ID)
			require.Nil(t, compiled.Manifest.Budgets[0].Truncation.DropHead)
		})
	}
}
