package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/prompty"
)

func fixtureMapper() Mapper {
	return NewMapper(prompty.ProfileIdentity{Provider: "fixture", Endpoint: "https://fixture.invalid", Model: "offline"})
}
func mustImport(t *testing.T, mapper Mapper, records []Record) ([]contexty.Message, []string) {
	t.Helper()
	messages, mandatory, err := mapper.Import(records, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	return messages, mandatory
}
func plainRecord(id string, role prompty.Role, text string) Record {
	return Record{ID: id, SourceRefs: []contexty.SourceRef{{Namespace: "host", ID: id}}, Message: prompty.ChatMessage{Role: role, Content: []prompty.ContentPart{prompty.TextPart{Text: text}}}}
}
func stateRecords(mapper Mapper) []Record {
	expiry := time.Unix(200, 0).UTC()
	carrier := plainRecord("carrier", prompty.RoleAssistant, "visible")
	carrier.Message.ProviderState = []prompty.PartState{{PartIndex: 0, Envelope: prompty.ProviderEnvelope{Format: prompty.ProviderStateFormat,
		Scope: mapper.Destination, Codec: "fixture-bytes", Payload: []byte{0, 255, 7}, Required: true, ExpiresAt: &expiry}}}
	carrier.Message.Annotations = []prompty.PartAnnotation{{PartIndex: 0, Kind: "citation", Value: json.RawMessage(`{"n":9007199254740993}`)}}
	carrier.Message.MessageAnnotations = []prompty.MessageAnnotation{{Kind: "scope", Value: json.RawMessage(`{"id":"external"}`), Scope: &mapper.Destination, Required: true}}
	return []Record{plainRecord("instructions", prompty.RoleDeveloper, "rules"), plainRecord("question", prompty.RoleUser, "question"), carrier}
}

func TestRoundtrip(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	records := []Record{plainRecord("d", prompty.RoleDeveloper, "rules"), plainRecord("u", prompty.RoleUser, "hello"),
		{ID: "call", Message: prompty.ChatMessage{Role: prompty.RoleAssistant, Content: []prompty.ContentPart{prompty.ToolCallPart{ID: "c", Name: "count", Args: "{ \"n\" : 9007199254740993 }"}}}},
		{ID: "result", Message: prompty.ChatMessage{Role: prompty.RoleTool, Content: []prompty.ContentPart{prompty.ToolResultPart{ToolCallID: "c", Name: "count", Content: []prompty.ContentPart{prompty.TextPart{Text: "failed"}}, IsError: true}}}},
		{ID: "media", Message: prompty.ChatMessage{Role: prompty.RoleUser, Content: []prompty.ContentPart{prompty.MediaPart{MediaType: "image", MIMEType: "image/png", URL: "https://fixture.invalid/i"}, prompty.MediaPart{MediaType: "audio", MIMEType: "audio/wav", Data: []byte{0, 1, 255}}}}},
	}
	records[1].Message.Metadata = json.RawMessage(`{"integer":9007199254740993}`)
	records[1].Message.Provenance = &prompty.MessageProvenance{ManifestID: "host", LayerID: "dialog"}
	messages, mandatory := mustImport(t, mapper, records)
	codec := contexty.ConversationCodec{Extensions: mapper.Codec.Extensions, OpaqueProfile: mapper.Profile}
	// Act.
	wire, err := codec.Encode(contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, messages))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := codec.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	result, err := mapper.Export(restored.Segment(contexty.SegmentHistory), mandatory, time.Unix(100, 0))
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, result) {
		t.Fatalf("roundtrip mismatch\n%#v\n%#v", records, result)
	}
	args := result[2].Message.Content[0].(prompty.ToolCallPart).Args
	if args != "{ \"n\" : 9007199254740993 }" {
		t.Fatalf("exact args lost: %q", args)
	}
}

func TestUnsupported(t *testing.T) {
	mapper := fixtureMapper()
	for _, part := range []prompty.ContentPart{prompty.ReasoningPart{Text: "reasoning"},
		prompty.ToolResultPart{ToolCallID: "call", Content: []prompty.ContentPart{prompty.TextPart{Text: "caption"}, prompty.MediaPart{MediaType: "image", MIMEType: "image/png", Data: []byte{1}}}},
		prompty.ToolCallPart{ID: "stream", ArgsChunk: "{"}, prompty.TextPart{Text: "cached", CachePolicy: &prompty.CachePolicy{}},
	} {
		// Arrange.
		record := Record{ID: "unsupported", Message: prompty.ChatMessage{Role: prompty.RoleAssistant, Content: []prompty.ContentPart{part}}}
		// Act.
		messages, _, err := mapper.Import([]Record{record}, time.Unix(100, 0))
		// Assert.
		if err == nil || messages != nil {
			t.Fatalf("unsupported accepted: %T", part)
		}
	}
	// Arrange.
	messages := []contexty.Message{{ID: "image", Role: contexty.RoleUser, Parts: []contexty.ContentPart{contexty.ImagePart{URL: "https://fixture.invalid", Detail: "high"}}}}
	// Act.
	records, err := mapper.Export(messages, nil, time.Unix(100, 0))
	// Assert.
	if !errors.Is(err, ErrUnsupported) || records != nil {
		t.Fatal("detail silently lost", err)
	}
}

