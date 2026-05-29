package contexty_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

// DoD matrix from Task10 §7.

func TestDoD_MetadataIsolation(t *testing.T) {
	ctx := context.Background()
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	msg := contexty.Message{
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "hello"}},
		Annotations: contexty.Annotations{
			SenderName: "alice",
			RefID:      "msg-42",
			Timestamp:  &ts,
		},
	}
	assert.Equal(t, "hello", msg.TextContent())
	assert.Equal(t, "alice", msg.Annotations.SenderName)
	assert.NotContains(t, msg.TextContent(), "alice")

	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{msg})
	xml, err := contexty.Render(ctx, snap, contexty.ViewLLMXML)
	require.NoError(t, err)
	flat, err := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
	require.NoError(t, err)
	assert.NotContains(t, xml, "alice")
	assert.NotContains(t, xml, "msg-42")
	assert.NotContains(t, flat, "alice")
	assert.NotContains(t, flat, "msg-42")

	store := contexty.NewMemoryConversationStore()
	s0, _ := store.Load(ctx, "meta")
	require.NoError(t, store.UpdateSegment(ctx, "meta", s0.Version(), contexty.SegmentHistory, []contexty.Message{msg}))
	engine := contexty.NewEngine(contexty.WithConversationID("meta"), contexty.WithStore(store))
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, payload.History, 1)
	assert.Equal(t, "hello", payload.History[0].TextContent())
	assert.Equal(t, "alice", payload.History[0].Annotations.SenderName)
}

func TestDoD_ToolPartsEndToEnd(t *testing.T) {
	ctx := context.Background()
	msgs := []contexty.Message{
		{
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "c1", Name: "search", Arguments: `{"q":"x"}`}},
		},
		{
			Role:  contexty.RoleTool,
			Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "c1", Content: "ok"}},
		},
	}
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "tools")
	require.NoError(t, err)
	require.NoError(t, store.UpdateSegment(ctx, "tools", s0.Version(), contexty.SegmentHistory, msgs))
	engine := contexty.NewEngine(contexty.WithConversationID("tools"), contexty.WithStore(store))
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, payload.History, 2)
	assert.True(t, contexty.ToolTurnUsesCanonicalLayout(payload.History, 0))
	require.Len(t, payload.History[0].ToolCallParts(), 1)
	require.Len(t, payload.History[1].ToolResultParts(), 1)

	msg := contexty.Message{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{ID: "c1", Name: "search", Arguments: `{"q":"x"}`},
			contexty.ToolResultPart{ToolCallID: "c1", Content: "ok"},
		},
	}
	data, err := contexty.MarshalMessageJSON(msg, contexty.DefaultProvenanceRegistry())
	require.NoError(t, err)
	out, err := contexty.UnmarshalMessageJSON(data, contexty.DefaultProvenanceRegistry())
	require.NoError(t, err)
	require.Len(t, out.ToolCallParts(), 1)
	require.Len(t, out.ToolResultParts(), 1)
	assert.Equal(t, "c1", out.ToolResultParts()[0].ToolCallID)
}

func TestDoD_TruncationAtomicity(t *testing.T) {
	ctx := context.Background()
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "old"),
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{ID: "a", Name: "fn", Arguments: "{}"},
			},
		},
		{
			Role:  contexty.RoleTool,
			Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "a", Content: "r"}},
		},
		contexty.TextMessage(contexty.RoleUser, "new"),
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		TokenLimit: 12,
	}, &contexty.FixedEstimator{TokensPerMessage: 5})
	out, err := pipe.Apply(ctx, msgs)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "new", out[0].TextContent())
	for _, m := range out {
		assert.NotEqual(t, contexty.RoleAssistant, m.Role)
		assert.NotEqual(t, contexty.RoleTool, m.Role)
	}
}

