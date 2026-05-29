package contexty_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestMessageClone_DeepCopy(t *testing.T) {
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	original := contexty.Message{
		Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{
			contexty.TextPart{Text: "hello"},
			contexty.ToolCallPart{ID: "c1", Name: "search", Arguments: `{"q":"go"}`},
		},
		Annotations: contexty.Annotations{SenderName: "alice", Timestamp: &ts},
		Provenance:  contexty.UserProvenance{Channel: "web", UserID: "u1"},
	}
	cloned := original.Clone()
	cloned.Parts[0] = contexty.TextPart{Text: "changed"}
	cloned.Annotations.SenderName = "bob"
	assert.Equal(t, "hello", original.TextContent())
	assert.Equal(t, "alice", original.Annotations.SenderName)
}

func TestPolymorphicPartsRoundTrip(t *testing.T) {
	parts := []contexty.ContentPart{
		contexty.TextPart{Text: "hi"},
		contexty.ImagePart{URL: "https://example.com/a.png"},
		contexty.ToolCallPart{ID: "1", Name: "fn", Arguments: "{}"},
		contexty.ToolResultPart{ToolCallID: "1", Content: "ok"},
	}
	data, err := contexty.MarshalParts(parts)
	require.NoError(t, err)
	out, err := contexty.UnmarshalParts(data)
	require.NoError(t, err)
	assert.True(t, contexty.MessagesEqual(
		[]contexty.Message{{Role: contexty.RoleUser, Parts: parts}},
		[]contexty.Message{{Role: contexty.RoleUser, Parts: out}},
	))
}

func TestPolymorphicParts_UnknownKindFails(t *testing.T) {
	_, err := contexty.UnmarshalParts([]byte(`[{"kind":"unknown","body":{}}]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown content part kind")
}

func TestProvenanceRegistry_RoundTrip(t *testing.T) {
	reg := contexty.DefaultProvenanceRegistry()
	msg := contexty.Message{
		Role:       contexty.RoleUser,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "x"}},
		Provenance: contexty.UserProvenance{Channel: "tg"},
	}
	data, err := contexty.MarshalMessageJSON(msg, reg)
	require.NoError(t, err)
	out, err := contexty.UnmarshalMessageJSON(data, reg)
	require.NoError(t, err)
	assert.Equal(t, "tg", out.Provenance.(contexty.UserProvenance).Channel)
}

func TestProvenanceRegistry_UnregisteredFails(t *testing.T) {
	reg := contexty.NewProvenanceRegistry()
	_, err := reg.Decode([]byte(`{"type_id":"missing","payload":{}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unregistered")
}

func TestUnmarshalMessageJSON_NilRegistryWithProvenanceFails(t *testing.T) {
	msg := contexty.Message{
		Role:       contexty.RoleUser,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "x"}},
		Provenance: contexty.UserProvenance{Channel: "tg"},
	}
	data, err := contexty.MarshalMessageJSON(msg, contexty.DefaultProvenanceRegistry())
	require.NoError(t, err)
	_, err = contexty.UnmarshalMessageJSON(data, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry is nil")
}

func TestConversationStore_OCC(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	s0, err := store.Load(ctx, "t1")
	require.NoError(t, err)
	require.NoError(t, store.AppendSegment(ctx, "t1", s0.Version(), contexty.SegmentHistory,
		contexty.TextMessage(contexty.RoleUser, "one"),
	))
	s1, err := store.Load(ctx, "t1")
	require.NoError(t, err)
	err = store.AppendSegment(ctx, "t1", 0, contexty.SegmentHistory, contexty.TextMessage(contexty.RoleUser, "stale"))
	require.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
	require.NoError(t, store.AppendSegment(ctx, "t1", s1.Version(), contexty.SegmentHistory,
		contexty.TextMessage(contexty.RoleAssistant, "two"),
	))
}

func TestRender_DoesNotMutateSnapshot(t *testing.T) {
	ctx := context.Background()
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "secret@email.com"),
	})
	before := snap.Segment(contexty.SegmentHistory)[0].TextContent()
	_, err := contexty.Render(ctx, snap, contexty.ViewLLMXML)
	require.NoError(t, err)
	after := snap.Segment(contexty.SegmentHistory)[0].TextContent()
	assert.Equal(t, before, after)
}

func TestViews_DifferentProjections(t *testing.T) {
	ctx := context.Background()
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "rules"),
	})
	xml, err := contexty.Render(ctx, snap, contexty.ViewLLMXML)
	require.NoError(t, err)
	flat, err := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
	require.NoError(t, err)
	assert.Contains(t, xml, "<system>")
	assert.Contains(t, flat, "system: rules")
}

func TestRedactionHook_CopyOnWrite(t *testing.T) {
	ctx := context.Background()
	orig := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a@b.com"),
	})
	masked, err := contexty.NewRedactionHook().Transform(ctx, orig)
	require.NoError(t, err)
	assert.Equal(t, "[REDACTED]", masked.Segment(contexty.SegmentHistory)[0].TextContent())
	assert.Equal(t, "a@b.com", orig.Segment(contexty.SegmentHistory)[0].TextContent())
}

func TestEngine_Compile_DeferredAndOverlay(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStore()
	s0, _ := store.Load(ctx, "t")
	_ = store.UpdateSegment(ctx, "t", s0.Version(), contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "sys"),
	})
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
	).WithOverlay(contexty.Overlay{"lang": "ru"})
	payload, err := engine.Compile(ctx)
	require.NoError(t, err)
	assert.Equal(t, "sys", payload.System[0].TextContent())
	assert.Equal(t, "dynamic", payload.Memory[0].TextContent())
	assert.Equal(t, "ru", payload.Overlay["lang"])
	// overlay not persisted
	snap, _ := store.Load(ctx, "t")
	assert.Empty(t, snap.Segment(contexty.SegmentMemory))
}

func TestBudgetPipeline_SummarizeThenTruncate(t *testing.T) {
	ctx := context.Background()
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "aaaaaaaaaa"),
		contexty.TextMessage(contexty.RoleUser, "bbbbbbbbbb"),
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		TokenLimit: 15,
		Summarizer: stubSummarizer(func(context.Context, []contexty.Message) (contexty.Message, error) {
			return contexty.TextMessage(contexty.RoleSystem, "sum"), nil
		}),
		TruncateStrategy: contexty.NewDropTailStrategy(),
	}, contexty.CharTokenEstimator{})
	out, err := pipe.Apply(ctx, msgs)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "sum", out[0].TextContent())
}
