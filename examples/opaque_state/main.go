// Offline host adapter fixtures: explicit state codecs and context dependencies.
// Run: go run ./examples/opaque_state.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/skosovsky/contexty"
)

func hostProfile() contexty.Descriptor {
	// This fixture profile has no portability claim about any model or provider.
	return contexty.Descriptor{ID: "host.fixture/protocol", Revision: "1"}
}

func hostCodec() contexty.JSONSerializer {
	registry := contexty.NewExtensionRegistry()
	registerPayloads(registry)
	return contexty.JSONSerializer{Provenance: contexty.DefaultProvenanceRegistry(), Extensions: registry}
}

func fixtureMessages(codec contexty.JSONSerializer) ([]contexty.Message, error) {
	input := contexty.TextMessage(contexty.RoleUser, "Bound input.")
	input.ID = "input"
	ref, err := contexty.MessageContentRef(input, codec)
	if err != nil {
		return nil, err
	}
	carrier := contexty.TextMessage(contexty.RoleAssistant, "Visible answer.")
	carrier.ID = "answer"
	binding := contexty.OpaqueBinding{
		Profile: hostProfile(), Required: []contexty.ContentRef{ref},
		Prefix: []contexty.ContentRef{ref}, Boundary: input.ID,
	}
	carrier.Extensions = []contexty.Extension{
		contexty.OpaqueState{
			ID: "signature-1", Codec: payloadCodec(signatureType),
			Payload:   signatureFixture{Signature: []byte{0, 1, 255}},
			Placement: contexty.OpaquePlacement{AfterPart: 0}, Binding: binding,
		},
		contexty.OpaqueState{
			ID: "compaction-1", Codec: payloadCodec(compactionType),
			Payload:   opaqueCompactionFixture{ItemID: "external-item-1", Opaque: []byte{255, 0, 2}},
			Placement: contexty.OpaquePlacement{AfterPart: 0}, Binding: binding,
		},
	}
	return []contexty.Message{input, carrier}, nil
}

func fixtureEngine(codec contexty.JSONSerializer, mode contexty.OpaqueInvalidationMode) *contexty.Engine {
	stages := make(map[string]contexty.Descriptor)
	for _, name := range []string{"source", "project", "opaque-state"} {
		stages[name] = contexty.Descriptor{ID: "fixture/" + name, Revision: "1"}
	}
	//nolint:exhaustruct_v5 // The recipe enables tracing and opaque policy only.
	trace := contexty.TraceProfile{
		Encoding: contexty.Descriptor{ID: "host.fixture/semantic-json", Revision: "1"},
		Codec:    codec, Stages: stages, Codecs: fixtureCodecBindings(),
		Labels: contexty.LabelProjection{Registry: codec.Extensions},
	}
	return contexty.NewEngine(
		contexty.WithTraceProfile(trace),
		contexty.WithOpaqueStatePolicy(contexty.OpaqueStatePolicy{
			Identity: contexty.Descriptor{ID: "host.fixture/state-lifecycle", Revision: "1"},
			Profile:  hostProfile(), Invalidated: mode,
		}),
	)
}

func fixtureCodecBindings() []contexty.CodecBinding {
	var bindings []contexty.CodecBinding
	for _, kind := range []contexty.CodecRegistryKind{contexty.CodecExtension, contexty.CodecLabel} {
		for _, typeID := range []string{contexty.OpaqueStateExtensionType, signatureType, compactionType} {
			bindings = append(bindings, contexty.CodecBinding{
				Kind: kind, Type: typeID, Descriptor: payloadCodec(typeID),
			})
		}
	}
	return bindings
}

func compileFixture(
	ctx context.Context,
	engine *contexty.Engine,
	messages []contexty.Message,
) (contexty.CompileResult, error) {
	//nolint:exhaustruct_v5 // No artifact, budget, resource or current-turn features are enabled.
	return engine.CompileSnapshot(ctx, contexty.CompileRequest{CompilationID: "opaque-fixture", History: messages})
}

func checkpointRoundTrip(
	ctx context.Context,
	codec contexty.JSONSerializer,
	messages []contexty.Message,
) ([]contexty.Message, error) {
	store := contexty.NewMemoryConversationStateStore(contexty.WithMemoryStateCodec(contexty.ConversationCodec{
		Provenance: codec.Provenance, Extensions: codec.Extensions, OpaqueProfile: hostProfile()}))
	//nolint:exhaustruct_v5 // One complete segment replacement is the host checkpoint decision.
	if err := store.CommitState(ctx, "fixture", 0, contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment, Segment: contexty.SegmentHistory, Messages: messages,
	}); err != nil {
		return nil, err
	}
	loaded, err := store.LoadState(ctx, "fixture")
	if err != nil {
		return nil, err
	}
	return loaded.Segment(contexty.SegmentHistory), nil
}

func run(ctx context.Context) error {
	codec := hostCodec()
	messages, err := fixtureMessages(codec)
	if err != nil {
		return err
	}
	loaded, err := checkpointRoundTrip(ctx, codec, messages)
	if err != nil {
		return err
	}
	if _, err = compileFixture(ctx, fixtureEngine(codec, contexty.OpaqueFailClosed), loaded); err != nil {
		return err
	}
	changed := contexty.TextMessage(contexty.RoleUser, "Changed bound input.")
	changed.ID = loaded[0].ID
	loaded[0] = changed
	_, rejected := compileFixture(ctx, fixtureEngine(codec, contexty.OpaqueFailClosed), loaded)
	if !errors.Is(rejected, contexty.ErrOpaqueStateInvalidated) {
		return fmt.Errorf("expected fail-closed dependency error, got %w", rejected)
	}
	dropped, err := compileFixture(ctx, fixtureEngine(codec, contexty.OpaqueDropInvalid), loaded)
	if err != nil {
		return err
	}
	fmt.Printf("checkpoint preserved two typed states; changed dependency rejected=%t; explicit drop remaining=%d\n",
		errors.Is(rejected, contexty.ErrOpaqueStateInvalidated), len(dropped.Payload.History[1].Extensions))
	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
}
