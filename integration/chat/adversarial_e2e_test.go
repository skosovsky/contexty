//go:build e2e

package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/skosovsky/prompty"

	"github.com/skosovsky/contexty"
)

func TestE2EAuditSimultaneousCASPreservesWinningHistory(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	now := time.Unix(100, 0)
	ctx := context.Background()
	memory := contexty.NewMemoryConversationStateStore(
		contexty.WithMemoryStateCodec(
			contexty.ConversationCodec{Extensions: mapper.Codec.Extensions, OpaqueProfile: mapper.Profile},
		),
	)
	store := auditBarrierStore{
		ConversationStateStore: memory,
		loaded:                 make(chan struct{}, 2),
		release:                make(chan struct{}),
	}
	turns, _ := mustImport(
		t,
		mapper,
		[]Record{plainRecord("first", prompty.RoleUser, "first"), plainRecord("second", prompty.RoleUser, "second")},
	)
	response := &prompty.Response{
		Outcome: prompty.OutcomeCompleted,
		Content: []prompty.ContentPart{prompty.TextPart{Text: "answer"}},
	}
	results := make(chan error, 2)
	// Act.
	for _, turn := range turns {
		go func(turn contexty.Message) {
			results <- mapper.CommitTerminal(ctx, store, "race", 0, turn, response, true, now)
		}(turn)
	}
	<-store.loaded
	<-store.loaded
	close(store.release)
	first, second := <-results, <-results
	state, err := memory.LoadState(ctx, "race")
	// Assert.
	if err != nil || state.Version() != 1 || len(state.Segment(contexty.SegmentHistory)) != 2 {
		t.Fatal("lost or duplicate history", state.Version(), err)
	}
	firstWon := first == nil && errors.Is(second, contexty.ErrConversationVersionConflict)
	secondWon := second == nil && errors.Is(first, contexty.ErrConversationVersionConflict)
	if !firstWon && !secondWon {
		t.Fatal("CAS did not arbitrate", first, second)
	}
}

type auditBarrierStore struct {
	contexty.ConversationStateStore

	loaded  chan struct{}
	release chan struct{}
}

func (s auditBarrierStore) LoadState(ctx context.Context, id string) (contexty.ConversationState, error) {
	state, err := s.ConversationStateStore.LoadState(ctx, id)
	s.loaded <- struct{}{}
	<-s.release
	return state, err
}