func TestStatePersistenceAndGates(t *testing.T) {
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

func TestStateExpiryPreservesInstantAcrossTimeZones(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	records := stateRecords(mapper)
	expiry := time.Unix(200, 0).In(time.FixedZone("host-zone", 7*60*60))
	records[2].Message.ProviderState[0].Envelope.ExpiresAt = &expiry
	messages, mandatory := mustImport(t, mapper, records)
	codec := contexty.ConversationCodec{Extensions: mapper.Codec.Extensions, OpaqueProfile: mapper.Profile}
	// Act.
	wire, err := codec.Encode(contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, messages))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := codec.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	output, err := mapper.Export(restored.Segment(contexty.SegmentHistory), mandatory, time.Unix(100, 0))
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	actual := output[2].Message.ProviderState[0].Envelope.ExpiresAt
	if actual == nil || !actual.Equal(expiry) {
		t.Fatalf("expiry instant changed: got %v, want %v", actual, expiry)
	}
	// Location names are not part of the JSON timestamp contract.
	records[2].Message.ProviderState[0].Envelope.ExpiresAt = actual
	if !reflect.DeepEqual(records, output) {
		t.Fatal("continuation fields changed")
	}
}

func TestSnapshotOwnsInputs(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	records := stateRecords(mapper)
	messages, _ := mustImport(t, mapper, records)
	snapshot := contexty.EmptyState().WithSegment(contexty.SegmentHistory, messages)
	before, _ := mapper.Codec.Marshal(snapshot.Segment(contexty.SegmentHistory)[2])
	// Act.
	records[2].Message.ProviderState[0].Envelope.Payload[0] = 42
	records[2].Message.Annotations[0].Value[0] = 'x'
	messages[2].Extensions[0].(Metadata).Wire[0] = 'x'
	messages[0].SourceRefs[0].ID = "mutated"
	// Assert.
	after, err := mapper.Codec.Marshal(snapshot.Segment(contexty.SegmentHistory)[2])
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("snapshot aliased", err)
	}
}

func TestPreparedRequestRejectsStaleEvidence(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	messages, mandatory := mustImport(t, mapper, stateRecords(mapper))
	now := time.Unix(100, 0)
	budget := ByteBudget{Window: 10000, Output: 1000, Overhead: 500}
	// Act.
	prepared, err := mapper.Prepare(messages, mandatory, budget, now)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Report.EffectiveLimit != 8500 {
		t.Fatal("reservations applied twice")
	}
	if err = mapper.ValidatePrepared(prepared, messages, now); err != nil {
		t.Fatal(err)
	}
	prepared.Execution.Messages[0], prepared.Execution.Messages[1] = prepared.Execution.Messages[1], prepared.Execution.Messages[0]
	if err = mapper.ValidatePrepared(prepared, messages, now); !errors.Is(err, ErrStaleExecution) {
		t.Fatal("native reorder accepted", err)
	}
	prepared, _ = mapper.Prepare(messages, mandatory, budget, now)
	prepared.Report.Bytes++
	if err = mapper.ValidatePrepared(prepared, messages, now); !errors.Is(err, ErrStaleExecution) {
		t.Fatal("stale report accepted", err)
	}
	if _, err = mapper.Prepare(messages, mandatory, ByteBudget{Window: 1}, now); !errors.Is(err, ErrBudget) {
		t.Fatal("overflow accepted", err)
	}
	if _, err = mapper.Prepare(messages, mandatory, ByteBudget{Window: 100, Output: 80, Overhead: 30}, now); !errors.Is(err, ErrBudget) {
		t.Fatal("invalid reservations accepted", err)
	}
}

