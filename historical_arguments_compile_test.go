package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestHistoricalArguments_MultipleCalls(t *testing.T) {
	// Arrange: two independently prepared calls share the same original message.
	request, first := fixturePreparedHistoricalArguments(t)
	request.CallID = "second"
	second := fixturePrepareHistoricalCall(t, request)
	pending := contexty.Message{
		ID:   "active",
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{ID: "active-call", Name: "opaque", Arguments: contexty.TextPayload("pending bytes")},
		},
		Extensions: []contexty.Extension{fixtureWireExtension{wire: `{"approval_digest":"active-original"}`}},
	}
	engine := fixtureEngine(contexty.WithBudgetPipeline(contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
		contexty.CharTokenEstimator{},
	),
	))
	// Act: apply both without replacing other parts or retargeting the message.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		History: request.History,
		Pending: []contexty.Message{pending},
		Options: []contexty.CompileOption{
			contexty.WithHistoricalArgumentProjection(first),
			contexty.WithHistoricalArgumentProjection(second),
		},
	})
	// Assert: persistence keeps both originals; both prompt refs stay independent.
	require.NoError(t, err)
	require.Equal(t, request.History, fixturePersistenceSegment(t, result, contexty.SegmentHistory))
	for _, part := range result.Payload.History[0].Parts {
		call, ok := part.(contexty.ToolCallPart)
		require.True(t, ok)
		require.Equal(t, "ok", call.Arguments.Text)
		require.NotNil(t, call.ArgumentsBlob)
	}
	require.Equal(t, request.History[1], result.Payload.History[1])
	require.Equal(t, pending, result.Payload.History[2])
	require.Equal(t, []contexty.Message{pending}, result.Source.Pending)
}

func TestHistoricalArguments_CompilePersistenceReplay(t *testing.T) {
	// Arrange: raw arguments cannot fit the budget; completed prompt preview can.
	request, prepared := fixturePreparedHistoricalArguments(t)
	trace := fixtureTraceProfile()
	trace.RequireOrigins = true
	trace.Codec = request.Codec
	trace.Codecs = []contexty.CodecBinding{fixtureCodecBinding(contexty.CodecLabel, "fixture-label"),
		fixtureCodecBinding(contexty.CodecExtension, "fixture-label")}
	trace.Labels = contexty.LabelProjection{Registry: request.Codec.Extensions,
		Policy: fixtureLabelPolicy(func(_ context.Context, inputs []contexty.Message, output contexty.Message,
			_ contexty.Descriptor) (contexty.LabelDecision, error) {
			labels := output.Extensions
			if len(inputs) > 0 {
				labels = inputs[0].Extensions
			}
			return contexty.LabelDecision{Extensions: labels}, nil
		})}
	engine := fixtureEngine(
		contexty.WithTraceProfile(trace),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile("copy"), fixtureBinding(contexty.RecordingLabelPolicy, "", "", 0)),
		),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
		contexty.WithBudgetPipeline(contexty.NewBudgetPipeline(
			contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)}, contexty.CharTokenEstimator{})),
	)
	var origins []contexty.ContentRef
	for _, message := range request.History {
		ref, err := contexty.MessageContentRef(message, request.Codec)
		require.NoError(t, err)
		origins = append(origins, ref)
	}
	option := contexty.WithHistoricalArgumentProjection(prepared)
	prepared.Selection.Preview.Bytes[0] = 'X'
	// Act: authoritative History stays raw; the option projects before budget.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "argument-compile",
		History:       request.History,
		Origins:       origins,
		Options: []contexty.CompileOption{
			option,
		},
		Targets: []contexty.CompileTarget{{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "copy"}},
	})
	// Assert: call/result/approval identity and original persistence are intact.
	require.NoError(t, err)
	require.Equal(t, request.History, result.Source.History)
	require.Equal(t, request.History, fixturePersistenceSegment(t, result, contexty.SegmentHistory))
	require.Equal(t, prepared.Prompt, result.Payload.History)
	require.Equal(t, prepared.Prompt, result.Projections["copy"].Messages)
	require.Equal(t, request.History[0].Extensions, result.Payload.History[0].Extensions)
	require.Contains(t, result.Transformations["assistant"], contexty.TransformRecord{
		Action: contexty.ActionFormatted, Reason: contexty.ReasonHistoricalArguments})
	require.NoError(t, result.Lineage.Validate())
	found := false
	for _, edge := range result.Lineage.Records {
		if edge.Stage == "historical-arguments" {
			found = true
			require.Equal(t, prepared.Reference.Policy, edge.Transform)
		}
	}
	require.True(t, found)
	checkpointCodec := contexty.ConversationCodec{
		Extensions:    request.Codec.Extensions,
		OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""},
	}
	checkpoint, err := checkpointCodec.Encode(contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory,
		fixturePersistenceSegment(t, result, contexty.SegmentHistory)))
	require.NoError(t, err)
	resumed, err := checkpointCodec.Decode(checkpoint)
	require.NoError(t, err)
	resumeResult, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "argument-resume",
		History:       resumed.Segment(contexty.SegmentHistory),
		Origins:       origins,
		Options: []contexty.CompileOption{
			option,
		},
		Targets: []contexty.CompileTarget{{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "copy"}},
	})
	require.NoError(t, err)
	require.Equal(t, result.Payload.History, resumeResult.Payload.History)
	require.Equal(t, request.History, fixturePersistenceSegment(t, resumeResult, contexty.SegmentHistory))
	accepted, err := result.Record.Accept("host-accept")
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*result.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), restored, expected, request.Codec,
		fixtureBlobReplayOption(t, *prepared.Selection.Stored))
	require.NoError(t, err)
	require.Equal(t, result.Payload.History, replayed.Outputs[0].Segments[string(contexty.SegmentHistory)])
	exported, err := contexty.ExportProjection(
		contexty.CompileProjection{Messages: result.Payload.History, Lineage: result.Lineage},
		contexty.ExportSelection{MessageIDs: []string{"assistant", "result"}},
		request.Codec,
	)
	require.NoError(t, err)
	exportWire, err := json.Marshal(exported)
	require.NoError(t, err)
	for _, forbidden := range []string{"write-scope", "retention", "argument-first", "original arguments ", "host-original"} {
		require.NotContains(t, string(exportWire), forbidden)
	}
}

