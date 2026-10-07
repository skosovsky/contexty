package main

import (
	"context"
	"fmt"
	"time"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/integration/chat"
	"github.com/skosovsky/prompty"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	ctx := context.Background()
	now := time.Now()
	mapper := chat.NewMapper(prompty.ProfileIdentity{Provider: "fixture", Endpoint: "https://fixture.invalid", Model: "offline"})
	store := contexty.NewMemoryConversationStateStore(contexty.WithMemoryStateCodec(contexty.ConversationCodec{Extensions: mapper.Codec.Extensions, OpaqueProfile: mapper.Profile}))
	messages, mandatory, err := mapper.Import([]chat.Record{{ID: "turn", Message: prompty.ChatMessage{Role: prompty.RoleUser, Content: []prompty.ContentPart{prompty.TextPart{Text: "Keep core independent."}}}}}, now)
	if err != nil {
		return err
	}
	state, err := store.LoadState(ctx, "recipe")
	if err != nil {
		return err
	}
	compiled, err := mapper.CompileTurn(ctx, state, messages[0])
	if err != nil {
		return err
	}
	prepared, err := mapper.Prepare(compiled.Payload.FlattenMessages(), mandatory, chat.ByteBudget{Window: 10000, Output: 1000, Overhead: 500}, now)
	if err != nil {
		return err
	}
	if err = mapper.ValidatePrepared(prepared, compiled.Payload.FlattenMessages(), now); err != nil {
		return err
	}
	terminal := &prompty.Response{Outcome: prompty.OutcomeCompleted, Content: []prompty.ContentPart{prompty.TextPart{Text: "Host owns integration and execution."}}}
	if err = mapper.CommitTerminal(ctx, store, "recipe", state.Version(), messages[0], terminal, true, now); err != nil {
		return err
	}
	saved, err := store.LoadState(ctx, "recipe")
	if err != nil {
		return err
	}
	fmt.Printf("offline request: %d bytes; CAS state: %d; history: %d messages\n", prepared.Report.Bytes, saved.Version(), len(saved.Segment(contexty.SegmentHistory)))
	return nil
}
