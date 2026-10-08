package main

import (
	"context"
	"fmt"
	"time"

	"github.com/skosovsky/prompty"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/integration/chat"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

const demoWindowBytes = 10000
const demoOutputBytes = 1000
const demoOverheadBytes = 500

func run() error {
	ctx := context.Background()
	now := time.Now()
	mapper := chat.NewMapper(
		prompty.ProfileIdentity{
			Provider:    "fixture",
			Endpoint:    "https://fixture.invalid",
			Model:       "offline",
			ModelFamily: "",
		},
	)
	store := contexty.NewMemoryConversationStateStore(
		contexty.WithMemoryStateCodec(
			contexty.ConversationCodec{
				Extensions:    mapper.Codec.Extensions,
				OpaqueProfile: mapper.Profile,
				Provenance:    nil,
			},
		),
	)
	messages, mandatory, err := mapper.Import(
		[]chat.Record{
			{
				ID:         "turn",
				SourceRefs: nil,
				Message: prompty.ChatMessage{
					Role:                    prompty.RoleUser,
					ProviderState:           nil,
					Annotations:             nil,
					MessageAnnotations:      nil,
					ContinuationUnavailable: false,
					CachePolicy:             nil,
					Provenance:              nil,
					Metadata:                nil,
					LayerKind:               "",
					Content: []prompty.ContentPart{
						prompty.TextPart{Text: "Keep core independent.", CachePolicy: nil},
					},
				},
			},
		},
		now,
	)
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
	prepared, err := mapper.Prepare(
		compiled.Payload.FlattenMessages(),
		mandatory,
		chat.ByteBudget{Window: demoWindowBytes, Output: demoOutputBytes, Overhead: demoOverheadBytes},
		now,
	)
	if err != nil {
		return err
	}
	if err = mapper.ValidatePrepared(prepared, compiled.Payload.FlattenMessages(), now); err != nil {
		return err
	}
	terminal := prompty.NewResponse(
		[]prompty.ContentPart{prompty.TextPart{Text: "Host owns integration and execution.", CachePolicy: nil}},
	)
	if err = mapper.CommitTerminal(
		ctx,
		store,
		"recipe",
		state.Version(),
		messages[0],
		terminal,
		true,
		now,
	); err != nil {
		return err
	}
	saved, err := store.LoadState(ctx, "recipe")
	if err != nil {
		return err
	}
	//nolint:forbidigo // This runnable recipe reports its offline result to the caller.
	fmt.Printf(
		"offline request: %d bytes; CAS state: %d; history: %d messages\n",
		prepared.Report.Bytes,
		saved.Version(),
		len(saved.Segment(contexty.SegmentHistory)),
	)
	return nil
}