func TestHistoricalArguments_CompilePreflight(t *testing.T) {
	for _, scenario := range []string{"pending", "changed-arguments", "changed-approval", "preview", "source", "edge", "duplicate", "missing-reference", "already-projected"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: malformed/stale projection fails before any transform callback.
			request, prepared := fixturePreparedHistoricalArguments(t)
			switch scenario {
			case "pending":
				request.History[1].Parts = request.History[1].Parts[:1]
			case "changed-arguments":
				call := request.History[0].Parts[0].(contexty.ToolCallPart)
				call.Arguments.Text = "modified working file"
				request.History[0].Parts[0] = call
			case "changed-approval":
				request.History[0].Extensions = []contexty.Extension{
					fixtureWireExtension{wire: `{"approval_digest":"changed"}`},
				}
			case "preview":
				prepared.Prompt[0].Parts = nil
			case "source":
				prepared.Source[0].Extensions = nil
			case "edge":
				prepared.Lineage.Records[0].Transform.Revision = "changed"
			case "missing-reference":
				prepared.Reference = nil
			case "already-projected":
				request.History = prepared.Prompt
			}
			options := []contexty.CompileOption{contexty.WithHistoricalArgumentProjection(prepared)}
			if scenario == "duplicate" {
				options = append(options, options[0])
			}
			calls := 0
			engine := fixtureEngine(
				contexty.WithTransformHooks(fixtureTextTransform{Replacer: func(text string) string {
					calls++
					return text
				}}),
			)
			// Act.
			result, err := engine.CompileSnapshot(
				context.Background(),
				contexty.CompileRequest{History: request.History, Options: options},
			)
			// Assert.
			require.ErrorIs(t, err, contexty.ErrInvalidHistoricalArguments)
			require.Zero(t, result)
			require.Zero(t, calls)
		})
	}
}
