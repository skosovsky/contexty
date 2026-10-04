package contexty_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

// acceptanceOpaquePayload is a host fixture; core has no knowledge of its bytes.
type acceptanceOpaquePayload struct {
	Bytes []byte `json:"bytes"`
}

func (acceptanceOpaquePayload) ExtensionType() string { return "acceptance.host.state" }
func (p acceptanceOpaquePayload) CloneExtension() contexty.Extension {
	p.Bytes = slices.Clone(p.Bytes)
	return p
}

func acceptanceOpaqueCodec() contexty.JSONSerializer {
	codec := contexty.DefaultJSONSerializer()
	codec.Extensions.RegisterOpaquePayload(
		"acceptance.host.state",
		contexty.Descriptor{ID: "host/encoding", Revision: "1"},
		func(data []byte) (contexty.Extension, error) {
			var payload acceptanceOpaquePayload
			err := json.Unmarshal(data, &payload)
			return payload, err
		},
	)
	return codec
}

func acceptanceOpaqueFixture(t *testing.T) ([]contexty.Message, contexty.JSONSerializer, contexty.Descriptor) {
	t.Helper()
	codec := acceptanceOpaqueCodec()
	profile := contexty.Descriptor{ID: "host/model", Revision: "fixture-v1"}
	messages := []contexty.Message{
		fixtureRollingText("a", "first"),
		fixtureRollingText("b", "second"),
		fixtureRollingText("state", "answer"),
		fixtureRollingText("outside", "tail"),
	}
	refs := make([]contexty.ContentRef, 2)
	for i := range refs {
		var err error
		refs[i], err = contexty.MessageContentRef(messages[i], codec)
		require.NoError(t, err)
	}
	messages[2].Extensions = []contexty.Extension{
		contexty.OpaqueState{
			ID:      "signature",
			Codec:   contexty.Descriptor{ID: "host/encoding", Revision: "1"},
			Payload: acceptanceOpaquePayload{Bytes: []byte{0, 255, 128, 1}},
			Placement: contexty.OpaquePlacement{
				AfterPart: 0,
			},
			Binding: contexty.OpaqueBinding{
				Profile:  profile,
				Required: refs,
				Prefix:   slices.Clone(refs),
				Boundary: "b",
			},
		},
	}
	return messages, codec, profile
}

func TestAcceptance_OpaqueDeclaredDependencies(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]contexty.Message) []contexty.Message
		valid  bool
	}{
		{"unchanged", func(m []contexty.Message) []contexty.Message { return m }, true},
		{"dependent text mutation", func(m []contexty.Message) []contexty.Message {
			m[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "changed"}}
			return m
		}, false},
		{"reorder", func(m []contexty.Message) []contexty.Message { m[0], m[1] = m[1], m[0]; return m }, false},
		{"inside prefix insertion", func(m []contexty.Message) []contexty.Message {
			return slices.Insert(m, 1, fixtureRollingText("inserted", "new"))
		}, false},
		{"truncation", func(m []contexty.Message) []contexty.Message { return m[1:] }, false},
		{"summary replacement", func(m []contexty.Message) []contexty.Message {
			return append([]contexty.Message{fixtureRollingText("summary", "first + second")}, m[2:]...)
		}, false},
		{"outside prefix mutation", func(m []contexty.Message) []contexty.Message {
			m[3].Parts = []contexty.ContentPart{contexty.TextPart{Text: "changed tail"}}
			return m
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: exact refs bind a complete ordered prefix; tail remains unbound.
			original, codec, profile := acceptanceOpaqueFixture(t)
			before, err := codec.Marshal(original[2])
			require.NoError(t, err)
			owned := make([]contexty.Message, len(original))
			for i := range original {
				owned[i] = original[i].Clone()
			}
			// Act.
			err = contexty.ValidateOpaqueState(tc.mutate(owned), codec, profile)
			// Assert: edits never repair or mutate the original host state.
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, contexty.ErrOpaqueStateInvalidated)
			}
			after, encodeErr := codec.Marshal(original[2])
			require.NoError(t, encodeErr)
			require.Equal(t, before, after)
		})
	}
}

