// Resilient store example: stdlib-only retries on [contexty.ErrUnavailable]
// around a [contexty.ConversationStateStore]. Run: go run ./examples/resilient_store
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/testutil"
)

const (
	exampleMaxRetries       = 5
	exampleInitialBackoffMs = 10
	exampleSimulatedFails   = 2
)

type resilientConversationStateStore struct {
	base     contexty.ConversationStateStore
	attempts int
	failLeft int
}

func (s *resilientConversationStateStore) withRetry(ctx context.Context, op func(context.Context) error) error {
	backoff := exampleInitialBackoffMs * time.Millisecond
	var last error
	for attempt := 0; attempt <= exampleMaxRetries; attempt++ {
		s.attempts++
		err := op(ctx)
		if err == nil {
			return nil
		}
		if errors.Is(err, contexty.ErrUnavailable) && attempt < exampleMaxRetries {
			last = err
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}
		return err
	}
	return last
}

func (s *resilientConversationStateStore) LoadState(
	ctx context.Context,
	conversationID string,
) (contexty.ConversationState, error) {
	var state contexty.ConversationState
	err := s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		var e error
		state, e = s.base.LoadState(ctx, conversationID)
		return e
	})
	return state, err
}

func (s *resilientConversationStateStore) ApplyDelta(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	delta contexty.ConversationDelta,
) error {
	return s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		return s.base.ApplyDelta(ctx, conversationID, expectedVersion, delta)
	})
}

func (s *resilientConversationStateStore) ClearState(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
) error {
	return s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		return s.base.ClearState(ctx, conversationID, expectedVersion)
	})
}

func main() {
	ctx := context.Background()
	base := testutil.NewMemoryConversationStateStore()
	store := &resilientConversationStateStore{
		base:     base,
		attempts: 0,
		failLeft: exampleSimulatedFails,
	}

	state0, err := store.LoadState(ctx, "demo")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Load after %d attempts (%d simulated ErrUnavailable): version=%d\n",
		store.attempts, exampleSimulatedFails, state0.Version())

	store.attempts = 0
	store.failLeft = 0
	//nolint:exhaustruct_v5 // zero-value fields omitted in example
	err = store.ApplyDelta(ctx, "demo", state0.Version(), contexty.ConversationDelta{
		Operation: contexty.DeltaAppendMessages,
		Segment:   contexty.SegmentHistory,
		Messages:  []contexty.Message{contexty.TextMessage(contexty.RoleUser, "hello")},
	})
	if err != nil {
		panic(err)
	}
	state1, err := store.LoadState(ctx, "demo")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Append ok; reload version=%d content=%q\n",
		state1.Version(), state1.Segment(contexty.SegmentHistory)[0].TextContent())
}
