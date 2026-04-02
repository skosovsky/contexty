// Resilient store example: stdlib-only retries on [contexty.ErrUnavailable] around a
// [testutil.MemoryStore]. Run from repo root: go run ./examples/resilient_store
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

type resilientHistoryStore struct {
	base     contexty.HistoryStore
	attempts int
	failLeft int
}

func (s *resilientHistoryStore) withRetry(ctx context.Context, op func(context.Context) error) error {
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

func (s *resilientHistoryStore) Load(ctx context.Context, threadID string) (contexty.HistorySnapshot, error) {
	var snap contexty.HistorySnapshot
	err := s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		var e error
		snap, e = s.base.Load(ctx, threadID)
		return e
	})
	return snap, err
}

func (s *resilientHistoryStore) Append(
	ctx context.Context,
	threadID string,
	expectedVersion int64,
	msgs ...contexty.Message,
) error {
	return s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		return s.base.Append(ctx, threadID, expectedVersion, msgs...)
	})
}

func (s *resilientHistoryStore) Save(
	ctx context.Context,
	threadID string,
	expectedVersion int64,
	msgs []contexty.Message,
) error {
	return s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		return s.base.Save(ctx, threadID, expectedVersion, msgs)
	})
}

func (s *resilientHistoryStore) Clear(ctx context.Context, threadID string, expectedVersion int64) error {
	return s.withRetry(ctx, func(ctx context.Context) error {
		if s.failLeft > 0 {
			s.failLeft--
			return fmt.Errorf("simulated: %w", contexty.ErrUnavailable)
		}
		return s.base.Clear(ctx, threadID, expectedVersion)
	})
}

func main() {
	ctx := context.Background()
	base := testutil.NewMemoryStore()
	store := &resilientHistoryStore{
		base:     base,
		attempts: 0,
		failLeft: exampleSimulatedFails,
	}

	snap0, err := store.Load(ctx, "demo")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Load after %d attempts (%d simulated ErrUnavailable): version=%d msgs=%d\n",
		store.attempts, exampleSimulatedFails, snap0.Version, len(snap0.Messages))

	store.attempts = 0
	store.failLeft = 0
	err = store.Append(ctx, "demo", snap0.Version, contexty.TextMessage(contexty.RoleUser, "hello"))
	if err != nil {
		panic(err)
	}
	snap1, err := store.Load(ctx, "demo")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Append ok; reload version=%d content=%q\n", snap1.Version, snap1.Messages[0].Content[0].Text)
}
