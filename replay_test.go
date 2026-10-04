package contexty_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestExact_Replay(t *testing.T) {
	// Arrange: every nondeterministic execution increments a counter.
	ctx := context.Background()
	a := contexty.TextMessage(contexty.RoleUser, "long question")
	a.ID = "a"
	b := contexty.TextMessage(contexty.RoleAssistant, "long answer")
	b.ID = "b"
	artifact := contexty.NewMemoryBlock("memory", contexty.TextPayload("memo")).ContextArtifact
	hooks, summaries, resolves, formats, policies := 0, 0, 0, 0, 0
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(12),
		Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
			summaries++
			message := contexty.TextMessage(contexty.RoleAssistant, "sum")
			message.ID = "summary"
			return message, nil
		})}, contexty.CharTokenEstimator{}, contexty.WithSummarizerDescriptor(fixtureTraceProfile().Stages["summarize"]))
	policy := fixtureContentPolicy(func(_ context.Context, candidate contexty.CaptureCandidate) (bool, error) {
		policies++
		// Changing callback bytes must not change source or saved wire.
		for i := range candidate.Content.Wire {
			candidate.Content.Wire[i] = 'x'
		}
		return true, nil
	})
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureBindings(
			fixtureRecordProfile("small", "xml"),
			fixtureBinding(contexty.RecordingHook, "", "", 0),
			fixtureBinding(contexty.RecordingResolver, "", "", 0),
			fixtureBinding(
				contexty.RecordingTargetFormatter,
				"small",
				"",
				0,
			),
			fixtureBinding(contexty.RecordingViewRenderer, "xml", "", 0),
		)),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"}, policy),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithTransformHooks(
			contexty.RedactionHook{Replacer: func(text string) string { hooks++; return text + "!" }},
		),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{Name: "chosen", Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				resolves++
				message := contexty.TextMessage(contexty.RoleUser, "dep")
				message.ID = "dep"
				return contexty.DeferredResult{Messages: []contexty.Message{message}}, nil
			}}),
	)
	request := contexty.CompileRequest{CompilationID: "saved", History: []contexty.Message{a, b},
		Artifacts: []contexty.ContextArtifact{artifact}, Targets: []contexty.CompileTarget{
			{
				Name: "small",
				Budget: contexty.NewBudgetPipeline(
					contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(8)},
					contexty.CharTokenEstimator{},
				),
				Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
					formats++
					messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "sum!"}}
					return messages, nil
				},
			},
			{Name: "xml", View: string(contexty.ViewLLMXML)},
		}}
	compiled, err := engine.CompileSnapshot(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, compiled.Record)
	require.Equal(t, contexty.RecordProposed, compiled.Record.State)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	baseline := []int{hooks, summaries, resolves, formats, policies}
	// Act: reconstruct after restart with codecs only, without the engine or ports.
	restored, err := contexty.DecodeSavedRecord(wire)
	require.NoError(t, err)
	replayed, err := contexty.Replay(ctx, restored, expected, contexty.DefaultJSONSerializer())
	// Assert: every output is restored; no nondeterministic execution occurred.
	require.NoError(t, err)
	actualCalls := []int{hooks, summaries, resolves, formats, policies}
	require.Equal(t, baseline, actualCalls)
	require.Equal(t, 1, summaries)
	require.Equal(t, 1, resolves)
	require.Equal(t, 1, formats)
	require.Equal(t, compiled.Payload.History, replayed.Outputs[0].Segments[string(contexty.SegmentHistory)])
	require.Equal(t, compiled.Payload.Memory, replayed.Outputs[0].Segments[string(contexty.SegmentMemory)])
	require.Equal(t, compiled.Projections["small"].Messages, replayed.Outputs[1].Segments["messages"])
	require.Equal(t, compiled.Projections["small"].Text, replayed.Outputs[1].Text)
	require.Equal(t, compiled.Projections["xml"].Text, replayed.Outputs[2].Text)
	require.Equal(t, compiled.Projections["xml"].Rendered.Message, *replayed.Outputs[2].Rendered)
	require.Equal(t, compiled.Artifacts, replayed.Artifacts)
	require.Equal(t, compiled.Lineage, replayed.Outputs[0].Lineage)
	// Arrange / Act / Assert: a coverage hole rejects the whole replay, not a partial result.
	badCoverage, err := restored.Clone()
	require.NoError(t, err)
	badCoverage.Manifest.Coverage = badCoverage.Manifest.Coverage[1:]
	partial, replayErr := contexty.Replay(ctx, badCoverage, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, replayErr, contexty.ErrInvalidCoverage)
	require.Zero(t, partial)
	for i, message := range compiled.Payload.History {
		originalWire, wireErr := contexty.DefaultJSONSerializer().Marshal(message)
		require.NoError(t, wireErr)
		require.Equal(t, originalWire, []byte(replayed.Outputs[0].WireSegments["history"][i]))
	}
	// Mutation of replay data or the returned manifest cannot mutate saved data.
	replayed.Outputs[0].Segments[string(contexty.SegmentHistory)][0].Parts = nil
	replayed.Manifest.Profile.Targets["small"] = contexty.Descriptor{}
	again, err := contexty.Replay(ctx, restored, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, compiled.Payload.History, again.Outputs[0].Segments[string(contexty.SegmentHistory)])
	require.Equal(t, "long question", a.TextContent())
	// Arrange / Act / Assert: even available final output cannot replace a deleted dependency.
	missing, err := restored.Clone()
	require.NoError(t, err)
	dependency := missing.Manifest.ResolvedDependencies[0]
	missing.Content = slices.DeleteFunc(missing.Content, func(content contexty.SavedContent) bool {
		return content.Ref.ID == dependency.ID && content.Ref.Digest == dependency.Digest
	})
	_, err = contexty.Replay(ctx, missing, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	actualCalls = []int{hooks, summaries, resolves, formats, policies}
	require.Equal(t, baseline, actualCalls)
}