func TestDoD_UnifiedBudgeting(t *testing.T) {
	ctx := context.Background()
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "aaaaaaaaaaaaaaaa"),
		contexty.TextMessage(contexty.RoleUser, "bbbbbbbbbbbbbbbb"),
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		TokenLimit: 15,
		Summarizer: stubSummarizer(func(context.Context, []contexty.Message) (contexty.Message, error) {
			return contexty.TextMessage(contexty.RoleSystem, "sum"), nil
		}),
	}, &contexty.FixedEstimator{TokensPerMessage: 10})
	out, err := pipe.Apply(ctx, msgs)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "sum", out[0].TextContent())
}

func TestDoD_ViewsNonMutating(t *testing.T) {
	ctx := context.Background()
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "secret@email.com"),
	})
	before := snap.Segment(contexty.SegmentHistory)[0].TextContent()
	xml, err := contexty.Render(ctx, snap, contexty.ViewLLMXML)
	require.NoError(t, err)
	flat, err := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
	require.NoError(t, err)
	assert.NotEqual(t, xml, flat)
	assert.Equal(t, before, snap.Segment(contexty.SegmentHistory)[0].TextContent())
}

func TestDoD_ProvenanceTyped(t *testing.T) {
	reg := contexty.DefaultProvenanceRegistry()
	msg := contexty.Message{
		Role:       contexty.RoleUser,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "x"}},
		Provenance: contexty.UserProvenance{Channel: "tg", UserID: "u1"},
	}
	data, err := contexty.MarshalMessageJSON(msg, reg)
	require.NoError(t, err)
	out, err := contexty.UnmarshalMessageJSON(data, reg)
	require.NoError(t, err)
	prov, ok := out.Provenance.(contexty.UserProvenance)
	require.True(t, ok)
	assert.Equal(t, "tg", prov.Channel)
}

