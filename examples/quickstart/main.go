// Offline durable compile and explicit checkpoint publication.
//
//nolint:exhaustruct_v5,mnd // Keep optional zero fields out of the onboarding example.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/skosovsky/contexty"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	store := contexty.NewMemoryConversationStateStore()
	initial, err := store.LoadState(ctx, "chat-1")
	if err != nil {
		return err
	}
	system := contexty.TextMessage(contexty.RoleSystem, "Answer briefly.")
	system.ID = "chat-1/system/v1" // Host identity for static content.
	if err = store.CommitState(ctx, "chat-1", initial.Version(), contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment, Segment: contexty.SegmentSystem, Messages: []contexty.Message{system},
	}); err != nil {
		return err
	}
	// Explicit structural approximation; a production host supplies its tokenizer.
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1000)},
		&contexty.FixedEstimator{TokensPerMessage: 4, TokensPerContentPart: 8},
	)
	engine := contexty.NewEngine(
		contexty.WithStateStore(store),
		contexty.WithConversationID("chat-1"),
		contexty.WithBudgetPipeline(pipe),
	)
	turn := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "My private account is 123. Help me."))
	turn = turn.WithPromptSafe(contexty.TextMessage(contexty.RoleUser, "Help with my account."))
	result, err := engine.Compile(ctx, contexty.CompileRequest{
		TurnID:                 "chat-1/turn-1",
		CurrentTurn:            &turn,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("chat-1"),
		RequireDurableIdentity: true,
	})
	if err != nil {
		return err
	}
	checkpoint, err := result.DerivePersistenceState(contexty.DefaultJSONSerializer(), contexty.Descriptor{})
	if err != nil {
		return err
	}
	// Compilation proposes context; the host explicitly publishes raw checkpoint data.
	if err = store.CommitState(ctx, "chat-1", result.NormalizedSnapshot.Version(), contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment,
		Segment:   contexty.SegmentHistory,
		Messages:  checkpoint.Segment(contexty.SegmentHistory),
	}); err != nil {
		return err
	}
	fmt.Println("prompt:", result.Payload.History[0].TextContent())
	fmt.Println("checkpoint:", checkpoint.Segment(contexty.SegmentHistory)[0].TextContent())
	return nil
}