func TestTerminalCommitAndCurrentTurn(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	ctx := context.Background()
	now := time.Unix(100, 0)
	store := contexty.NewMemoryConversationStateStore(contexty.WithMemoryStateCodec(contexty.ConversationCodec{Extensions: mapper.Codec.Extensions, OpaqueProfile: mapper.Profile}))
	messages, _ := mustImport(t, mapper, []Record{plainRecord("turn", prompty.RoleUser, "question")})
	turn := messages[0]
	response := &prompty.Response{Outcome: prompty.OutcomeCompleted, Content: []prompty.ContentPart{prompty.TextPart{Text: "answer"}}}
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
	if err = mapper.CommitTerminal(ctx, store, "conversation", 0, turn, response, false, now); !errors.Is(err, ErrIncomplete) {
		t.Fatal(err)
	}
	incomplete := *response
	incomplete.Outcome = prompty.OutcomeIncomplete
	if err = mapper.CommitTerminal(ctx, store, "conversation", 0, turn, &incomplete, true, now); !errors.Is(err, ErrIncomplete) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = mapper.CommitTerminal(canceled, store, "conversation", 0, turn, response, true, now); !errors.Is(err, context.Canceled) {
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
	if err = mapper.CommitTerminal(ctx, store, "conversation", 0, turn, response, true, now); !errors.Is(err, contexty.ErrConversationVersionConflict) {
		t.Fatal("CAS lost write", err)
	}
	state, _ = store.LoadState(ctx, "conversation")
	if state.Version() != 1 {
		t.Fatal("CAS changed state")
	}
}

func TestSnapshotOwnsJSONBinaryAndResult(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	now := time.Unix(100, 0)
	records := []Record{{ID: "call", Message: prompty.ChatMessage{Role: prompty.RoleAssistant, Content: []prompty.ContentPart{prompty.ToolCallPart{ID: "c", Name: "tool", Args: `{"n":9007199254740993}`}}}},
		{ID: "result", Message: prompty.ChatMessage{Role: prompty.RoleTool, Content: []prompty.ContentPart{prompty.ToolResultPart{ToolCallID: "c", Name: "tool", Content: []prompty.ContentPart{prompty.TextPart{Text: "ok"}}}}}},
		{ID: "media", Message: prompty.ChatMessage{Role: prompty.RoleUser, Content: []prompty.ContentPart{prompty.MediaPart{MediaType: "image", MIMEType: "image/png", Data: []byte{0, 255}}}}}}
	messages, mandatory := mustImport(t, mapper, records)
	snapshot := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, messages)
	before := snapshot.Segment(contexty.SegmentHistory)
	refBefore, err := contexty.MessageContentRef(before[0], mapper.Codec)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := mapper.Prepare(before, mandatory, ByteBudget{Window: 10000}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	messages[0].Parts[0].(contexty.ToolCallPart).Arguments.Data[0] = 'x'
	messages[2].Parts[0].(contexty.MediaPart).Data[0] = 42
	records[2].Message.Content[0].(prompty.MediaPart).Data[0] = 43
	messages[0].Parts[0] = contexty.TextPart{Text: "mutated"}
	// Assert.
	after := snapshot.Segment(contexty.SegmentHistory)
	refAfter, err := contexty.MessageContentRef(after[0], mapper.Codec)
	if err != nil {
		t.Fatal(err)
	}
	if refAfter != refBefore || !reflect.DeepEqual(before, after) {
		t.Fatal("immutable snapshot/digest changed")
	}
	if err = mapper.ValidatePrepared(prepared, after, now); err != nil {
		t.Fatal("compiled native result changed", err)
	}
	// Arrange: JSON whitespace normalization preserves semantics while native bytes remain exact.
	normalized := after[0].Parts[0].(contexty.ToolCallPart)
	normalized.Arguments.Data = json.RawMessage(`{ "n" : 9007199254740993 }`)
	after[0].Parts[0] = normalized
	// Act.
	output, err := mapper.Export(after, mandatory, now)
	// Assert.
	if err != nil || output[0].Message.Content[0].(prompty.ToolCallPart).Args != `{"n":9007199254740993}` {
		t.Fatal("semantic/exact JSON conflated", err)
	}
}

func TestStateCarrierAndPayloadCannotBeRewritten(t *testing.T) {
	mapper := fixtureMapper()
	for _, mutate := range []func([]contexty.Message){
		func(messages []contexty.Message) { messages[2].Role = contexty.RoleUser },
		func(messages []contexty.Message) {
			envelope := messages[2].Extensions[1].(contexty.OpaqueState)
			envelope.Payload = StatePayload{Wire: []byte(`{}`), Digest: digest([]byte(`{}`))}
			messages[2].Extensions[1] = envelope
		},
		func(messages []contexty.Message) {
			envelope := messages[2].Extensions[1].(contexty.OpaqueState)
			payload := envelope.Payload.(StatePayload)
			payload.Wire[0] = 'x'
			envelope.Payload = payload
			messages[2].Extensions[1] = envelope
		},
		func(messages []contexty.Message) {
			envelope := messages[2].Extensions[1].(contexty.OpaqueState)
			envelope.Codec.Revision = "unknown"
			messages[2].Extensions[1] = envelope
		},
	} {
		// Arrange.
		messages, mandatory := mustImport(t, mapper, stateRecords(mapper))
		// Act.
		mutate(messages)
		output, err := mapper.Export(messages, mandatory, time.Unix(100, 0))
		// Assert.
		if err == nil || output != nil {
			t.Fatal("state corruption accepted")
		}
	}
}

func TestPlainExecutionPinsActualDestination(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	now := time.Unix(100, 0)
	messages, mandatory := mustImport(t, mapper, []Record{plainRecord("user", prompty.RoleUser, "question")})
	prepared, err := mapper.Prepare(messages, mandatory, ByteBudget{Window: 10000}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	mapper.Destination.Model = "changed-model"
	err = mapper.ValidatePrepared(prepared, messages, now)
	// Assert.
	if !errors.Is(err, ErrStaleExecution) {
		t.Fatal("destination change accepted", err)
	}
	if prepared.Execution.ModelOptions.Model != "offline" {
		t.Fatal("native execution did not pin model")
	}
}