func TestReplay_RequiresCodecs(t *testing.T) {
	// Arrange: accepted content requires a host extension decoder.
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(data)}, nil
	})
	trace := fixtureTraceProfile()
	trace.Codec.Extensions = registry
	trace.Codecs = []contexty.CodecBinding{
		{
			Kind:       contexty.CodecExtension,
			Type:       "fixture-label",
			Descriptor: contexty.Descriptor{ID: "host/label-codec", Revision: "pinned"},
		},
		{
			Kind:       contexty.CodecLabel,
			Type:       "fixture-label",
			Descriptor: contexty.Descriptor{ID: "host/label-codec", Revision: "pinned"},
		},
	}
	trace.Labels = contexty.LabelProjection{Registry: registry, RequiredTypes: []string{"fixture-label"},
		Policy: fixtureLabelPolicy(func(_ context.Context, inputs []contexty.Message,
			_ contexty.Message, _ contexty.Descriptor,
		) (contexty.LabelDecision, error) {
			return contexty.LabelDecision{Extensions: inputs[0].Extensions}, nil
		})}
	message := contexty.TextMessage(contexty.RoleUser, "labeled")
	message.ID = "m"
	message.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"label":"host-owned"}`}}
	result, err := contexty.NewEngine(
		contexty.WithTraceProfile(trace),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingLabelPolicy, "", "", 0)),
		),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent)),
	).CompileSnapshot(context.Background(),
		contexty.CompileRequest{CompilationID: "codec", History: []contexty.Message{message}})
	require.NoError(t, err)
	record, err := result.Record.Accept("host-approve")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*result.Manifest)
	require.NoError(t, err)
	// Act / Assert: no silent label loss with an absent codec.
	_, err = contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrReplayCodec)
	replayed, err := contexty.Replay(context.Background(), record, expected, trace.Codec)
	require.NoError(t, err)
	require.Equal(t, message.Extensions, replayed.Outputs[0].Segments["history"][0].Extensions)
}

func TestCapture_PolicyCancellation(t *testing.T) {
	// Arrange: host policy cancels the compile while deciding on content.
	ctx, cancel := context.WithCancel(context.Background())
	message := contexty.TextMessage(contexty.RoleUser, "input")
	message.ID = "m"
	policy := fixtureContentPolicy(func(context.Context, contexty.CaptureCandidate) (bool, error) {
		cancel()
		return true, nil
	})
	engine := contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"}, policy))
	// Act / Assert: no partial record is returned after cancellation.
	result, err := engine.CompileSnapshot(
		ctx,
		contexty.CompileRequest{CompilationID: "cancel", History: []contexty.Message{message}},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result.Record)
}

func TestRecompute_CreatesNewRecord(t *testing.T) {
	// Arrange: recompute receives content explicitly and uses new pinned behavior.
	previous, _ := fixtureSimpleRecord(t)
	message := contexty.TextMessage(contexty.RoleUser, "changed input")
	message.ID = "m"
	profile := fixtureRecordProfile()
	profile.Pipeline.Revision = "changed"
	profile = fixtureBindings(profile, fixtureBinding(contexty.RecordingHook, "", "", 0))
	calls := 0
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(profile),
		contexty.WithTransformHooks(
			contexty.RedactionHook{Replacer: func(text string) string { calls++; return text + "!" }},
		),
	)
	request := contexty.CompileRequest{CompilationID: "next", History: []contexty.Message{message}}
	// Act.
	result, err := engine.Recompute(context.Background(), previous.Manifest, request)
	// Assert: old manifest/output remains unchanged; new execution has new evidence.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "changed input!", result.Payload.History[0].TextContent())
	require.NotEqual(t, previous.Manifest.Digest, result.Manifest.Digest)
	require.Equal(t, "next", result.Manifest.ID)
	require.Equal(t, previous.Manifest.ID, result.Manifest.PreviousRecord.ID)
	require.Equal(t, previous.Manifest.Digest, result.Manifest.PreviousRecord.Digest)
	require.Greater(t, len(result.Lineage.Records), len(previous.Manifest.Outputs[0].Lineage.Records))
	require.Equal(t, "simple", previous.Manifest.ID)
	require.Nil(t, request.PreviousRecord)
	// Arrange / Act / Assert: reusing the previous ID performs no execution.
	request.CompilationID = previous.Manifest.ID
	_, err = engine.Recompute(context.Background(), previous.Manifest, request)
	require.ErrorIs(t, err, contexty.ErrRecomputeIdentity)
	require.Equal(t, 1, calls)
}

func TestReplay_Failures(t *testing.T) {
	// Arrange: mutable copies of a valid accepted record/current expectation.
	record, expected := fixtureSimpleRecord(t)
	for name, mutate := range map[string]func(*contexty.ReplayExpectation){
		"profile": func(e *contexty.ReplayExpectation) { e.Profile.Prompt.Revision = "changed" },
		"budget":  func(e *contexty.ReplayExpectation) { e.Budgets[0].TokenLimit++ },
		"reservations": func(e *contexty.ReplayExpectation) {
			e.Budgets[0].Request = contexty.WindowInputBudget(15, 3, 2)
		},
		"inputs":   func(e *contexty.ReplayExpectation) { e.Inputs[1].Messages = nil },
		"encoding": func(e *contexty.ReplayExpectation) { e.Encoding.Revision = "changed" },
		"revision": func(e *contexty.ReplayExpectation) { e.SourceRevision++ },
	} {
		t.Run(name, func(t *testing.T) {
			intent, err := contexty.ReplayExpectationFor(record.Manifest)
			require.NoError(t, err)
			mutate(&intent)
			// Act / Assert.
			_, err = contexty.Replay(context.Background(), record, intent, contexty.DefaultJSONSerializer())
			require.ErrorIs(t, err, contexty.ErrReplayMismatch)
		})
	}
	// Act / Assert: deletion never triggers hidden refetch or partial output.
	copyRecord, err := record.Clone()
	require.NoError(t, err)
	copyRecord.Content = nil
	result, err := contexty.Replay(context.Background(), copyRecord, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.Empty(t, result.Outputs)
	// Arrange / Act / Assert: saved bytes cannot disagree with their identity.
	copyRecord, err = record.Clone()
	require.NoError(t, err)
	copyRecord.Content[0].Wire = json.RawMessage(`{"id":"m","parts":[]}`)
	_, err = contexty.Replay(context.Background(), copyRecord, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrReplayContentMismatch)
	// Arrange / Act / Assert: impossible recorded budget is explicit overflow.
	copyRecord, err = record.Clone()
	require.NoError(t, err)
	copyRecord.Manifest.Budgets[0].EstimatedTokens = 11
	_, err = contexty.Replay(context.Background(), copyRecord, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	copyRecord, err = record.Supersede("host-supersede")
	require.NoError(t, err)
	_, err = contexty.Replay(context.Background(), copyRecord, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrUnsupportedReplay)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = contexty.Replay(ctx, record, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, context.Canceled)
}

func TestReplay_Privacy(t *testing.T) {
	// Arrange: raw content is forbidden, approved redacted output is independently saved.
	message := contexty.TextMessage(contexty.RoleUser, "SECRET-RAW")
	message.ID = "m"
	policy := fixtureContentPolicy(func(_ context.Context, candidate contexty.CaptureCandidate) (bool, error) {
		return !bytes.Contains(candidate.Content.Wire, []byte("SECRET-RAW")), nil
	})
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingHook, "", "", 0)),
		),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"}, policy),
		contexty.WithTransformHooks(contexty.RedactionHook{Replacer: func(string) string { return "safe" }}),
	)
	compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "private",
		History: []contexty.Message{message}})
	require.NoError(t, err)
	accepted, err := compiled.Record.Accept("host-approve-safe")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	wire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "SECRET-RAW")
	require.Len(t, accepted.Omitted, 1)
	// Act / Assert: missing raw is not secretly fetched; only approved saved output returns.
	replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, "safe", replayed.Outputs[0].Segments["history"][0].TextContent())
	// Arrange: a purpose-only policy denies input but would allow unchanged output.
	purposePolicy := fixtureContentPolicy(func(_ context.Context, candidate contexty.CaptureCandidate) (bool, error) {
		return candidate.Purpose != contexty.CaptureInput, nil
	})
	engine = contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"}, purposePolicy))
	compiled, err = engine.CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "unchanged",
		History: []contexty.Message{message}})
	require.NoError(t, err)
	// Act / Assert: source-stage/output relabeling of purpose cannot evade denial.
	require.Empty(t, compiled.Record.Content)
	incomplete, err := compiled.Record.Accept("host-approve")
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.Empty(t, incomplete.State)
	_, err = contexty.Replay(context.Background(), *compiled.Record, expected, contexty.DefaultJSONSerializer())
	require.ErrorIs(t, err, contexty.ErrUnsupportedReplay)
}