func TestAcceptance_OpaqueProfileAndEncodingAreExplicit(t *testing.T) {
	// Arrange.
	messages, codec, profile := acceptanceOpaqueFixture(t)
	wrongCodec := contexty.DefaultJSONSerializer()
	wrongCodec.Extensions.RegisterOpaquePayload(
		"acceptance.host.state",
		contexty.Descriptor{ID: "host/encoding", Revision: "2"},
		func(data []byte) (contexty.Extension, error) {
			var p acceptanceOpaquePayload
			err := json.Unmarshal(data, &p)
			return p, err
		},
	)
	// Act and assert: a same-shaped payload cannot imply compatible host semantics.
	require.ErrorIs(
		t,
		contexty.ValidateOpaqueState(messages, codec, contexty.Descriptor{ID: profile.ID, Revision: "other"}),
		contexty.ErrOpaqueStateInvalidated,
	)
	require.ErrorIs(
		t,
		contexty.ValidateOpaqueState(messages, contexty.DefaultJSONSerializer(), profile),
		contexty.ErrMissingOpaqueStateCodec,
	)
	require.ErrorIs(t, contexty.ValidateOpaqueState(messages, wrongCodec, profile), contexty.ErrMissingOpaqueStateCodec)
}

func acceptanceOpaqueEngine(
	codec contexty.JSONSerializer,
	profile contexty.Descriptor,
	mode contexty.OpaqueInvalidationMode,
	options ...contexty.EngineOption,
) *contexty.Engine {
	trace := fixtureTraceProfile()
	trace.Codec = codec
	trace.Labels.Registry = codec.Extensions
	trace.Codecs = []contexty.CodecBinding{
		{
			Kind:       contexty.CodecExtension,
			Type:       "acceptance.host.state",
			Descriptor: contexty.Descriptor{ID: "host/encoding", Revision: "1"},
		},
		{
			Kind:       contexty.CodecLabel,
			Type:       "acceptance.host.state",
			Descriptor: contexty.Descriptor{ID: "host/encoding", Revision: "1"},
		},
	}
	base := []contexty.EngineOption{
		contexty.WithTraceProfile(trace),
		contexty.WithOpaqueStatePolicy(
			contexty.OpaqueStatePolicy{
				Identity:    contexty.Descriptor{ID: "host/lifecycle", Revision: "1"},
				Profile:     profile,
				Invalidated: mode,
			},
		),
	}
	return contexty.NewEngine(append(base, options...)...)
}

func TestAcceptance_OpaqueOutputPolicyCannotRepairBinding(t *testing.T) {
	for _, edit := range []string{"dependency", "payload", "rebind", "placement"} {
		t.Run(edit, func(t *testing.T) {
			// Arrange: final output policy owns copies but cannot mint a new opaque authorization.
			messages, codec, profile := acceptanceOpaqueFixture(t)
			original, err := codec.Marshal(messages[2])
			require.NoError(t, err)
			policy := contexty.OutputPolicy{
				Identity: contexty.Descriptor{ID: "host/output", Revision: "1"},
				Project: func(_ context.Context, in contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
					state := in.Payload.History[2].Extensions[0].(contexty.OpaqueState)
					switch edit {
					case "dependency":
						in.Payload.History[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "changed"}}
					case "payload":
						state.Payload = acceptanceOpaquePayload{Bytes: []byte("replacement")}
					case "rebind":
						state.Binding.Required = nil
						state.Binding.Prefix = nil
						state.Binding.Boundary = ""
					case "placement":
						state.Placement.AfterPart = -1
					}
					in.Payload.History[2].Extensions[0] = state
					return in.Payload, nil
				},
			}
			// Act: even the explicit drop policy cannot approve state-byte or binding edits.
			mode := contexty.OpaqueDropInvalid
			if edit == "dependency" {
				mode = contexty.OpaqueFailClosed
			}
			_, err = acceptanceOpaqueEngine(
				codec,
				profile,
				mode,
				contexty.WithOutputPolicy(policy),
			).CompileSnapshot(t.Context(), contexty.CompileRequest{CompilationID: "opaque-boundary", History: messages})
			// Assert.
			if edit == "dependency" {
				require.ErrorIs(t, err, contexty.ErrOpaqueStateInvalidated)
			} else {
				require.ErrorIs(t, err, contexty.ErrInvalidOpaqueState)
			}
			after, encodeErr := codec.Marshal(messages[2])
			require.NoError(t, encodeErr)
			require.Equal(t, original, after)
		})
	}
}

