// Host reconciliation after an acknowledged read or an ambiguous checkpoint write.
// Run: go run ./examples/resilient_store
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/skosovsky/contexty"
)

var ErrUnknownOutcome = errors.New("host: checkpoint write outcome unknown")

// lostAcknowledgementStore models a backend commit whose response never arrived.
type lostAcknowledgementStore struct {
	contexty.ConversationStateStore

	loseNext bool
}

func (s *lostAcknowledgementStore) CommitState(
	ctx context.Context,
	id string,
	version int64,
	deltas ...contexty.ConversationDelta,
) error {
	if err := s.ConversationStateStore.CommitState(ctx, id, version, deltas...); err != nil {
		return err
	}
	if s.loseNext {
		s.loseNext = false
		return fmt.Errorf("response lost after commit: %w", contexty.ErrUnavailable)
	}
	return nil
}

// reconcileOwnedCheckpoint requires a host-owned unique witness in the expected
// content. Exact content plus the next revision is sufficient in this host model;
// later writes or a competing checkpoint remain unknown. No mutation is retried.
func reconcileOwnedCheckpoint(ctx context.Context, store contexty.ConversationStateStore, id string,
	before contexty.ConversationState, deltas ...contexty.ConversationDelta) error {
	working, err := contexty.ApplyDeltas(before, deltas...)
	if err != nil {
		return err
	}
	expected, err := contexty.ProjectCheckpoint(
		working,
		contexty.DefaultJSONSerializer(),
		contexty.Descriptor{ID: "", Revision: ""},
	)
	if err != nil {
		return err
	}
	if !hasNewCheckpointWitness(before, expected) {
		return ErrUnknownOutcome
	}
	version, err := contexty.NextConversationVersion(before.Version())
	if err != nil {
		return err
	}
	actual, err := store.LoadState(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: reload: %w", ErrUnknownOutcome, err)
	}
	codec := contexty.ConversationCodec{
		Provenance:    contexty.DefaultProvenanceRegistry(),
		Extensions:    nil,
		OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""},
	}
	expectedWire, err := codec.Encode(expected.WithVersion(version))
	if err != nil {
		return err
	}
	actualWire, err := codec.Encode(actual)
	if err != nil {
		return err
	}
	if !bytes.Equal(expectedWire, actualWire) {
		return ErrUnknownOutcome
	}
	return nil
}

func hasNewCheckpointWitness(before, expected contexty.ConversationState) bool {
	known := make(map[string]bool)
	for _, messages := range before.AllSegments() {
		for _, message := range messages {
			known[message.ID] = true
		}
	}
	for _, artifact := range before.Artifacts() {
		known[artifact.ID] = true
	}
	for _, messages := range expected.AllSegments() {
		for _, message := range messages {
			if message.ID != "" && !known[message.ID] {
				return true
			}
		}
	}
	for _, artifact := range expected.Artifacts() {
		if artifact.ID != "" && !known[artifact.ID] {
			return true
		}
	}
	return false
}

func main() {
	ctx := context.Background()
	base := contexty.NewMemoryConversationStateStore()
	store := &lostAcknowledgementStore{ConversationStateStore: base, loseNext: true}
	before, err := store.LoadState(ctx, "demo")
	if err != nil {
		panic(err)
	}
	// The host controls this unique application message ID; it is not a library receipt.
	message := contexty.TextMessage(contexty.RoleUser, "hello")
	message.ID = "host-write-1/message"
	delta := contexty.ConversationDelta{Operation: contexty.DeltaAppendMessages, Segment: contexty.SegmentHistory,
		Messages: []contexty.Message{message}, MessageIDs: nil, Artifact: nil, ToolRound: nil}
	err = store.CommitState(ctx, "demo", before.Version(), delta)
	if !errors.Is(err, contexty.ErrUnavailable) {
		panic("expected an ambiguous response")
	}
	// Reusing the original token cannot duplicate the mutation, but conflict proves no ownership.
	retryErr := store.CommitState(ctx, "demo", before.Version(), delta)
	fmt.Printf(
		"Lost response; retry conflicts=%t (not evidence of success)\n",
		errors.Is(retryErr, contexty.ErrConversationVersionConflict),
	)
	if err = reconcileOwnedCheckpoint(ctx, base, "demo", before, delta); err != nil {
		panic(err)
	}
	after, err := base.LoadState(ctx, "demo")
	if err != nil {
		panic(err)
	}
	fmt.Printf(
		"Host reconciled owned checkpoint: version=%d messages=%d\n",
		after.Version(),
		len(after.Segment(contexty.SegmentHistory)),
	)
}
