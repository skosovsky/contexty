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
	// Arrange.
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	original := contexty.Message{
		Actor: &contexty.Actor{Kind: "user", ID: "u1", DisplayName: "alice"},
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{
			contexty.TextPart{Text: "hello"},
			contexty.ToolCallPart{ID: "c1", Name: "search", Arguments: contexty.JSONPayload(`{"q":"go"}`)},
		},
		Annotations: contexty.Annotations{Timestamp: &ts},
		Provenance:  contexty.UserProvenance{Channel: "web", UserID: "u1"},
	}
	// Act.
	cloned := original.Clone()
	cloned.Parts[0] = contexty.TextPart{Text: "changed"}
	cloned.Actor.DisplayName = "bob"
	// Assert.
	assert.Equal(t, "hello", original.TextContent())
	assert.Equal(t, "alice", original.Actor.DisplayName)
}

func TestPolymorphicPartsRoundTrip(t *testing.T) {
	// Arrange.
	parts := []contexty.ContentPart{
		contexty.TextPart{Text: "hi"},
		contexty.ImagePart{URL: "https://example.com/a.png"},
		contexty.ToolCallPart{ID: "1", Name: "fn", Arguments: contexty.JSONPayload("{}")},
		contexty.ToolResultPart{ToolCallID: "1", Payload: contexty.TextPayload("ok")},
	}
	data, err := contexty.MarshalParts(parts)
	require.NoError(t, err)
	out, err := contexty.UnmarshalParts(data)
	require.NoError(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.True(t, contexty.MessagesEqual(
		[]contexty.Message{{Role: contexty.RoleUser, Parts: parts}},
		[]contexty.Message{{Role: contexty.RoleUser, Parts: out}},
	))
}

func TestPolymorphicParts_UnknownKindFails(t *testing.T) {
	// Arrange.
	_, err := contexty.UnmarshalParts([]byte(`[{"kind":"unknown","body":{}}]`))
	require.Error(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Contains(t, err.Error(), "unknown content part kind")
}

func TestProvenanceRegistry_RoundTrip(t *testing.T) {
	// Arrange.
	reg := contexty.DefaultProvenanceRegistry()
	msg := contexty.Message{
		Role:       contexty.RoleUser,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "x"}},
		Provenance: contexty.UserProvenance{Channel: "tg"},
	}
	data, err := contexty.MarshalMessageJSON(msg, contexty.MessageCodec{Provenance: reg})
	require.NoError(t, err)
	out, err := contexty.UnmarshalMessageJSON(data, contexty.MessageCodec{Provenance: reg})
	require.NoError(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, "tg", out.Provenance.(contexty.UserProvenance).Channel)
}

func TestProvenanceRegistry_UnregisteredFails(t *testing.T) {
	// Arrange.
	reg := contexty.NewProvenanceRegistry()
	// Act.
	_, err := reg.Decode([]byte(`{"type_id":"missing","payload":{}}`))
	// Assert.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unregistered")
}

func TestUnmarshalMessageJSON_NilRegistryWithProvenanceFails(t *testing.T) {
	// Arrange.
	msg := contexty.Message{
		Role:       contexty.RoleUser,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "x"}},
		Provenance: contexty.UserProvenance{Channel: "tg"},
	}
	data, err := contexty.MarshalMessageJSON(
		msg,
		contexty.MessageCodec{Provenance: contexty.DefaultProvenanceRegistry()},
	)
	require.NoError(t, err)
	_, err = contexty.UnmarshalMessageJSON(data, contexty.MessageCodec{})
	require.Error(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Contains(t, err.Error(), "registry is nil")
}

func TestConversationStateStore_OCC(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	s0, err := loadState(ctx, store, "t1")
	require.NoError(t, err)
	require.NoError(t, appendSegment(ctx, store, "t1", s0.Version(), contexty.SegmentHistory,
		contexty.TextMessage(contexty.RoleUser, "one"),
	))
	s1, err := loadState(ctx, store, "t1")
	require.NoError(t, err)
	err = appendSegment(ctx, store, "t1", 0, contexty.SegmentHistory, contexty.TextMessage(contexty.RoleUser, "stale"))
	require.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
	// Act / Assert: exercise the contract and check its result.
	require.NoError(t, appendSegment(ctx, store, "t1", s1.Version(), contexty.SegmentHistory,
		contexty.TextMessage(contexty.RoleAssistant, "two"),
	))
}

func TestRender_DoesNotMutateSnapshot(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "secret@email.com"),
	})
	before := snap.Segment(contexty.SegmentHistory)[0].TextContent()
	// Act.
	_, err := contexty.Render(ctx, snap, contexty.ViewLLMXML)
	// Assert.
	require.NoError(t, err)
	after := snap.Segment(contexty.SegmentHistory)[0].TextContent()
	assert.Equal(t, before, after)
}

func TestViews_DifferentProjections(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "rules"),
	})
	// Act.
	xml, err := contexty.Render(ctx, snap, contexty.ViewLLMXML)
	// Assert.
	require.NoError(t, err)
	flat, err := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
	require.NoError(t, err)
	assert.Contains(t, xml, "<system>")
	assert.Contains(t, flat, "system: rules")
}

func TestHostTextTransform_CopyOnWrite(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	orig := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a@b.com"),
	})
	masked, err := fixtureEmailTransform().Transform(ctx, orig)
	require.NoError(t, err)
	assert.Equal(t, "[REDACTED]", masked.Segment(contexty.SegmentHistory)[0].TextContent())
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, "a@b.com", orig.Segment(contexty.SegmentHistory)[0].TextContent())
}

func TestEngine_Compile_DeferredAndResolveVar(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	s0, _ := loadState(ctx, store, "t")
	_ = updateSegment(ctx, store, "t", s0.Version(), contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "sys"),
	})
	engine := fixtureEngine(
		contexty.WithConversationID("t"),
		contexty.WithStateStore(store),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "mem",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{
					Messages: []contexty.Message{contexty.TextMessage(contexty.RoleUser, "dynamic")},
				}, nil
			},
		}),
	)
	// Act.
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	payload := result.Payload
	assert.Equal(t, "sys", payload.System[0].TextContent())
	assert.Equal(t, "dynamic", payload.Memory[0].TextContent())
	// resolve vars not persisted
	snap, _ := loadState(ctx, store, "t")
	assert.Empty(t, snap.Segment(contexty.SegmentMemory))
}

func TestBudgetPipeline_SummaryWithinCapacity(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "aaaaaaaaaa"),
		contexty.TextMessage(contexty.RoleUser, "bbbbbbbbbb"),
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget: contexty.EffectiveInputBudget(15),
		Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
			return contexty.TextMessage(contexty.RoleSystem, "sum"), nil
		}),
		TruncateStrategy: contexty.NewDropTailStrategy(),
	}, contexty.CharTokenEstimator{})
	// Act.
	outBudget, err := pipe.Apply(ctx, msgs)
	out := outBudget.Messages
	// Assert.
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "sum", out[0].TextContent())
}
