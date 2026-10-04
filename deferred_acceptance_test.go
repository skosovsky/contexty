package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Deferred_NotPersisted(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	s0, _ := loadState(ctx, store, "t")
	require.NoError(
		t,
		updateSegment(ctx, store, "t", s0.Version(), contexty.SegmentSystem, []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "sys"),
		}),
	)
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
	assert.Equal(t, "dynamic", payload.Memory[0].TextContent())
	snap, _ := loadState(ctx, store, "t")
	assert.Empty(t, snap.Segment(contexty.SegmentMemory))
}

func TestAcceptance_Deferred_RedactionThroughCompile(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	engine := fixtureEngine(
		contexty.WithConversationID("t"),
		contexty.WithStateStore(store),
		contexty.WithTransformHooks(fixtureEmailTransform()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "mem",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{
					contexty.TextMessage(contexty.RoleUser, "contact a@b.com"),
				}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	payload := result.Payload
	require.Len(t, payload.Memory, 1)
	assert.Equal(t, "contact [REDACTED]", payload.Memory[0].TextContent())
}

func TestAcceptance_Deferred_DuplicateMessageID(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "dup-deferred",
			Segment: contexty.SegmentHistory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "hist-dup",
					Role:  contexty.RoleUser,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "from deferred"}},
				}}}, nil
			},
		}),
	)
	// Act.
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "hist-dup",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "existing"}},
		}},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrDuplicateMessageID)
}
