package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestContract_Reservations(t *testing.T) {
	// Arrange: these modes describe the same input capacity, not two deductions.
	message := contexty.TextMessage(contexty.RoleUser, "1234567")
	message.ID = "m"
	system := contexty.TextMessage(contexty.RoleSystem, "123")
	system.ID = "sys"
	request := contexty.CompileRequest{
		CompilationID: "reserved",
		System:        []contexty.Message{system},
		History: []contexty.Message{
			message,
		},
		Targets: []contexty.CompileTarget{{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "target",
			Budget: contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.WindowInputBudget(12, 3, 2)},
				contexty.CharTokenEstimator{})}},
	}
	for _, budget := range []contexty.BudgetRequest{
		contexty.WindowInputBudget(20, 7, 3), contexty.EffectiveInputBudget(10),
	} {
		// Act.
		engine := contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
			contexty.WithCompileRecording(fixtureRecordProfile("target")),
			contexty.WithBudgetPipeline(contexty.SegmentHistory, contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: budget}, contexty.CharTokenEstimator{})))
		result, err := engine.CompileSnapshot(context.Background(), request)
		// Assert: main reserves system once; target has its own seven-token capacity.
		require.NoError(t, err)
		require.Equal(t, []contexty.Message{message}, result.Payload.History)
		require.Equal(t, []contexty.Message{message}, result.Projections["target"].Messages)
		require.Equal(t, budget, result.Manifest.Budgets[0].Request)
		require.Equal(t, 10, result.Manifest.Budgets[0].TokenLimit)
		require.Equal(t, 10, result.Manifest.Budgets[0].EstimatedTokens)
		require.Equal(t, 7, result.Manifest.Budgets[1].TokenLimit)
		wire, wireErr := contexty.EncodeManifest(*result.Manifest)
		require.NoError(t, wireErr)
		restored, decodeErr := contexty.DecodeManifest(wire)
		require.NoError(t, decodeErr)
		require.Equal(t, result.Manifest.Budgets, restored.Budgets)
	}
	// Arrange / Act / Assert: zero input capacity is explicit, not an absent limit.
	zero := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.WindowInputBudget(5, 3, 2)},
		contexty.CharTokenEstimator{})
	emptyBudget, err := zero.Apply(context.Background(), []contexty.Message{message})
	empty := emptyBudget.Messages
	require.NoError(t, err)
	require.Empty(t, empty)
	_, err = zero.ApplyWithLimit(context.Background(), nil, 1)
	require.ErrorIs(t, err, contexty.ErrInvalidBudgetRequest)
}

func TestInvalid_Reservations(t *testing.T) {
	// Arrange: subtraction-based checks must also reject integer overflow safely.
	maxInt := int(^uint(0) >> 1)
	invalid := []contexty.BudgetRequest{
		{}, contexty.EffectiveInputBudget(-1), contexty.WindowInputBudget(-1, 0, 0),
		contexty.WindowInputBudget(10, -1, 0), contexty.WindowInputBudget(10, 0, -1),
		contexty.WindowInputBudget(10, 11, 0), contexty.WindowInputBudget(10, 6, 5),
		contexty.WindowInputBudget(maxInt, maxInt, maxInt),
		{Mode: contexty.BudgetEffective, EffectiveLimit: 10, OutputReservation: 1},
		{Mode: contexty.BudgetWindow, Window: 10, EffectiveLimit: 10},
		{Mode: "unknown", EffectiveLimit: 10},
	}
	for _, budget := range invalid {
		// Act / Assert: reject even empty input and do not execute deferred callbacks.
		_, err := budget.Resolve()
		require.ErrorIs(t, err, contexty.ErrInvalidBudgetRequest)
		pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: budget}, contexty.CharTokenEstimator{})
		_, err = pipe.Apply(context.Background(), nil)
		require.ErrorIs(t, err, contexty.ErrInvalidBudgetRequest)
		calls := 0
		deferred := contexty.WithDeferredBlocks(contexty.DeferredBlock{Name: "body", Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				calls++
				return contexty.DeferredResult{Messages: nil}, nil
			}})
		engine := contexty.NewEngine(deferred, contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe))
		_, err = engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
		require.ErrorIs(t, err, contexty.ErrInvalidBudgetRequest)
		require.Zero(t, calls)
		engine = contexty.NewEngine(deferred)
		_, err = engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
			Targets: []contexty.CompileTarget{
				{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "bad", Budget: pipe},
			},
		})
		require.ErrorIs(t, err, contexty.ErrInvalidBudgetRequest)
		require.Zero(t, calls)
	}
	// Arrange / Act / Assert: maximal valid window and bounded history sub-limit.
	limit, err := contexty.WindowInputBudget(maxInt, maxInt-1, 1).Resolve()
	require.NoError(t, err)
	require.Zero(t, limit)
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)},
		contexty.CharTokenEstimator{})
	for _, sublimit := range []int{-1, 11} {
		_, err = pipe.ApplyWithLimit(context.Background(), nil, sublimit)
		require.ErrorIs(t, err, contexty.ErrInvalidBudgetRequest)
	}
}
