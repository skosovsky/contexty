package contexty_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type traceContextKey struct{}

func TestObserver_MessageNodeIDDeterminism(t *testing.T) {
	msg := contexty.TextMessage(contexty.RoleUser, "hello")
	id1 := contexty.MessageNodeID(msg, 0, "history")
	id2 := contexty.MessageNodeID(msg, 0, "history")
	assert.Equal(t, id1, id2)
	assert.NotEmpty(t, id1)
	assert.NotContains(t, id1, "unknown")

	other := contexty.TextMessage(contexty.RoleUser, "other")
	id3 := contexty.MessageNodeID(other, 0, "history")
	assert.NotEqual(t, id1, id3)

	withRef := msg
	withRef.Annotations.RefID = "msg-ref"
	assert.Equal(t, "msg-ref", contexty.MessageNodeID(withRef, 9, "history"))
}

func TestObserver_DropHeadEmitsEvictions(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	ctx = contexty.WithBudgetObservationForTest(ctx, rec, "history")

	strategy := contexty.NewDropHeadStrategy(contexty.DropHeadConfig{MinMessages: 1})
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		contexty.TextMessage(contexty.RoleUser, "mid"),
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	out, err := strategy.Apply(ctx, msgs, 30, 10, &contexty.FixedEstimator{TokensPerMessage: 10})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, rec.Evictions, 2)
	for _, e := range rec.Evictions {
		assert.Equal(t, contexty.EvictionReasonTruncate, e.Reason)
		assert.NotEmpty(t, e.NodeID)
		assert.Equal(t, ctx, e.Ctx)
	}
}

func TestObserver_ToolTurnAtomicEviction(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	ctx = contexty.WithBudgetObservationForTest(ctx, rec, "history")

	strategy := contexty.NewDropHeadStrategy(contexty.DropHeadConfig{MinMessages: 1})
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.TextPart{Text: "call"},
				contexty.ToolCallPart{ID: "call_a", Name: "f", Arguments: "{}"},
			},
		},
		{
			Role:  contexty.RoleTool,
			Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "call_a", Content: "r1"}},
		},
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	out, err := strategy.Apply(ctx, msgs, 40, 15, &contexty.FixedEstimator{TokensPerMessage: 10})
	require.NoError(t, err)
	require.NotEmpty(t, out)
	evicted := 0
	for _, e := range rec.Evictions {
		if e.Reason == contexty.EvictionReasonTruncate {
			evicted++
		}
	}
	assert.GreaterOrEqual(t, evicted, 2, "tool turn should evict assistant + tool atomically")
}

func TestObserver_BudgetPipelineSummarizeAndTokens(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	ctx = contexty.WithBudgetObservationForTest(ctx, rec, "history")

	msgs := []contexty.Message{contexty.TextMessage(contexty.RoleUser, "long message")}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		TokenLimit: 10,
		Summarizer: stubSummarizer(func(context.Context, []contexty.Message) (contexty.Message, error) {
			return contexty.TextMessage(contexty.RoleSystem, "compressed"), nil
		}),
	}, contexty.CharTokenEstimator{})
	out, err := pipe.Apply(ctx, msgs)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, rec.Tokens, 1)
	assert.Equal(t, "history", rec.Tokens[0].BlockID)
	assert.Positive(t, rec.Tokens[0].Count)
	require.Len(t, rec.Summaries, 1)
	assert.Greater(t, rec.Summaries[0], 1.0)
}

func TestObserver_ContextPropagation(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceContextKey{}, "trace-42")
	rec := &contexty.RecordingObserver{}
	ctx = contexty.WithBudgetObservationForTest(ctx, rec, "history")

	strategy := contexty.NewDropStrategy()
	_, err := strategy.Apply(ctx, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "x"),
	}, 100, 10, &contexty.FixedEstimator{TokensPerMessage: 10})
	require.NoError(t, err)
	require.Len(t, rec.Evictions, 1)
	assert.Equal(t, "trace-42", rec.Evictions[0].Ctx.Value(traceContextKey{}))
}

