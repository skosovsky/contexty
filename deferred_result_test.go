package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestDeferred_ResultCancellation(t *testing.T) {
	// Arrange: a callback returns partial content and a host error after cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hooks := 0
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(contexty.DeferredBlock{
		Name: "selected", Resolve: func(context.Context) (contexty.DeferredResult, error) {
			cancel()
			return contexty.DeferredResult{
				Messages: []contexty.Message{fixtureRollingText("partial", "private")},
			}, contexty.ErrResourceDenied
		},
	}), contexty.WithTransformHooks(fixtureDeferredHook(func(_ context.Context, snapshot contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
		hooks++
		return snapshot, nil
	})))
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{})
	// Assert: cancellation dominates callback returns, with no content or later callbacks.
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	require.Zero(t, hooks)
}

func TestDeferred_ResultIsolation(t *testing.T) {
	// Arrange: a host owns a mutable callback slice and mutates it in a later hook.
	message := fixtureRollingText("deferred", "safe")
	message.SourceRefs = []contexty.SourceRef{{ID: "source"}}
	messages := []contexty.Message{message}
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(contexty.DeferredBlock{
		Name: "selected", Resolve: func(context.Context) (contexty.DeferredResult, error) {
			return contexty.DeferredResult{Messages: messages}, nil
		},
	}), contexty.WithTransformHooks(fixtureDeferredHook(func(_ context.Context, snapshot contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
		messages[0].Parts[0] = contexty.TextPart{Text: "host-mutated"}
		messages[0].SourceRefs[0].ID = "host-mutated"
		return snapshot, nil
	})))
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
	// Assert: compiled typed content is independent from callback-owned containers.
	require.NoError(t, err)
	require.Equal(t, "safe", result.Payload.Memory[0].TextContent())
	require.Equal(t, "source", result.Payload.Memory[0].SourceRefs[0].ID)
	require.Empty(t, result.Source.Memory)
}