func TestAcceptance_OpaqueSelectionRequiresDependenciesOrExplicitDrop(t *testing.T) {
	for _, mode := range []contexty.OpaqueInvalidationMode{contexty.OpaqueFailClosed, contexty.OpaqueDropInvalid} {
		t.Run(string(mode), func(t *testing.T) {
			// Arrange: branch keeps state but deliberately omits the first bound input.
			messages, codec, profile := acceptanceOpaqueFixture(t)
			original, err := codec.Marshal(messages[2])
			require.NoError(t, err)
			selectPolicy := contexty.SelectionPolicy{
				Identity: contexty.Descriptor{ID: "host/branch", Revision: "1"},
				Select: func(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
					choices := make([]contexty.SelectionChoice, 0, len(candidates))
					for _, candidate := range candidates {
						if candidate.Ref.ID != "a" {
							choices = append(choices, contexty.SelectionChoice{Ref: candidate.Ref})
						}
					}
					return choices, nil
				},
			}
			// Act.
			result, err := acceptanceOpaqueEngine(
				codec,
				profile,
				mode,
				contexty.WithSelectionPolicy(selectPolicy),
				contexty.WithCompileRecording(fixtureRecordProfile()),
			).CompileSnapshot(t.Context(), contexty.CompileRequest{CompilationID: "opaque-boundary", History: messages})
			// Assert: no old signature leaves the branch and the canonical input stays immutable.
			if mode == contexty.OpaqueFailClosed {
				require.ErrorIs(t, err, contexty.ErrOpaqueStateInvalidated)
			} else {
				require.NoError(t, err)
				require.Empty(t, result.Payload.History[1].Extensions)
				require.NotNil(t, result.Manifest.Outputs[0].OpaqueState)
				require.Len(t, result.Manifest.Outputs[0].OpaqueState.Dropped, 1)
				require.Equal(t, "signature", result.Manifest.Outputs[0].OpaqueState.Dropped[0].StateID)
			}
			after, encodeErr := codec.Marshal(messages[2])
			require.NoError(t, encodeErr)
			require.Equal(t, original, after)
		})
	}
}

