// Resilient store example: stdlib-only retries on [contexty.ErrUnavailable] around
// [testutil.MemoryConversationStore]. Run: go run ./examples/resilient_store
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

type resilientConversationStore struct {
	base     contexty.ConversationStore
	attempts int
	failLeft int
}

func (s *resilientConversationStore) withRetry(ctx context.Context, op func(context.Context) error) error {
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

func (s *resilientConversationStore) Load(
	ctx context.Context,
	conversationID string,
) (contexty.ConversationSnapshot, error) {
	var snap contexty.ConversationSnapshot
	err := s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		var e error
		snap, e = s.base.Load(ctx, conversationID)
		return e
	})
	return snap, err
}

func (s *resilientConversationStore) UpdateSegment(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	name contexty.SegmentName,
	msgs []contexty.Message,
) error {
	return s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		return s.base.UpdateSegment(ctx, conversationID, expectedVersion, name, msgs)
	})
}

func (s *resilientConversationStore) AppendSegment(
	ctx context.Context,
	conversationID string,
	expectedVersion int64,
	name contexty.SegmentName,
	msgs ...contexty.Message,
) error {
	return s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		return s.base.AppendSegment(ctx, conversationID, expectedVersion, name, msgs...)
	})
}

func (s *resilientConversationStore) Clear(ctx context.Context, conversationID string, expectedVersion int64) error {
	return s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		return s.base.Clear(ctx, conversationID, expectedVersion)
	})
}

func main() {
	ctx := context.Background()
	base := testutil.NewMemoryConversationStore()
	store := &resilientConversationStore{
		base:     base,
		attempts: 0,
		failLeft: exampleSimulatedFails,
	}

	snap0, err := store.Load(ctx, "demo")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Load after %d attempts (%d simulated ErrUnavailable): version=%d\n",
		store.attempts, exampleSimulatedFails, snap0.Version())

	store.attempts = 0
	store.failLeft = 0
	err = store.AppendSegment(ctx, "demo", snap0.Version(), contexty.SegmentHistory,
		contexty.TextMessage(contexty.RoleUser, "hello"))
	if err != nil {
		panic(err)
	}
	snap1, err := store.Load(ctx, "demo")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Append ok; reload version=%d content=%q\n",
		snap1.Version(), snap1.Segment(contexty.SegmentHistory)[0].TextContent())
}
