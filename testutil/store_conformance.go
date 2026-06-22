package testutil

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/skosovsky/contexty"
)

// CheckStateStore runs the OCC lifecycle contract against a fresh namespace.
// A cleared state keeps its concurrency identity without retaining its payload.
func CheckStateStore(t *testing.T, store contexty.ConversationStateStore, id string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	conflict := func(err error) {
		t.Helper()
		if !errors.Is(err, contexty.ErrConversationVersionConflict) {
			t.Fatalf("expected OCC conflict, got %v", err)
		}
	}
	ctx := context.Background()
	old := contexty.ConversationDelta{Operation: contexty.DeltaReplaceSegment, Segment: contexty.SegmentHistory,
		Messages:   []contexty.Message{contexty.TextMessage(contexty.RoleUser, "private old data")},
		MessageIDs: nil, Artifact: nil, ToolRound: nil}
	// Arrange: a writer holds a token belonging to the deleted state.
	initial, err := store.LoadState(ctx, id)
	check(err)
	check(store.ApplyDelta(ctx, id, initial.Version(), old))
	stale, err := store.LoadState(ctx, id)
	check(err)
	// Act: clear and recreate using the fresh empty-state identity.
	check(store.ClearState(ctx, id, stale.Version()))
	empty, err := store.LoadState(ctx, id)
	check(err)
	if empty.Version() <= stale.Version() || len(empty.AllSegments()) != 0 || len(empty.Artifacts()) != 0 {
		t.Fatal("clear must erase content and advance the OCC identity")
	}
	fresh := old
	fresh.Messages = []contexty.Message{contexty.TextMessage(contexty.RoleUser, "fresh")}
	check(store.ApplyDelta(ctx, id, empty.Version(), fresh))
	// Assert: both a stale replacement and clear must conflict.
	conflict(store.ApplyDelta(ctx, id, stale.Version(), old))
	old.Operation = contexty.DeltaAppendMessages
	conflict(store.ApplyDelta(ctx, id, stale.Version(), old))
	conflict(store.ClearState(ctx, id, stale.Version()))
	current, err := store.LoadState(ctx, id)
	check(err)
	if got := current.Segment(contexty.SegmentHistory); len(got) != 1 || got[0].TextContent() != "fresh" {
		t.Fatal("stale writer changed fresh content")
	}

	// Arrange: concurrent writers share one valid token.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { errs <- store.ApplyDelta(ctx, id, current.Version(), fresh) })
	}
	// Act.
	wg.Wait()
	close(errs)
	// Assert: exactly one commit; the other must conflict.
	commits := 0
	for writeErr := range errs {
		if writeErr == nil {
			commits++
		} else {
			conflict(writeErr)
		}
	}
	if commits != 1 {
		t.Fatalf("expected one commit, got %d", commits)
	}
}
