//go:build e2e

package chat

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/skosovsky/prompty"

	"github.com/skosovsky/contexty"
)

func TestE2EStatePersistenceAndGates(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	records := stateRecords(mapper)
	messages, mandatory := mustImport(t, mapper, records)
	codec := contexty.ConversationCodec{Extensions: mapper.Codec.Extensions, OpaqueProfile: mapper.Profile}
	// Act.
	wire, err := codec.Encode(contexty.EmptyState().WithSegment(contexty.SegmentHistory, messages))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := codec.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	output, err := mapper.Export(restored.Segment(contexty.SegmentHistory), mandatory, time.Unix(100, 0))
	// Assert.
	if err != nil || !reflect.DeepEqual(records, output) {
		t.Fatal("state changed", err)
	}
	if _, err = (contexty.ConversationCodec{OpaqueProfile: mapper.Profile}).Decode(wire); err == nil {
		t.Fatal("unknown codec accepted")
	}
	if _, err = mapper.Export(messages, mandatory, time.Unix(200, 0)); !errors.Is(err, prompty.ErrStateExpired) {
		t.Fatal("expired state accepted", err)
	}
	other := mapper
	other.Destination.Model = "other"
	if _, err = other.Export(messages, mandatory, time.Unix(100, 0)); !errors.Is(err, ErrStaleExecution) {
		t.Fatal("wrong destination accepted", err)
	}
	other.Profile.Revision = "other"
	if _, err = other.Export(messages, mandatory, time.Unix(100, 0)); !errors.Is(err, ErrStaleExecution) {
		t.Fatal("wrong profile accepted", err)
	}
	for _, modified := range [][]contexty.Message{{messages[1], messages[0], messages[2]}, messages[1:], messages[:2]} {
		if _, err = mapper.Export(modified, mandatory, time.Unix(100, 0)); err == nil {
			t.Fatal("invalid reorder/truncation accepted")
		}
	}
	after := append(slices.Clone(messages), contexty.TextMessage(contexty.RoleUser, "after binding"))
	after[3].ID = "tail"
	if _, err = mapper.Export(after, mandatory, time.Unix(100, 0)); err != nil {
		t.Fatal("outside scope invalidated", err)
	}
	dropped := restored.Segment(contexty.SegmentHistory)
	dropped[2].Extensions = dropped[2].Extensions[:1]
	if _, err = mapper.Export(dropped, mandatory, time.Unix(100, 0)); !errors.Is(err, prompty.ErrStateUnavailable) {
		t.Fatal("required state silently dropped", err)
	}
}

func TestE2ETerminalCommitAndCurrentTurn(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	ctx := context.Background()
	now := time.Unix(100, 0)
	store := contexty.NewMemoryConversationStateStore(
		contexty.WithMemoryStateCodec(
			contexty.ConversationCodec{Extensions: mapper.Codec.Extensions, OpaqueProfile: mapper.Profile},
		),
	)
	messages, _ := mustImport(t, mapper, []Record{plainRecord("turn", prompty.RoleUser, "question")})
	turn := messages[0]
	response := &prompty.Response{
		Outcome: prompty.OutcomeCompleted,
		Content: []prompty.ContentPart{prompty.TextPart{Text: "answer"}},
	}
	state, _ := store.LoadState(ctx, "conversation")
	// Act.
	compiled, err := mapper.CompileTurn(ctx, state, turn)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Payload.History) != 1 || compiled.Payload.History[0].ID != turn.ID {
		t.Fatal("turn duplicated")
	}
	if err = mapper.CommitTerminal(
		ctx,
		store,
		"conversation",
		0,
		turn,
		response,
		false,
		now,
	); !errors.Is(
		err,
		ErrIncomplete,
	) {
		t.Fatal(err)
	}
	incomplete := *response
	incomplete.Outcome = prompty.OutcomeIncomplete
	if err = mapper.CommitTerminal(
		ctx,
		store,
		"conversation",
		0,
		turn,
		&incomplete,
		true,
		now,
	); !errors.Is(
		err,
		ErrIncomplete,
	) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = mapper.CommitTerminal(
		canceled,
		store,
		"conversation",
		0,
		turn,
		response,
		true,
		now,
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatal(err)
	}
	state, _ = store.LoadState(ctx, "conversation")
	if state.Version() != 0 {
		t.Fatal("incomplete committed")
	}
	if err = mapper.CommitTerminal(ctx, store, "conversation", 0, turn, response, true, now); err != nil {
		t.Fatal(err)
	}
	if err = mapper.CommitTerminal(ctx, store, "conversation", 0, turn, response, true, now); err != nil {
		t.Fatal("retry duplicates", err)
	}
	state, _ = store.LoadState(ctx, "conversation")
	if len(state.Segment(contexty.SegmentHistory)) != 2 || state.Version() != 1 {
		t.Fatal("duplicate commit")
	}
	turn.ID = "next"
	if err = mapper.CommitTerminal(
		ctx,
		store,
		"conversation",
		0,
		turn,
		response,
		true,
		now,
	); !errors.Is(
		err,
		contexty.ErrConversationVersionConflict,
	) {
		t.Fatal("CAS lost write", err)
	}
	state, _ = store.LoadState(ctx, "conversation")
	if state.Version() != 1 {
		t.Fatal("CAS changed state")
	}
}
