package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_ResolveVar_NotPersisted(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	s0, _ := loadState(ctx, store, "ov")
	require.NoError(
		t,
		updateSegment(ctx, store, "ov", s0.Version(), contexty.SegmentSystem, []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "sys"),
		}),
	)
	engine := fixtureEngine(
		contexty.WithConversationID("ov"),
		contexty.WithStateStore(store),
	)
	// Act.
	_, err := engine.Compile(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("reason", "wake"),
		},
	})
	// Assert.
	require.NoError(t, err)
	snap, _ := loadState(ctx, store, "ov")
	assert.Empty(t, snap.Segment(contexty.SegmentMemory))
	assert.Len(t, snap.Segment(contexty.SegmentSystem), 1)
}

func TestAcceptance_ResolveVar_InDeferredResolve(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	engine := fixtureEngine(
		contexty.WithConversationID("ov"),
		contexty.WithStateStore(store),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "locale",
			Segment: contexty.SegmentMemory,
			Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
				vars := contexty.CompileResolveVarFromContext(ctx)
				return contexty.DeferredResult{Messages: []contexty.Message{
					contexty.TextMessage(contexty.RoleSystem, "locale="+vars["locale"]),
				}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.Compile(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("locale", "ru-RU"),
		},
	})
	// Assert.
	require.NoError(t, err)
	payload := result.Payload
	assert.Equal(t, "locale=ru-RU", payload.Memory[0].TextContent())
}

func TestAcceptance_ResolveVar_InDeferred(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "locale",
			Segment: contexty.SegmentMemory,
			Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
				vars := contexty.CompileResolveVarFromContext(ctx)
				return contexty.DeferredResult{Messages: []contexty.Message{
					contexty.TextMessage(contexty.RoleSystem, "locale="+vars["locale"]),
				}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("locale", "ru-RU"),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "locale=ru-RU", result.Payload.Memory[0].TextContent())
}

func TestAcceptance_ResolveVar_MapClone(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	var captured, fresh map[string]string
	engine := fixtureEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "vars",
			Segment: contexty.SegmentMemory,
			Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
				captured = contexty.CompileResolveVarFromContext(ctx)
				captured["mutated"] = "yes"
				fresh = contexty.CompileResolveVarFromContext(ctx)
				return contexty.DeferredResult{Messages: []contexty.Message{
					contexty.TextMessage(contexty.RoleSystem, "locale="+fresh["locale"]),
				}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("locale", "ru-RU"),
		},
	})
	// Assert.
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, "yes", captured["mutated"])
	require.NotNil(t, fresh)
	assert.Equal(t, "ru-RU", fresh["locale"])
	_, hasMutated := fresh["mutated"]
	assert.False(t, hasMutated)
	assert.Equal(t, "locale=ru-RU", result.Payload.Memory[0].TextContent())
}
