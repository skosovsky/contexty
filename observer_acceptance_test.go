package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Observer_EvictionTelemetry(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStateStore()
	s0, err := loadState(ctx, store, "evict-obs")
	require.NoError(t, err)
	msgs := []contexty.Message{
		{
			ID:    "u-old",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}},
		},
		{
			ID:    "u-new",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "new"}},
		},
	}
	require.NoError(
		t,
		updateSegment(ctx, store, "evict-obs", s0.Version(), contexty.SegmentHistory, msgs),
	)

	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:   contexty.EffectiveInputBudget(15),
			DropHead: contexty.DropHeadConfig{MinMessages: 1},
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
		contexty.WithBudgetObserver(rec),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("evict-obs"),
		contexty.WithStateStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	// Act.
	_, err = engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	require.NotEmpty(t, rec.Evictions)
	nodeIDs := rec.EvictionNodeIDs()
	assert.Contains(t, nodeIDs, "u-old")
	for _, id := range nodeIDs {
		assert.NotEmpty(t, id)
	}
}

func TestAcceptance_Observer_CompileTelemetry(t *testing.T) {
	// Arrange.
	ctx := context.WithValue(context.Background(), traceContextKey{}, "dod-trace")
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStateStore()
	s0, err := loadState(ctx, store, "compile-obs")
	require.NoError(t, err)
	history := []contexty.Message{contexty.TextMessage(contexty.RoleUser, "hello")}
	require.NoError(
		t,
		updateSegment(ctx, store, "compile-obs", s0.Version(), contexty.SegmentHistory, history),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("compile-obs"),
		contexty.WithStateStore(store),
		contexty.WithObserver(rec),
	)
	// Act.
	_, err = engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	require.Len(t, rec.Compilations, 1)
	assert.Equal(t, "dod-trace", rec.Compilations[0].Ctx.Value(traceContextKey{}))
	assert.Positive(t, rec.Compilations[0].TotalCost)
}

func TestAcceptance_Observer_DoesNotBreakCompile(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStateStore()
	s0, err := loadState(ctx, store, "obs-passive")
	require.NoError(t, err)
	history := []contexty.Message{contexty.TextMessage(contexty.RoleUser, "stable")}
	require.NoError(
		t,
		updateSegment(ctx, store, "obs-passive", s0.Version(), contexty.SegmentHistory, history),
	)
	est := &callCountEstimator{}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1000)},
		est,
		contexty.WithBudgetObserver(rec),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("obs-passive"),
		contexty.WithStateStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(rec),
	)
	// Act.
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	payload := result.Payload
	require.Len(t, payload.History, 1)
	require.Empty(t, rec.Compilations)
}
