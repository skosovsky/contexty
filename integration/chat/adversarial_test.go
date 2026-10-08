package chat

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/skosovsky/prompty"

	"github.com/skosovsky/contexty"
)

func TestAuditCarrierRoleChangeInvalidatesState(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	messages, mandatory := mustImport(t, mapper, stateRecords(mapper))
	messages[2].Role = contexty.RoleUser
	// Act.
	records, err := mapper.Export(messages, mandatory, time.Unix(100, 0))
	// Assert.
	if err == nil {
		t.Fatalf(
			"carrier role changed assistant->user but provider state accepted: %#v",
			records[2].Message.ProviderState,
		)
	}
}

func TestAuditCorruptMandatoryPayloadIsRejected(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	messages, mandatory := mustImport(t, mapper, stateRecords(mapper))
	state := messages[2].Extensions[1].(contexty.OpaqueState)
	state.Payload = StatePayload{Wire: []byte(`{}`)}
	messages[2].Extensions[1] = state
	// Act.
	records, err := mapper.Export(messages, mandatory, time.Unix(100, 0))
	// Assert.
	if err == nil {
		t.Fatalf(
			"mandatory payload replaced by {} but export succeeded with states=%#v annotations=%#v",
			records[2].Message.ProviderState,
			records[2].Message.MessageAnnotations,
		)
	}
}

func TestAuditAdjacentLargeIntegerMismatchRejected(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	record := Record{
		ID: "call",
		Message: prompty.ChatMessage{
			Role:    prompty.RoleAssistant,
			Content: []prompty.ContentPart{prompty.ToolCallPart{ID: "c", Name: "n", Args: `{"n":9007199254740993}`}},
		},
	}
	messages, mandatory := mustImport(t, mapper, []Record{record})
	call := messages[0].Parts[0].(contexty.ToolCallPart)
	call.Arguments = contexty.JSONPayload(`{"n":9007199254740992}`)
	messages[0].Parts[0] = call
	// Act.
	output, err := mapper.Export(messages, mandatory, time.Unix(100, 0))
	// Assert.
	if err == nil || output != nil {
		t.Fatal("distinct integers treated as equal", err)
	}
}

func TestAuditInlineMediaMutationCannotAliasSnapshot(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	data := []byte{1, 2, 255}
	records := []Record{
		{
			ID: "media",
			Message: prompty.ChatMessage{
				Role: prompty.RoleUser,
				Content: []prompty.ContentPart{
					prompty.MediaPart{MediaType: "audio", MIMEType: "audio/wav", Data: data},
				},
			},
		},
	}
	messages, mandatory := mustImport(t, mapper, records)
	snapshot := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, messages)
	// Act.
	data[0] = 3
	messages[0].Parts[0].(contexty.MediaPart).Data[1] = 4
	output, err := mapper.Export(snapshot.Segment(contexty.SegmentHistory), mandatory, time.Unix(100, 0))
	// Assert.
	if err != nil || !bytes.Equal(output[0].Message.Content[0].(prompty.MediaPart).Data, []byte{1, 2, 255}) {
		t.Fatal("media alias", err)
	}
}

func TestAuditExpiryBoundaryAndScopeFail(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	messages, mandatory := mustImport(t, mapper, stateRecords(mapper))
	// Act.
	_, expired := mapper.Prepare(messages, mandatory, ByteBudget{Window: 10000}, time.Unix(200, 0))
	changed := mapper
	changed.Destination.Endpoint = "https://other.invalid"
	_, scope := changed.Prepare(messages, mandatory, ByteBudget{Window: 10000}, time.Unix(100, 0))
	// Assert.
	if !errors.Is(expired, prompty.ErrStateExpired) || scope == nil {
		t.Fatal(expired, scope)
	}
}

func TestAuditOutsideScopeChangeAndNativeTamper(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	messages, mandatory := mustImport(t, mapper, stateRecords(mapper))
	messages = append(
		messages,
		contexty.Message{
			ID:    "tail",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "first"}},
		},
	)
	messages[3].Parts[0] = contexty.TextPart{Text: "after"}
	// Act.
	prepared, err := mapper.Prepare(messages, mandatory, ByteBudget{Window: 10000}, time.Unix(100, 0))
	// Assert.
	if err != nil {
		t.Fatal("outside scope invalidated", err)
	}
	// Arrange.
	prepared.Execution.Messages[3].Role = prompty.RoleDeveloper
	// Act.
	err = mapper.ValidatePrepared(prepared, messages, time.Unix(100, 0))
	// Assert.
	if !errors.Is(err, ErrStaleExecution) {
		t.Fatal("stale native role accepted", err)
	}
}

func TestAuditDestinationMutationInvalidatesPreparedPlainRequest(t *testing.T) {
	// Arrange.
	mapper := fixtureMapper()
	messages, mandatory := mustImport(t, mapper, []Record{plainRecord("user", prompty.RoleUser, "hello")})
	prepared, err := mapper.Prepare(messages, mandatory, ByteBudget{Window: 10000}, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	mapper.Destination.Model = "different-model"
	// Act.
	err = mapper.ValidatePrepared(prepared, messages, time.Unix(100, 0))
	// Assert.
	if err == nil {
		t.Fatal("prepared report for original destination accepted after destination mutation")
	}
}
