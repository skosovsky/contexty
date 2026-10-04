package testutil

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/skosovsky/contexty"
)

// CheckCheckpointStore checks atomic writes and persistence parity in a fresh ID.
func CheckCheckpointStore(t *testing.T, store contexty.ConversationStateStore, id string) {
	t.Helper()
	ctx := context.Background()
	initial, err := store.LoadState(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	history := contexty.ConversationDelta{
		Operation: contexty.DeltaAppendMessages,
		Segment:   contexty.SegmentHistory,
		Messages: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "history"),
		},
		MessageIDs: nil,
		Artifact:   nil,
		ToolRound:  nil,
	}
	memory := history
	memory.Segment = contexty.SegmentMemory
	artifacts := checkpointArtifactMatrix()
	deltas := []contexty.ConversationDelta{history, memory}
	for i := range artifacts {
		deltas = append(
			deltas,
			contexty.ConversationDelta{Operation: contexty.DeltaUpsertArtifact, Segment: "", Messages: nil,
				MessageIDs: nil, Artifact: &artifacts[i], ToolRound: nil},
		)
	}
	// Arrange / Act: publish history, memory and the complete artifact policy matrix together.
	if err = store.CommitState(ctx, id, initial.Version(), deltas...); err != nil {
		t.Fatal(err)
	}
	committed, err := store.LoadState(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	working, err := contexty.ApplyDeltas(initial, deltas...)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := contexty.ProjectCheckpoint(
		working,
		contexty.DefaultJSONSerializer(),
		contexty.Descriptor{ID: "", Revision: ""},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Assert: one revision and the same explicit projection across every backend.
	if committed.Version() != initial.Version()+1 || !reflect.DeepEqual(committed.Artifacts(), expected.Artifacts()) ||
		len(committed.Segment(contexty.SegmentHistory)) != 1 || len(committed.Segment(contexty.SegmentMemory)) != 1 {
		t.Fatal("checkpoint batch or artifact projection differs")
	}
	var ids []string
	for _, artifact := range committed.Artifacts() {
		ids = append(ids, artifact.ID)
	}
	retained := []string{"matrix//", "matrix//store", "matrix/persistent/", "matrix/persistent/store",
		"matrix/bound_to_turn/store", "matrix/ephemeral/store"}
	slices.Sort(retained)
	if !slices.Equal(ids, retained) {
		t.Fatalf("unexpected retained artifact matrix: %v", ids)
	}
	checkCheckpointRollback(t, store, id, committed, history)
	// Arrange / Act / Assert: empty batch is rejected; an explicit no-op consumes one token.
	if err = store.CommitState(ctx, id, committed.Version()); !errors.Is(err, contexty.ErrEmptyCheckpointCommit) {
		t.Fatal(err)
	}
	if err = store.CommitState(ctx, id, committed.Version(), contexty.ConversationDelta{
		Operation: "", Segment: "", Messages: nil, MessageIDs: nil, Artifact: nil, ToolRound: nil}); err != nil {
		t.Fatal(err)
	}
	after, err := store.LoadState(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version() != committed.Version()+1 {
		t.Fatal("explicit no-op must consume exactly one revision")
	}
}

func checkpointArtifactMatrix() []contexty.ContextArtifact {
	var artifacts []contexty.ContextArtifact
	for _, lifecycle := range []contexty.ArtifactLifecycle{"", contexty.ArtifactLifecyclePersistent,
		contexty.ArtifactLifecycleTurnBound, contexty.ArtifactLifecycleEphemeral} {
		for _, persistence := range []contexty.ArtifactPersistencePolicy{contexty.ArtifactPersistenceDefault,
			contexty.ArtifactPersistenceStore, contexty.ArtifactPersistenceSkip} {
			artifact := contexty.NewMemoryBlock(
				"matrix/"+string(lifecycle)+"/"+string(persistence),
				contexty.TextPayload("fact"),
			).ContextArtifact
			artifact.Lifecycle, artifact.Persistence = lifecycle, persistence
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts
}

type missingCheckpointExtension struct{ Value string }

func (missingCheckpointExtension) ExtensionType() string                { return "testutil/missing-checkpoint-codec" }
func (e missingCheckpointExtension) CloneExtension() contexty.Extension { return e }

func checkCheckpointRollback(t *testing.T, store contexty.ConversationStateStore, id string,
	before contexty.ConversationState, history contexty.ConversationDelta) {
	t.Helper()
	artifact := contexty.NewMemoryBlock("codec-failure", contexty.TextPayload("fact")).ContextArtifact
	artifact.Extensions = []contexty.Extension{missingCheckpointExtension{Value: "cannot encode"}}
	for _, failure := range []contexty.ConversationDelta{
		{Operation: "invalid", Segment: "", Messages: nil, MessageIDs: nil, Artifact: nil, ToolRound: nil},
		{Operation: contexty.DeltaUpsertArtifact, Segment: "", Messages: nil, MessageIDs: nil, Artifact: &artifact, ToolRound: nil},
	} {
		// Arrange / Act: validation or codec failure occurs after an otherwise-valid append.
		err := store.CommitState(context.Background(), id, before.Version(), history, failure)
		if err == nil {
			t.Fatal("invalid checkpoint batch succeeded")
		}
		after, err := store.LoadState(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		// Assert: no partial publication or revision consumption.
		if !reflect.DeepEqual(before, after) {
			t.Fatal("failed batch changed checkpoint or revision")
		}
	}
}
