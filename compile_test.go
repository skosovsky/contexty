package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func reqHistory(msgs []contexty.Message) contexty.CompileRequest {
	return contexty.CompileRequest{History: msgs}
}

func TestStatelessCompile(t *testing.T) {
	ctx := context.Background()
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hello"),
		contexty.TextMessage(contexty.RoleUser, "world"),
	}
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{TokenLimit: 1000},
				&contexty.FixedEstimator{TokensPerMessage: 10},
			),
		),
	)
	result, err := engine.CompileSnapshot(ctx, reqHistory(msgs))
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 2)
}

func TestStatelessCompile_RedactionAndBudget(t *testing.T) {
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	req := reqHistory([]contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "contact me at user@example.com"),
		contexty.TextMessage(contexty.RoleUser, "more"),
		contexty.TextMessage(contexty.RoleUser, "tail"),
	})
	result, err := engine.CompileSnapshot(ctx, req)
	require.NoError(t, err)
	require.NotEmpty(t, result.Payload.History)
	assert.NotContains(t, result.Payload.History[0].TextContent(), "user@example.com")
}

func TestStatelessCompile_ObserverTelemetry(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a"),
		contexty.TextMessage(contexty.RoleUser, "b"),
		contexty.TextMessage(contexty.RoleUser, "c"),
	}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
		contexty.WithBudgetObserver(rec),
	)
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(rec),
	)
	result, err := engine.CompileSnapshot(ctx, reqHistory(msgs))
	require.NoError(t, err)
	require.NotEmpty(t, result.Payload.History)
	require.Len(t, rec.Tokens, 1)
	require.NotEmpty(t, rec.Evictions)
	require.Len(t, rec.Compilations, 1)
}

func TestStatelessCompile_DeferredBlocks(t *testing.T) {
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "hint",
			Segment: contexty.SegmentSystem,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{
					contexty.TextMessage(contexty.RoleSystem, "dynamic"),
				}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{})
	require.NoError(t, err)
	require.Len(t, result.Payload.System, 1)
	assert.Equal(t, "dynamic", result.Payload.System[0].TextContent())
}

func TestStatelessCompile_IgnoresStoreAndConversationID(t *testing.T) {
	ctx := context.Background()
	const convID = "stored-conv"
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, convID)
	require.NoError(t, err)
	storeMsgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "from-store"),
	}
	require.NoError(t, store.UpdateSegment(ctx, convID, s0.Version(), contexty.SegmentHistory, storeMsgs))

	engine := contexty.NewEngine(
		contexty.WithStore(store),
		contexty.WithConversationID(convID),
	)
	result, err := engine.CompileSnapshot(ctx, reqHistory([]contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "from-snapshot"),
	}))
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 1)
	assert.Equal(t, "from-snapshot", result.Payload.History[0].TextContent())
}

func TestStatelessCompile_ContextPropagation(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceContextKey{}, "trace-stateless")
	rec := &contexty.RecordingObserver{}
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a"),
		contexty.TextMessage(contexty.RoleUser, "b"),
		contexty.TextMessage(contexty.RoleUser, "c"),
	}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
		contexty.WithBudgetObserver(rec),
	)
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(rec),
	)
	_, err := engine.CompileSnapshot(ctx, reqHistory(msgs))
	require.NoError(t, err)
	require.Len(t, rec.Tokens, 1)
	assert.Equal(t, "trace-stateless", rec.Tokens[0].Ctx.Value(traceContextKey{}))
	require.NotEmpty(t, rec.Evictions)
	for _, e := range rec.Evictions {
		assert.Equal(t, "trace-stateless", e.Ctx.Value(traceContextKey{}))
	}
	require.Len(t, rec.Compilations, 1)
	assert.Equal(t, "trace-stateless", rec.Compilations[0].Ctx.Value(traceContextKey{}))
}