func TestAcceptance_OpaqueAcceptedReplayPinsEncodingAndPolicy(t *testing.T) {
	// Arrange: issued state includes arbitrary bytes and explicit context bindings.
	messages, codec, profile := acceptanceOpaqueFixture(t)
	calls := 0
	output := contexty.OutputPolicy{
		Identity: contexty.Descriptor{ID: "host/output", Revision: "1"},
		Project: func(_ context.Context, in contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
			calls++
			return in.Payload, nil
		},
	}
	engine := acceptanceOpaqueEngine(
		codec,
		profile,
		contexty.OpaqueFailClosed,
		contexty.WithOutputPolicy(output),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "1"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	result, err := engine.CompileSnapshot(
		t.Context(),
		contexty.CompileRequest{CompilationID: "opaque-replay", History: messages},
	)
	require.NoError(t, err)
	accepted, err := result.Record.Accept("host/accepted")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(accepted.Manifest)
	require.NoError(t, err)
	// Act.
	replayed, err := contexty.Replay(t.Context(), accepted, expected, codec)
	// Assert: adapter projection never reruns, including bytes and placement/bindings.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, result.Payload.History, replayed.Outputs[0].Segments[string(contexty.SegmentHistory)])
	changed := expected
	changed.Encoding.Revision = "different"
	_, err = contexty.Replay(t.Context(), accepted, changed, codec)
	require.Error(t, err)
	changed = expected
	config := *expected.CompileConfiguration.OpaqueState
	config.Policy.Revision = "different"
	changed.CompileConfiguration.OpaqueState = &config
	_, err = contexty.Replay(t.Context(), accepted, changed, codec)
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestAcceptance_OpaquePrefixRejectsStaleWireEvidence(t *testing.T) {
	// Arrange: the adapter explicitly authorizes the current host state and its bound inputs.
	messages, codec, profile := acceptanceOpaqueFixture(t)
	recipe := fixturePrefixRecipe()
	recipe.Codec = codec
	recipe.Boundaries = []contexty.PrefixBoundary{{ID: "state-prefix", AfterMessageID: "state"}}
	recipe.Authorize = func(_ context.Context, _ contexty.Message) error {
		return contexty.ValidateOpaqueState(messages, codec, profile)
	}
	previous, err := contexty.BuildPrefixManifest(t.Context(), messages, recipe)
	require.NoError(t, err)
	confirmation := contexty.PrefixWireConfirmation{
		BoundaryID:     "state-prefix",
		SemanticDigest: previous.Boundaries[0].Digest,
		Renderer:       recipe.Renderer,
		WireDigest:     previous.Boundaries[0].Messages[0].Content.Digest,
	}
	initial, err := contexty.DiagnosePrefix(t.Context(), messages, recipe, &previous)
	require.NoError(t, err)
	_, err = contexty.WithPrefixWireConfirmation(initial, confirmation)
	require.NoError(t, err)
	// Act: host supplies a freshly issued state, changing bytes but retaining declared bindings.
	owned := make([]contexty.Message, len(messages))
	for i := range messages {
		owned[i] = messages[i].Clone()
	}
	state := owned[2].Extensions[0].(contexty.OpaqueState)
	state.Payload = acceptanceOpaquePayload{Bytes: []byte("fresh signed bytes")}
	owned[2].Extensions[0] = state
	current, err := contexty.DiagnosePrefix(t.Context(), owned, recipe, &previous)
	// Assert: semantic evidence changes and the former adapter wire assertion cannot migrate.
	require.NoError(t, err)
	require.Len(t, current.Invalidations, 1)
	require.Contains(t, current.Invalidations[0].Reasons, contexty.PrefixContentChanged)
	require.Empty(t, current.WireConfirmations)
	_, err = contexty.WithPrefixWireConfirmation(current, confirmation)
	require.ErrorIs(t, err, contexty.ErrPrefixWireMismatch)
	changedRecipe := recipe
	changedRecipe.Encoding.Revision = "encoding-2"
	changed, err := contexty.DiagnosePrefix(t.Context(), messages, changedRecipe, &previous)
	require.NoError(t, err)
	require.Contains(t, changed.Invalidations[0].Reasons, contexty.PrefixCodecChanged)
	changedRecipe = recipe
	changedRecipe.Policy.Revision = "authorization-2"
	changed, err = contexty.DiagnosePrefix(t.Context(), messages, changedRecipe, &previous)
	require.NoError(t, err)
	require.Contains(t, changed.Invalidations[0].Reasons, contexty.PrefixPolicyChanged)
}

func TestAcceptance_OpaqueCompileTransformInvalidation(t *testing.T) {
	cases := []struct {
		name      string
		transform contexty.SegmentFormatter
		valid     bool
	}{
		{"dependent text", func(_ context.Context, m []contexty.Message) ([]contexty.Message, error) {
			m[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "changed"}}
			return m, nil
		}, false},
		{"reorder", func(_ context.Context, m []contexty.Message) ([]contexty.Message, error) {
			m[0], m[1] = m[1], m[0]
			return m, nil
		}, false},
		{"insert", func(_ context.Context, m []contexty.Message) ([]contexty.Message, error) {
			return slices.Insert(m, 1, fixtureRollingText("new", "insert")), nil
		}, false},
		{
			"truncate",
			func(_ context.Context, m []contexty.Message) ([]contexty.Message, error) { return m[1:], nil },
			false,
		},
		{"summarize", func(_ context.Context, m []contexty.Message) ([]contexty.Message, error) {
			return append([]contexty.Message{fixtureRollingText("summary", "summary of dependencies")}, m[2:]...), nil
		}, false},
		{"outside scope", func(_ context.Context, m []contexty.Message) ([]contexty.Message, error) {
			m[3].Parts = []contexty.ContentPart{contexty.TextPart{Text: "new tail"}}
			return m, nil
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: host transformations run before the final state validation boundary.
			messages, codec, profile := acceptanceOpaqueFixture(t)
			trace := fixtureTraceProfile()
			trace.Codec = codec
			trace.Labels.Registry = codec.Extensions
			trace.Labels.Policy = acceptanceOpaquePreserveLabels{}
			trace.Mapping = acceptanceOpaqueTransformMapping(codec)
			engine := acceptanceOpaqueEngine(
				codec,
				profile,
				contexty.OpaqueFailClosed,
				contexty.WithSegmentFormatter(contexty.SegmentHistory, tc.transform),
				contexty.WithTraceProfile(trace),
			)
			// Act.
			result, err := engine.CompileSnapshot(
				t.Context(),
				contexty.CompileRequest{CompilationID: "opaque-transforms", History: messages},
			)
			// Assert: invalid output is atomic; outside-scope changes retain exact state bytes.
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, messages[2].Extensions, result.Payload.History[2].Extensions)
			} else {
				require.ErrorIs(t, err, contexty.ErrOpaqueStateInvalidated)
				require.Empty(t, result.Payload.History)
			}
			require.Equal(t, "first", messages[0].TextContent())
			require.Equal(t, "tail", messages[3].TextContent())
		})
	}
}

// acceptanceOpaquePreserveLabels is a host decision to carry unchanged fixture state.
type acceptanceOpaquePreserveLabels struct{}

