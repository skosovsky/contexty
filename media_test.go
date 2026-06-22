package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestMedia_Codec(t *testing.T) {
	// Arrange: arbitrary bytes are not text, metadata, or a guessed provider format.
	message := contexty.Message{ID: "media", Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.MediaPart{MIMEType: "audio/wav", Data: []byte{0, 255, 1}}}}
	codec := contexty.DefaultJSONSerializer()
	// Act.
	wire, err := codec.Marshal(message)
	require.NoError(t, err)
	var restored contexty.Message
	err = codec.Unmarshal(wire, &restored)
	// Assert.
	require.NoError(t, err)
	require.Contains(t, string(wire), "00ff01")
	require.Equal(t, message, restored)
	clone := restored.Clone()
	clone.Parts[0].(contexty.MediaPart).Data[0] = 42
	require.Equal(t, byte(0), restored.Parts[0].(contexty.MediaPart).Data[0])
	for _, bad := range []string{
		`{"mime_type":"audio/wav","data_hex":"x"}`,
		`{"mime_type":"broken","data_hex":"00"}`,
		`{"mime_type":"*/*","data_hex":"00"}`,
		`{"mime_type":"audio/wav"}`,
		`{"mime_type":"audio/wav","data_hex":null}`,
		`{"mime_type":"audio/wav","data_hex":"00","hidden":"secret"}`,
	} {
		var part contexty.MediaPart
		require.ErrorIs(t, json.Unmarshal([]byte(bad), &part), contexty.ErrInvalidMediaPart)
		require.Zero(t, part)
	}
	_, err = codec.Marshal(contexty.Message{Parts: []contexty.ContentPart{contexty.MediaPart{Data: []byte{1}}}})
	require.ErrorIs(t, err, contexty.ErrInvalidMediaPart)
}

func TestMedia_Estimate(t *testing.T) {
	// Arrange: neither rune count nor binary length is a supported media token cost.
	message := contexty.Message{ID: "media", Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.MediaPart{MIMEType: "video/mp4", Data: []byte{1, 2, 3}}}}
	ctx := context.Background()
	request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(5),
		Segments: []contexty.EstimateSegment{{Name: "media", Messages: []contexty.Message{message}}}}
	strict, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	// Act.
	report, err := strict.Report(ctx, request)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
	require.Zero(t, report)
	_, err = (contexty.CharTokenEstimator{}).Estimate(ctx, []contexty.Message{message})
	require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
	_, err = (&contexty.CharFallbackEstimator{CharsPerToken: 4}).Estimate(ctx, []contexty.Message{message})
	require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
	profile := fixtureEstimateProfile()
	profile.Fallback = &contexty.EstimateFallback{
		Policy: contexty.Descriptor{ID: "fallback", Revision: "pinned"},
		Tokens: 7,
	}
	permissive, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		profile,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	report, err = permissive.Report(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 7, report.Total)
	require.Equal(t, contexty.EstimateUnknown, report.Quality)
	require.Equal(t, contexty.ReasonTokenBudgetExceeded, report.OverflowReason)
	require.Equal(t, contexty.EstimateMedia, report.Segments[0].Coverage[0].Kind)
	wire, err := contexty.EncodeEstimateReport(report)
	require.NoError(t, err)
	restored, err := contexty.DecodeEstimateReport(wire)
	require.NoError(t, err)
	require.Equal(t, report, restored)
}

func TestMedia_ArtifactReplay(t *testing.T) {
	// Arrange: optional preview must not overwrite the binary body.
	ctx := context.Background()
	payload := contexty.BinaryPayload([]byte{0, 255, 3}, "application/pdf")
	payload.Text = "preview"
	artifact := contexty.NewMemoryBlock("document", payload).ContextArtifact
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	// Act.
	compiled, err := engine.CompileSnapshot(
		ctx,
		contexty.CompileRequest{CompilationID: "media", Artifacts: []contexty.ContextArtifact{artifact}},
	)
	// Assert: typed bytes, source artifact and lineage survive exact replay.
	require.NoError(t, err)
	require.Len(t, compiled.Payload.Memory, 1)
	parts := compiled.Payload.Memory[0].Parts
	require.Equal(t, contexty.TextPart{Text: "preview"}, parts[0])
	require.Equal(t, contexty.MediaPart{MIMEType: "application/pdf", Data: payload.Binary}, parts[1])
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	replayed, err := contexty.Replay(ctx, restored, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, compiled.Payload.Memory, replayed.Outputs[0].Segments["memory"])
	parts[1].(contexty.MediaPart).Data[0] = 99
	require.Equal(t, byte(0), compiled.Source.Artifacts[0].Payload.Binary[0])
	require.Equal(t, byte(0), replayed.Outputs[0].Segments["memory"][0].Parts[1].(contexty.MediaPart).Data[0])
}

func TestMedia_ViewsAndInvalidArtifact(t *testing.T) {
	// Arrange.
	message := contexty.Message{ID: "media", Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.MediaPart{MIMEType: "image/png", Data: []byte{1}}}}
	snapshot := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{message})
	// Act and Assert: text-only views never silently drop media.
	for _, view := range []contexty.ViewType{contexty.ViewLLMXML, contexty.ViewFlatClassifier} {
		text, err := contexty.Render(context.Background(), snapshot, view)
		require.ErrorIs(t, err, contexty.ErrUnsupportedMediaRendering)
		require.Empty(t, text)
	}
	for _, mimeType := range []string{"", "broken"} {
		artifact := contexty.NewMemoryBlock("invalid", contexty.BinaryPayload([]byte{1}, mimeType)).ContextArtifact
		result, err := contexty.NewEngine().
			CompileSnapshot(context.Background(), contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{artifact}})
		require.ErrorIs(t, err, contexty.ErrInvalidMediaPart)
		require.Zero(t, result)
	}
}

func TestMedia_ArtifactBudget(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict", true: "positive fallback"}[fallback], func(t *testing.T) {
			// Arrange: binary artifacts use the same report profile as final output.
			profile := fixtureEstimateProfile()
			if fallback {
				profile.Fallback = &contexty.EstimateFallback{
					Policy: contexty.Descriptor{ID: "fallback", Revision: "pinned"}, Tokens: 7}
			}
			reporter, err := contexty.NewEstimateReporter(
				contexty.CharTokenEstimator{},
				profile,
				contexty.DefaultJSONSerializer(),
			)
			require.NoError(t, err)
			artifact := contexty.NewMemoryBlock(
				"audio",
				contexty.BinaryPayload([]byte{1, 2}, "audio/wav"),
			).ContextArtifact.
				WithBudget(
					contexty.ArtifactBudgetPolicy{TokenLimit: 7},
				)
			engine := contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory,
				contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(20)}, reporter)))
			// Act.
			compiled, err := engine.CompileSnapshot(
				context.Background(),
				contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{artifact}},
			)
			// Assert: fallback does not remove or rewrite actual bytes.
			if !fallback {
				require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
				require.Zero(t, compiled)
				return
			}
			require.NoError(t, err)
			require.Equal(
				t,
				contexty.MediaPart{MIMEType: "audio/wav", Data: []byte{1, 2}},
				compiled.Payload.Memory[0].Parts[0],
			)
			require.Len(t, compiled.Estimates, 1)
			require.Equal(t, 7, compiled.Estimates[0].Report.Total)
			require.Equal(t, contexty.EstimateUnknown, compiled.Estimates[0].Report.Quality)
		})
	}
}