func TestDoD_PolymorphicDecodeStrict(t *testing.T) {
	_, err := contexty.UnmarshalParts([]byte(`[{"kind":"unknown","body":{}}]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown content part kind")
}

func TestDoD_StructuralSharing(t *testing.T) {
	ctx := context.Background()
	base := contexty.EmptySnapshot().WithSegment(contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "immutable"),
	})
	derived := base.WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "new"),
	})
	assert.Equal(t, "immutable", base.Segment(contexty.SegmentSystem)[0].TextContent())
	assert.Equal(t, "new", derived.Segment(contexty.SegmentHistory)[0].TextContent())

	orig := base.WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a@b.com"),
	})
	masked, err := contexty.NewRedactionHook().Transform(ctx, orig)
	require.NoError(t, err)
	assert.Equal(t, "a@b.com", orig.Segment(contexty.SegmentHistory)[0].TextContent())
	assert.Equal(t, "[REDACTED]", masked.Segment(contexty.SegmentHistory)[0].TextContent())
}

func TestDoD_ProvenanceThroughCompile(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	msg := contexty.Message{
		Role:        contexty.RoleUser,
		Parts:       []contexty.ContentPart{contexty.TextPart{Text: "hi"}},
		Annotations: contexty.Annotations{Timestamp: &ts, RefID: "r1"},
		Provenance:  contexty.UserProvenance{Channel: "api"},
	}
	s0, _ := store.Load(ctx, "t")
	require.NoError(t, store.UpdateSegment(ctx, "t", s0.Version(), contexty.SegmentHistory, []contexty.Message{msg}))
	engine := contexty.NewEngine(contexty.WithConversationID("t"), contexty.WithStore(store))
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, payload.History, 1)
	assert.Equal(t, "api", payload.History[0].Provenance.(contexty.UserProvenance).Channel)
}

func TestDoD_RedactionThroughCompile(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	s0, _ := store.Load(ctx, "t")
	require.NoError(t, store.UpdateSegment(ctx, "t", s0.Version(), contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "reach me at a@b.com"),
	}))
	engine := contexty.NewEngine(
		contexty.WithConversationID("t"),
		contexty.WithStore(store),
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
	)
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, payload.History, 1)
	assert.Equal(t, "reach me at [REDACTED]", payload.History[0].TextContent())
	snap, _ := store.Load(ctx, "t")
	assert.Equal(t, "reach me at a@b.com", snap.Segment(contexty.SegmentHistory)[0].TextContent())
}

func TestDoD_DeferredNotPersisted(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	s0, _ := store.Load(ctx, "t")
	require.NoError(t, store.UpdateSegment(ctx, "t", s0.Version(), contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "sys"),
	}))
	engine := contexty.NewEngine(
		contexty.WithConversationID("t"),
		contexty.WithStore(store),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "mem",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{contexty.TextMessage(contexty.RoleUser, "dynamic")}, nil
			},
		}),
	)
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dynamic", payload.Memory[0].TextContent())
	snap, _ := store.Load(ctx, "t")
	assert.Empty(t, snap.Segment(contexty.SegmentMemory))
}

func TestDoD_ToolPartsStorageRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	msgs := []contexty.Message{
		{
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "c1", Name: "search", Arguments: `{}`}},
		},
		{
			Role:  contexty.RoleTool,
			Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "c1", Content: "ok"}},
		},
	}
	s0, err := store.Load(ctx, "roundtrip")
	require.NoError(t, err)
	require.NoError(t, store.UpdateSegment(ctx, "roundtrip", s0.Version(), contexty.SegmentHistory, msgs))
	snap, err := store.Load(ctx, "roundtrip")
	require.NoError(t, err)
	got := snap.Segment(contexty.SegmentHistory)
	require.Len(t, got, 2)
	assert.True(t, contexty.ToolTurnUsesCanonicalLayout(got, 0))
	require.Len(t, got[0].ToolCallParts(), 1)
	require.Len(t, got[1].ToolResultParts(), 1)
}

func TestDoD_ToolTurnCanonicalLayout(t *testing.T) {
	msgs := []contexty.Message{
		{
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "a", Name: "fn", Arguments: "{}"}},
		},
		{
			Role:  contexty.RoleTool,
			Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "a", Content: "r"}},
		},
	}
	assert.True(t, contexty.ToolTurnUsesCanonicalLayout(msgs, 0))
	inMessage := []contexty.Message{{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{ID: "b", Name: "fn", Arguments: "{}"},
			contexty.ToolResultPart{ToolCallID: "b", Content: "inline"},
		},
	}}
	assert.False(t, contexty.ToolTurnUsesCanonicalLayout(inMessage, 0))
}

func TestDoD_DeferredRedactionThroughCompile(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	engine := contexty.NewEngine(
		contexty.WithConversationID("t"),
		contexty.WithStore(store),
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "mem",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{contexty.TextMessage(contexty.RoleUser, "contact a@b.com")}, nil
			},
		}),
	)
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, payload.Memory, 1)
	assert.Equal(t, "contact [REDACTED]", payload.Memory[0].TextContent())
}

func TestDoD_OverlayNotPersisted(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	s0, _ := store.Load(ctx, "ov")
	require.NoError(t, store.UpdateSegment(ctx, "ov", s0.Version(), contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "sys"),
	}))
	engine := contexty.NewEngine(
		contexty.WithConversationID("ov"),
		contexty.WithStore(store),
	).WithOverlay(contexty.Overlay{"reason": "wake"})
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	assert.Equal(t, "wake", payload.Overlay["reason"])
	snap, _ := store.Load(ctx, "ov")
	assert.Empty(t, snap.Segment(contexty.SegmentMemory))
	assert.Len(t, snap.Segment(contexty.SegmentSystem), 1)
}

func TestDoD_OverlayInDeferredResolve(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	engine := contexty.NewEngine(
		contexty.WithConversationID("ov"),
		contexty.WithStore(store),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "locale",
			Segment: contexty.SegmentMemory,
			Resolve: func(ctx context.Context) ([]contexty.Message, error) {
				ov := contexty.CompileOverlayFromContext(ctx)
				return []contexty.Message{
					contexty.TextMessage(contexty.RoleSystem, "locale="+ov["locale"]),
				}, nil
			},
		}),
	).WithOverlay(contexty.Overlay{"locale": "ru-RU"})
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	assert.Equal(t, "locale=ru-RU", payload.Memory[0].TextContent())
}

func TestDoD_EstimatorErrorPropagation(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("tokenizer unavailable")
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		TokenLimit: 10,
	}, &contexty.FailingEstimator{Err: boom})
	_, err := pipe.Apply(ctx, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hello"),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, contexty.ErrTokenCountFailed)
}

func TestDoD_CompileDeterminism(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	s0, _ := store.Load(ctx, "det")
	require.NoError(t, store.UpdateSegment(ctx, "det", s0.Version(), contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "stable"),
	}))
	engine := contexty.NewEngine(
		contexty.WithConversationID("det"),
		contexty.WithStore(store),
	).WithOverlay(contexty.Overlay{"k": "v"})
	p1, err := engine.Compile(ctx)
	require.NoError(t, err)
	p2, err := engine.Compile(ctx)
	require.NoError(t, err)
	assert.Equal(t, p1, p2)
}

func TestDoD_ObserverEvictionTelemetry(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "evict-obs")
	require.NoError(t, err)
	msgs := []contexty.Message{
		{
			Role:        contexty.RoleUser,
			Parts:       []contexty.ContentPart{contexty.TextPart{Text: "old"}},
			Annotations: contexty.Annotations{RefID: "u-old"},
		},
		{
			Role:        contexty.RoleUser,
			Parts:       []contexty.ContentPart{contexty.TextPart{Text: "new"}},
			Annotations: contexty.Annotations{RefID: "u-new"},
		},
	}
	require.NoError(t, store.UpdateSegment(ctx, "evict-obs", s0.Version(), contexty.SegmentHistory, msgs))

	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
		contexty.WithBudgetObserver(rec),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("evict-obs"),
		contexty.WithStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	_, err = engine.Compile(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rec.Evictions)
	nodeIDs := rec.EvictionNodeIDs()
	assert.Contains(t, nodeIDs, "u-old")
	for _, id := range nodeIDs {
		assert.NotEmpty(t, id)
	}
}

func TestDoD_ObserverCompileTelemetry(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceContextKey{}, "dod-trace")
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "compile-obs")
	require.NoError(t, err)
	history := []contexty.Message{contexty.TextMessage(contexty.RoleUser, "hello")}
	require.NoError(t, store.UpdateSegment(ctx, "compile-obs", s0.Version(), contexty.SegmentHistory, history))
	engine := contexty.NewEngine(
		contexty.WithConversationID("compile-obs"),
		contexty.WithStore(store),
		contexty.WithObserver(rec),
	)
	_, err = engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, rec.Compilations, 1)
	assert.Equal(t, "dod-trace", rec.Compilations[0].Ctx.Value(traceContextKey{}))
	assert.Positive(t, rec.Compilations[0].TotalCost)
}

func TestDoD_ObserverDoesNotBreakCompile(t *testing.T) {
	ctx := context.Background()
	rec := &contexty.RecordingObserver{}
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "obs-passive")
	require.NoError(t, err)
	history := []contexty.Message{contexty.TextMessage(contexty.RoleUser, "stable")}
	require.NoError(t, store.UpdateSegment(ctx, "obs-passive", s0.Version(), contexty.SegmentHistory, history))
	est := &callCountEstimator{}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 1000},
		est,
		contexty.WithBudgetObserver(rec),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("obs-passive"),
		contexty.WithStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(rec),
	)
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	require.Len(t, payload.History, 1)
	require.Empty(t, rec.Compilations)
}