func (acceptanceOpaquePreserveLabels) ProjectLabels(
	_ context.Context,
	_ []contexty.Message,
	output contexty.Message,
	_ contexty.Descriptor,
) (contexty.LabelDecision, error) {
	return contexty.LabelDecision{Extensions: output.Extensions, Upgrade: false, DecisionRef: ""}, nil
}

// acceptanceOpaqueCounter models an adapter with an explicitly known unit cost.
type acceptanceOpaqueCounter struct{}

func (acceptanceOpaqueCounter) Estimate(_ context.Context, m []contexty.Message) (int, error) {
	return len(m), nil
}
func (acceptanceOpaqueCounter) EstimatePerMessage(_ context.Context, m []contexty.Message) ([]int, error) {
	costs := make([]int, len(m))
	for i := range costs {
		costs[i] = 1
	}
	return costs, nil
}

func TestAcceptance_OpaqueRealSummaryInvalidatesRetainedState(t *testing.T) {
	// Arrange: only state is retained; the real budget strategy summarizes its dependencies.
	messages, codec, profile := acceptanceOpaqueFixture(t)
	calls := 0
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:    contexty.EffectiveInputBudget(2),
			Retention: contexty.RetentionPolicy{MessageIDs: []string{"state"}},
			Summarizer: stubSummarizer(
				func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
					calls++
					require.NotEmpty(t, request.Messages)
					require.Equal(t, 1, request.MaxTokens)
					return fixtureRollingText("summary", "local summary"), nil
				},
			),
		},
		acceptanceOpaqueCounter{},
	)
	trace := fixtureTraceProfile()
	trace.Codec = codec
	trace.Labels.Registry = codec.Extensions
	trace.Labels.Policy = acceptanceOpaquePreserveLabels{}
	engine := acceptanceOpaqueEngine(
		codec,
		profile,
		contexty.OpaqueFailClosed,
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithTraceProfile(trace),
	)
	// Act.
	result, err := engine.CompileSnapshot(
		t.Context(),
		contexty.CompileRequest{CompilationID: "opaque-real-summary", History: messages},
	)
	// Assert: local summary cannot validate a signature over the original messages.
	require.Equal(t, 1, calls)
	require.ErrorIs(t, err, contexty.ErrOpaqueStateInvalidated)
	require.Empty(t, result.Payload.History)
	require.Equal(t, "first", messages[0].TextContent())
	require.Len(t, messages[2].Extensions, 1)
}