func TestObserver_CompilePipelineEvent(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "obs")
	require.NoError(t, err)
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a"),
		contexty.TextMessage(contexty.RoleUser, "b"),
		contexty.TextMessage(contexty.RoleUser, "c"),
	}
	require.NoError(t, store.UpdateSegment(ctx, "obs", s0.Version(), contexty.SegmentHistory, msgs))

	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
		contexty.WithBudgetObserver(rec),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("obs"),
		contexty.WithStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(rec),
	)
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, payload.History)
	require.Len(t, rec.Tokens, 1)
	require.NotEmpty(t, rec.Evictions)
	require.Len(t, rec.Compilations, 1)
	assert.Positive(t, rec.Compilations[0].TotalCost)
	assert.Greater(t, rec.Compilations[0].Duration, time.Duration(0))
}

func TestObserver_OrphanRepairEmitsEvictions(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	ctx = contexty.WithBudgetObservationForTest(ctx, rec, "history")

	optOut := false
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		TokenLimit: 12,
		TruncateStrategy: contexty.NewDropHeadStrategy(contexty.DropHeadConfig{
			KeepTurnAtomicity: &optOut,
		}),
	}, &contexty.FixedEstimator{TokensPerMessage: 5})
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		{
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "a", Name: "fn", Arguments: "{}"}},
		},
		{
			Role:  contexty.RoleTool,
			Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "a", Content: "r"}},
		},
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	_, err := pipe.Apply(ctx, msgs)
	require.NoError(t, err)
	hasRepair := false
	for _, e := range rec.Evictions {
		if e.Reason == contexty.EvictionReasonOrphanRepair {
			hasRepair = true
			break
		}
	}
	assert.True(t, hasRepair)
}

func TestObserver_DirectBudgetPipelineApply(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 10, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
		contexty.WithBudgetObserver(rec),
	)
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	out, err := pipe.Apply(ctx, msgs)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, rec.Tokens, 1)
	assert.Equal(t, "budget", rec.Tokens[0].BlockID)
	require.NotEmpty(t, rec.Evictions)
}

func TestObserver_CompilePassiveOnTelemetryEstimateFailure(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "passive")
	require.NoError(t, err)
	require.NoError(t, store.UpdateSegment(ctx, "passive", s0.Version(), contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hello"),
	}))
	est := &callCountEstimator{}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 1000},
		est,
		contexty.WithBudgetObserver(rec),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("passive"),
		contexty.WithStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(rec),
	)
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, payload.History, 1)
	require.Empty(t, rec.Compilations)
}

func TestObserver_CompileAndBudgetObserverPriority(t *testing.T) {
	ctx := context.Background()
	engineRec := &contexty.RecordingObserver{}
	budgetRec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "priority")
	require.NoError(t, err)
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a"),
		contexty.TextMessage(contexty.RoleUser, "b"),
		contexty.TextMessage(contexty.RoleUser, "c"),
	}
	require.NoError(t, store.UpdateSegment(ctx, "priority", s0.Version(), contexty.SegmentHistory, msgs))
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
		contexty.WithBudgetObserver(budgetRec),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("priority"),
		contexty.WithStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(engineRec),
	)
	_, err = engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, budgetRec.Tokens, 1)
	require.NotEmpty(t, budgetRec.Evictions)
	require.Empty(t, budgetRec.Compilations)
	require.Len(t, engineRec.Compilations, 1)
	require.Empty(t, engineRec.Tokens)
	require.Empty(t, engineRec.Evictions)
}

func TestObserver_EngineOnlyObserverGetsCompileEventOnly(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "engine-only")
	require.NoError(t, err)
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a"),
		contexty.TextMessage(contexty.RoleUser, "b"),
		contexty.TextMessage(contexty.RoleUser, "c"),
	}
	require.NoError(t, store.UpdateSegment(ctx, "engine-only", s0.Version(), contexty.SegmentHistory, msgs))
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("engine-only"),
		contexty.WithStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(rec),
	)
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, payload.History)
	require.Len(t, rec.Compilations, 1)
	require.Empty(t, rec.Tokens)
	require.Empty(t, rec.Evictions)
	require.Empty(t, rec.Summaries)
}