func TestAcceptance_OpaqueDropCascadesDeterministically(t *testing.T) {
	// Arrange: state B binds the exact carrier of state A, which binds an omitted input.
	messages, codec, profile := acceptanceOpaqueFixture(t)
	messages = messages[:3]
	firstRef, err := contexty.MessageContentRef(messages[0], codec)
	require.NoError(t, err)
	stateA := messages[2].Extensions[0].(contexty.OpaqueState)
	stateA.ID = "a-state"
	stateA.Binding = contexty.OpaqueBinding{Profile: profile, Required: []contexty.ContentRef{firstRef}}
	messages[1].Extensions = []contexty.Extension{stateA}
	carrierRef, err := contexty.MessageContentRef(messages[1], codec)
	require.NoError(t, err)
	stateB := stateA.CloneExtension().(contexty.OpaqueState)
	stateB.ID = "b-state"
	stateB.Binding.Required = []contexty.ContentRef{carrierRef}
	messages[2].Extensions = []contexty.Extension{stateB}
	policy := contexty.SelectionPolicy{
		Identity: contexty.Descriptor{ID: "host/drop-ancestor", Revision: "1"},
		Select: func(_ context.Context, c []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
			var choices []contexty.SelectionChoice
			for _, candidate := range c {
				if candidate.Ref.ID != "a" {
					choices = append(choices, contexty.SelectionChoice{Ref: candidate.Ref})
				}
			}
			return choices, nil
		},
	}
	engine := acceptanceOpaqueEngine(
		codec,
		profile,
		contexty.OpaqueDropInvalid,
		contexty.WithSelectionPolicy(policy),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "1"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	request := contexty.CompileRequest{CompilationID: "opaque-cascade", History: messages}
	// Act: repeat identical host choices to detect map-order-dependent drop evidence.
	var previous string
	for range 8 {
		result, compileErr := engine.CompileSnapshot(t.Context(), request)
		require.NoError(t, compileErr)
		// Assert: invalidation reaches a fixed point and evidence is stable.
		require.Empty(t, result.Payload.History[0].Extensions)
		require.Empty(t, result.Payload.History[1].Extensions)
		dropped := result.Manifest.Outputs[0].OpaqueState.Dropped
		require.Len(t, dropped, 2)
		require.Equal(t, "a-state", dropped[0].StateID)
		require.Equal(t, "b-state", dropped[1].StateID)
		if previous != "" {
			require.Equal(t, previous, result.Manifest.Digest)
		}
		previous = result.Manifest.Digest
		accepted, acceptErr := result.Record.Accept("host/accepted")
		require.NoError(t, acceptErr)
		expected, expectErr := contexty.ReplayExpectationFor(accepted.Manifest)
		require.NoError(t, expectErr)
		replayed, replayErr := contexty.Replay(t.Context(), accepted, expected, codec)
		require.NoError(t, replayErr)
		for i, message := range result.Payload.History {
			issued, issuedErr := codec.Marshal(message)
			require.NoError(t, issuedErr)
			replayedBytes, replayedErr := codec.Marshal(
				replayed.Outputs[0].Segments[string(contexty.SegmentHistory)][i],
			)
			require.NoError(t, replayedErr)
			require.Equal(t, issued, replayedBytes)
		}
	}
	require.Len(t, messages[1].Extensions, 1)
	require.Len(t, messages[2].Extensions, 1)
}

func acceptanceOpaqueTransformMapping(codec contexty.JSONSerializer) contexty.TraceMapping {
	return func(_ context.Context, _ string, inputs, outputs []contexty.Message) (map[string][]contexty.ContentRef, error) {
		mapping := make(map[string][]contexty.ContentRef)
		for _, output := range outputs {
			existing := false
			for _, input := range inputs {
				if input.ID == output.ID {
					existing = true
					break
				}
			}
			if existing {
				continue
			}
			for _, input := range inputs {
				ref, refErr := contexty.MessageContentRef(input, codec)
				if refErr != nil {
					return nil, refErr
				}
				mapping[output.ID] = append(mapping[output.ID], ref)
			}
		}
		return mapping, nil
	}
}

func TestAcceptance_OpaqueOutputPolicyCannotReverseStateOrder(t *testing.T) {
	for _, mode := range []contexty.OpaqueInvalidationMode{contexty.OpaqueFailClosed, contexty.OpaqueDropInvalid} {
		for _, channel := range []string{"output policy", "formatter"} {
			t.Run(string(mode)+"/"+channel, func(t *testing.T) {
				// Arrange: one carrier has two ordered host items at the same placement.
				messages, codec, profile := acceptanceOpaqueFixture(t)
				first := messages[2].Extensions[0].(contexty.OpaqueState)
				second := first.CloneExtension().(contexty.OpaqueState)
				second.ID = "compaction"
				second.Payload = acceptanceOpaquePayload{Bytes: []byte("external compaction")}
				messages[2].Extensions = append(messages[2].Extensions, second)
				option := contexty.WithOutputPolicy(
					contexty.OutputPolicy{
						Identity: contexty.Descriptor{ID: "host/reorder", Revision: "1"},
						Project: func(_ context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
							slices.Reverse(input.Payload.History[2].Extensions)
							return input.Payload, nil
						},
					},
				)
				trace := fixtureTraceProfile()
				trace.Codec = codec
				trace.Labels.Registry = codec.Extensions
				trace.Labels.Policy = acceptanceOpaquePreserveLabels{}
				if channel == "formatter" {
					option = contexty.WithSegmentFormatter(
						contexty.SegmentHistory,
						func(_ context.Context, m []contexty.Message) ([]contexty.Message, error) {
							slices.Reverse(m[2].Extensions)
							return m, nil
						},
					)
				}
				// Act: removal permission does not grant permission to reorder opaque wire items.
				result, err := acceptanceOpaqueEngine(
					codec,
					profile,
					mode,
					contexty.WithTraceProfile(trace),
					option,
				).CompileSnapshot(t.Context(), contexty.CompileRequest{CompilationID: "opaque-state-order", History: messages})
				// Assert: no ambiguous wire order or mutated source escapes the boundary.
				require.ErrorIs(t, err, contexty.ErrInvalidOpaqueState)
				require.Zero(t, result)
				require.Equal(t, "signature", messages[2].Extensions[0].(contexty.OpaqueState).ID)
				require.Equal(t, "compaction", messages[2].Extensions[1].(contexty.OpaqueState).ID)
			})
		}
	}
}
