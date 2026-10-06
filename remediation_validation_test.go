package contexty_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestRemediation_EventIdentityAfterTrimAndRetry(t *testing.T) {
	// Arrange: repeated text in distinct logical turns with a prompt-safe projection.
	ctx := context.Background()
	engine := contexty.NewEngine()
	policy := contexty.NewStableMessageIdentityPolicy("chat")
	turn := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "yes")).
		WithPromptSafe(contexty.TextMessage(contexty.RoleUser, "safe yes"))
	var history []contexty.Message
	var ids []string
	for i := range 3 {
		request := contexty.CompileRequest{
			TurnID:                 fmt.Sprintf("turn-%d", i),
			History:                history,
			CurrentTurn:            &turn,
			IdentityPolicy:         policy,
			RequireDurableIdentity: true,
		}
		// Act: compile, retry the same logical event with a different retained history.
		result, err := engine.CompileSnapshot(ctx, request)
		require.NoError(t, err)
		retry := request
		retry.History = nil
		repeated, err := engine.CompileSnapshot(ctx, retry)
		// Assert: stable event ID and raw/prompt link, independent of history size.
		require.NoError(t, err)
		id := result.Source.CurrentTurn.Raw.ID
		require.Equal(t, id, repeated.Source.CurrentTurn.Raw.ID)
		require.Equal(t, id, result.Source.CurrentTurn.PromptSafe.ID)
		require.Equal(t, id, result.Payload.History[len(result.Payload.History)-1].ID)
		require.NotContains(t, ids, id)
		ids = append(ids, id)
		persisted := result.Writeback.Snapshot.Segment(contexty.SegmentHistory)
		history = persisted[len(persisted)-1:] // Exercise the old collision condition.
	}
}

func TestRemediation_PendingOrdinalsAndExplicitIdentity(t *testing.T) {
	// Arrange: two identical pending events plus current turn under one host turn.
	policy := contexty.NewStableMessageIdentityPolicy("host")
	turn := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "same"))
	request := contexty.CompileRequest{
		TurnID:  "turn",
		History: []contexty.Message{fixtureRollingText("archived-event", "same")},
		Pending: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "same"),
			contexty.TextMessage(contexty.RoleUser, "same"),
		},
		CurrentTurn:            &turn,
		IdentityPolicy:         policy,
		RequireDurableIdentity: true,
	}
	// Act.
	first, _, err := request.Normalize()
	require.NoError(t, err)
	request.History = nil
	second, _, err := request.Normalize()
	// Assert: pending ordinal independent of history offset, IDs distinct and explicit ID retained.
	require.NoError(t, err)
	require.Equal(t, "archived-event", first.History[0].ID)
	require.Equal(t, first.Pending[0].ID, second.Pending[0].ID)
	require.Equal(t, first.Pending[1].ID, second.Pending[1].ID)
	require.NotEqual(t, first.Pending[0].ID, first.Pending[1].ID)
	require.NotEqual(t, first.Pending[0].ID, first.CurrentTurn.Raw.ID)
}

func TestRemediation_MissingEventIdentityFails(t *testing.T) {
	for _, request := range []contexty.CompileRequest{
		{IdentityPolicy: contexty.NewStableMessageIdentityPolicy("host"), CurrentTurn: new(contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "active")))},
		{TurnID: "turn", IdentityPolicy: contexty.NewStableMessageIdentityPolicy("host"), History: []contexty.Message{contexty.TextMessage(contexty.RoleUser, "historical")}},
	} {
		// Arrange: either missing TurnID or historical input without durable event ID.
		// Act / Assert: no positional/content fallback.
		result, err := contexty.NewEngine().CompileSnapshot(context.Background(), request)
		require.ErrorIs(t, err, contexty.ErrMissingEventIdentity)
		require.Zero(t, result)
	}
}

func TestRemediation_EventIdentityContentRevision(t *testing.T) {
	// Arrange: same logical event, different image content; different turns same content.
	policy := contexty.NewStableMessageIdentityPolicy("host")
	identity := contexty.MessageIdentityContext{TurnID: "turn", CurrentTurn: true, Ordinal: 0}
	a := contexty.Message{Role: contexty.RoleUser, Parts: []contexty.ContentPart{contexty.ImagePart{URL: "a"}}}
	b := contexty.Message{Role: contexty.RoleUser, Parts: []contexty.ContentPart{contexty.ImagePart{URL: "b"}}}
	// Act.
	idA, err := policy.ResolveMessageID(identity, a)
	require.NoError(t, err)
	idB, err := policy.ResolveMessageID(identity, b)
	require.NoError(t, err)
	identity.TurnID = "next-turn"
	nextID, err := policy.ResolveMessageID(identity, a)
	require.NoError(t, err)
	a.ID, b.ID = idA, idB
	refA, err := contexty.MessageContentRef(a, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	refB, err := contexty.MessageContentRef(b, contexty.DefaultJSONSerializer())
	// Assert: logical identity and content revision are separate contracts.
	require.NoError(t, err)
	require.Equal(t, idA, idB)
	require.NotEqual(t, idA, nextID)
	require.NotEqual(t, refA, refB)
}

func TestRemediation_ExplicitEmptyOverrides(t *testing.T) {
	// Arrange: each persisted segment has content; nil inherits only selected segments.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	var deltas []contexty.ConversationDelta
	for _, segment := range []contexty.SegmentName{contexty.SegmentSystem, contexty.SegmentHistory, contexty.SegmentMemory, contexty.SegmentTools} {
		deltas = append(
			deltas,
			contexty.ConversationDelta{
				Operation: contexty.DeltaAppendMessages,
				Segment:   segment,
				Messages:  []contexty.Message{fixtureRollingText(string(segment), "stored")},
			},
		)
	}
	require.NoError(t, store.CommitState(ctx, "conversation", 0, deltas...))
	engine := contexty.NewEngine(contexty.WithStateStore(store), contexty.WithConversationID("conversation"))
	request := contexty.CompileRequest{System: []contexty.Message{}, Memory: []contexty.Message{}}
	// Act.
	result, err := engine.Compile(ctx, request)
	require.NoError(t, err)
	frozen := request.Freeze()
	normalized, _, err := request.Normalize()
	// Assert: explicit empty stays empty, nil history inherits, tools from request, store untouched.
	require.NoError(t, err)
	require.Empty(t, result.Payload.System)
	require.Empty(t, result.Payload.Memory)
	require.Len(t, result.Payload.History, 1)
	require.Empty(t, result.Payload.Tools)
	require.NotNil(t, result.Source.System)
	require.NotNil(t, frozen.System)
	require.NotNil(t, normalized.System)
	require.Nil(t, normalized.History)
	state, err := store.LoadState(ctx, "conversation")
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Version())
	require.Len(t, state.Segment(contexty.SegmentSystem), 1)
}

func TestRemediation_ActiveRoleProjectionScope(t *testing.T) {
	// Arrange: hooks and formatter operate on historical content; role applies to all prompt messages.
	calls := 0
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithRoleProjectionPolicy(
			contexty.RoleProjectionFunc(func(message contexty.Message) (contexty.Role, error) {
				calls++
				message.Parts[0] = contexty.TextPart{Text: "mutated"}
				return contexty.RoleAssistant, nil
			}),
		),
		contexty.WithSegmentFormatter(
			contexty.SegmentHistory,
			func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
				require.Len(t, messages, 1)
				require.Equal(t, "history", messages[0].ID)
				return messages, nil
			},
		),
	)
	turn := contexty.NewCurrentTurn(fixtureRollingText("current", "raw")).
		WithPromptSafe(fixtureRollingText("current", "safe"))
	request := contexty.CompileRequest{
		CompilationID: "active-role-scope",
		History:       []contexty.Message{fixtureRollingText("history", "history")},
		Pending:       []contexty.Message{fixtureRollingText("pending", "pending")},
		CurrentTurn:   &turn,
	}
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: roles/transform evidence shaped; source/persistence unchanged and callback mutation discarded.
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.NoError(t, result.Lineage.Validate())
	roleIDs := make(map[string]bool)
	for _, record := range result.Lineage.Records {
		if record.Transform.ID != "role" {
			continue
		}
		for index, output := range record.Outputs {
			roleIDs[output.ID] = true
			require.Equal(t, output.ID, record.Inputs[index].ID)
			require.NotEqual(t, output, record.Inputs[index])
		}
	}
	require.Equal(t, map[string]bool{"history": true, "pending": true, "current": true}, roleIDs)
	for _, message := range result.Payload.History {
		require.Equal(t, contexty.RoleAssistant, message.Role)
		require.NotEqual(t, "mutated", message.TextContent())
		require.Contains(
			t,
			result.Transformations[message.ID],
			contexty.TransformRecord{Action: contexty.ActionFormatted, Reason: contexty.ReasonRoleProjection},
		)
	}
	require.Equal(t, contexty.RoleUser, result.Source.CurrentTurn.Raw.Role)
	require.Equal(t, contexty.RoleUser, result.Writeback.Snapshot.Segment(contexty.SegmentHistory)[1].Role)
}

func TestRemediation_CurrentTurnAndStateConfiguration(t *testing.T) {
	for _, scenario := range []string{"prompt-only", "unknown-empty-policy", "missing-conversation-id"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: invalid intent must fail before store load.
			reads := 0
			store := fixtureComponentStoreProbe{
				ConversationStateStore: contexty.NewMemoryConversationStateStore(),
				reads:                  &reads,
			}
			options := []contexty.EngineOption{
				contexty.WithStateStore(store),
				contexty.WithConversationID("conversation"),
			}
			request := contexty.CompileRequest{}
			want := contexty.ErrInvalidCurrentTurn
			switch scenario {
			case "prompt-only":
				request.CurrentTurn = &contexty.CurrentTurn{PromptSafe: fixtureRollingText("active", "safe")}
			case "unknown-empty-policy":
				request.CurrentTurn = &contexty.CurrentTurn{Persistence: "typo"}
				want = contexty.ErrInvalidCurrentTurnPersistencePolicy
			case "missing-conversation-id":
				options = []contexty.EngineOption{contexty.WithStateStore(store)}
				want = contexty.ErrInvalidCompileConfiguration
			}
			// Act / Assert.
			result, err := contexty.NewEngine(options...).Compile(context.Background(), request)
			require.ErrorIs(t, err, want)
			require.Zero(t, result)
			require.Zero(t, reads)
		})
	}
	// Arrange: stateless entrypoint explicitly bypasses incomplete stateful configuration.
	reads := 0
	store := fixtureComponentStoreProbe{
		ConversationStateStore: contexty.NewMemoryConversationStateStore(),
		reads:                  &reads,
	}
	// Act / Assert: correct zero current turn is absence.
	result, err := contexty.NewEngine(contexty.WithStateStore(store)).
		CompileSnapshot(context.Background(), contexty.CompileRequest{CurrentTurn: &contexty.CurrentTurn{}})
	require.NoError(t, err)
	require.Nil(t, result.Source.CurrentTurn)
	require.Zero(t, reads)
}

func TestRemediation_DeferredEnumPreflight(t *testing.T) {
	for flags := range 8 {
		recording, stateless, resourceAware := flags&1 != 0, flags&2 != 0, flags&4 != 0
		for _, field := range []string{"segment", "policy"} {
			t.Run(
				fmt.Sprintf("record=%v/snapshot=%v/resource=%v/%s", recording, stateless, resourceAware, field),
				func(t *testing.T) {
					// Arrange: otherwise valid message/resource resolver with typo configuration.
					reads, calls := 0, 0
					block := contexty.DeferredBlock{Name: "selected"}
					if resourceAware {
						resolver, request, _ := fixtureResourceFixture(t)
						block = fixtureAdapterResourceBlock(t, resolver, request)
					}
					block.Resolve = func(context.Context) (contexty.DeferredResult, error) { calls++; return contexty.DeferredResult{}, nil }
					if field == "segment" {
						block.Segment = "memroy"
					} else {
						block.MergePolicy = "replcae"
					}
					store := fixtureComponentStoreProbe{
						ConversationStateStore: contexty.NewMemoryConversationStateStore(),
						reads:                  &reads,
					}
					options := []contexty.EngineOption{
						contexty.WithDeferredBlocks(block),
						contexty.WithStateStore(store),
						contexty.WithConversationID("conversation"),
					}
					if recording {
						options = append(
							options,
							contexty.WithTraceProfile(fixtureTraceProfile()),
							contexty.WithCompileRecording(
								fixtureBindings(
									fixtureRecordProfile(),
									fixtureBinding(contexty.RecordingResolver, "", "", 0),
								),
							),
						)
					}
					engine := contexty.NewEngine(options...)
					compile := engine.Compile
					if stateless {
						compile = engine.CompileSnapshot
					}
					// Act / Assert: no resolver/store side effect, no payload, common config error.
					result, err := compile(context.Background(), contexty.CompileRequest{CompilationID: "test"})
					require.ErrorIs(t, err, contexty.ErrInvalidCompileConfiguration)
					require.Zero(t, result)
					require.Zero(t, calls)
					require.Zero(t, reads)
				},
			)
		}
	}
	// Arrange: documented empty defaults still select memory+append.
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(
			contexty.DeferredBlock{Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{fixtureRollingText("deferred", "new")}}, nil
			}},
		),
	)
	// Act / Assert.
	result, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{Memory: []contexty.Message{fixtureRollingText("existing", "old")}},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"existing", "deferred"}, fixtureMessageIDs(result.Payload.Memory))
}

func TestRemediation_MaterializationPartParity(t *testing.T) {
	call := contexty.ToolCallPart{ID: "call", Name: "tool"}
	toolResult := contexty.ToolResultPart{ToolCallID: "call"}
	invalidMedia := contexty.MediaPart{MIMEType: "broken"}
	var typedNil *contexty.TextPart
	cases := []contexty.ContentPart{call, &call, toolResult, &toolResult, invalidMedia, &invalidMedia, typedNil}
	for index, part := range cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			// Arrange: same invalid representations through compile and resource ports.
			policy := contexty.ArtifactMaterializationPolicy{
				Identity: contexty.Descriptor{ID: "host", Revision: "v1"},
				Materialize: func(context.Context, contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
					return contexty.ArtifactRepresentation{
						Role:  contexty.RoleAssistant,
						Parts: []contexty.ContentPart{part},
					}, nil
				},
			}
			engine := contexty.NewEngine(contexty.WithArtifactMaterialization(policy))
			resolver, request, _ := fixtureResourceFixture(t)
			resolver.Materialization = &policy
			// Act / Assert: canonical validation rejects both entrypoints without panic/result.
			result, err := engine.CompileSnapshot(
				context.Background(),
				contexty.CompileRequest{
					Artifacts: []contexty.ContextArtifact{
						contexty.NewMemoryBlock("data", contexty.TextPayload("data")).ContextArtifact,
					},
				},
			)
			require.ErrorIs(t, err, contexty.ErrInvalidArtifactMaterialization)
			require.Zero(t, result)
			resource, err := resolver.Resolve(context.Background(), request)
			require.ErrorIs(t, err, contexty.ErrInvalidArtifactMaterialization)
			require.Zero(t, resource)
		})
	}
	// Arrange: pointers behave exactly like values for AST inspection and cloning.
	text := contexty.TextPart{Text: "text"}
	message := contexty.Message{Parts: []contexty.ContentPart{&text, &call, &toolResult}}
	// Act / Assert.
	require.Equal(t, "text", message.TextContent())
	require.True(t, message.HasToolCalls())
	require.Len(t, message.ToolCallParts(), 1)
	require.Len(t, message.ToolResultParts(), 1)
	require.True(t, contexty.MessageEqual(message, message.Clone()))
	_, err := contexty.MarshalParts([]contexty.ContentPart{typedNil})
	require.ErrorIs(t, err, contexty.ErrInvalidContentPart)
}

func TestRemediation_NamedViewRegistration(t *testing.T) {
	for _, names := range [][]string{{string(contexty.ViewLLMXML)}, {string(contexty.ViewFlatClassifier)}, {"unique", "unique"}} {
		// Arrange: reserved or duplicate registrations, each with observable formatter.
		calls := 0
		var options []contexty.EngineOption
		for _, name := range names {
			options = append(
				options,
				contexty.WithNamedView(
					name,
					contexty.ViewConfiguration{
						Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
							calls++
							return messages, nil
						},
					},
				),
			)
		}
		engine := contexty.NewEngine(options...)
		// Act / Assert: configuration error is returned before rendering callback.
		output, err := engine.RenderView(context.Background(), contexty.EmptySnapshot(), names[0])
		require.ErrorIs(t, err, contexty.ErrInvalidCompileConfiguration)
		require.Empty(t, output)
		require.Zero(t, calls)
		result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{})
		require.ErrorIs(t, err, contexty.ErrInvalidCompileConfiguration)
		require.Zero(t, result)
	}
}

func TestRemediation_ProvenanceDecoderInvariants(t *testing.T) {
	// Arrange / Assert: invalid registration is a programming error, not deferred panic.
	for _, typeID := range []string{"", " ", " user"} {
		require.Panics(t, func() {
			contexty.NewProvenanceRegistry().
				Register(typeID, func([]byte) (contexty.Provenance, error) { return contexty.UserProvenance{}, nil })
		})
	}
	require.Panics(t, func() { contexty.NewProvenanceRegistry().Register("user", nil) })
	callbackError := errors.New("callback failure")
	var typedNil *hostProvenance
	for _, scenario := range []string{"nil", "typed-nil", "wrong-type", "callback-error"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: decoder must keep discriminator and preserve host failures.
			registry := contexty.NewProvenanceRegistry()
			registry.Register("user", func([]byte) (contexty.Provenance, error) {
				switch scenario {
				case "nil":
					return nil, nil //nolint:nilnil // Deliberately invalid host decoder.
				case "typed-nil":
					return typedNil, nil
				case "wrong-type":
					return contexty.SystemProvenance{}, nil
				default:
					return nil, callbackError
				}
			})
			// Act.
			result, err := registry.Decode([]byte(`{"type_id":"user","payload":{}}`))
			// Assert: nil metadata/type changes cannot escape through the common codec either.
			if scenario == "callback-error" {
				require.ErrorIs(t, err, callbackError)
			} else {
				require.ErrorIs(t, err, contexty.ErrInvalidProvenance)
			}
			require.Nil(t, result)
			var message contexty.Message
			err = (contexty.JSONSerializer{Provenance: registry}).Unmarshal(
				[]byte(`{"role":"user","parts":[],"provenance":{"type_id":"user","payload":{}}}`),
				&message,
			)
			require.Error(t, err)
			require.Zero(t, message)
		})
	}
}

func TestRemediation_CurrentResourceCodecRevisions(t *testing.T) {
	// Arrange: unchanged topology but a changed current host revision.
	resource, codec := fixtureResolvedResource(t, true)
	wire, err := contexty.EncodeResolvedResource(context.Background(), resource, codec)
	require.NoError(t, err)
	calls := 0
	codec.Labels = contexty.NewExtensionRegistry()
	codec.Labels.Register(
		"fixture-label",
		func([]byte) (contexty.Extension, error) { calls++; return fixtureWireExtension{}, nil },
	)
	for index := range codec.Codecs {
		if codec.Codecs[index].Kind == contexty.CodecLabel {
			codec.Codecs[index].Descriptor.Revision = "changed"
		}
	}
	// Act / Assert: fail compatibility before decoder invocation, no inferred saved identity.
	restored, err := contexty.DecodeResolvedResource(context.Background(), wire, codec)
	require.ErrorIs(t, err, contexty.ErrReplayCodec)
	require.Zero(t, restored)
	require.Zero(t, calls)
}

func TestRemediation_LayerReplacementRetainsEntireGroup(t *testing.T) {
	// Arrange: same layer name in different templates and a two-message incoming group.
	existing := []contexty.Message{
		{ID: "old-a", Origin: &contexty.MessageOrigin{TemplateID: "a", LayerID: "persona"}},
		{ID: "keep-b", Origin: &contexty.MessageOrigin{TemplateID: "b", LayerID: "persona"}},
	}
	incoming := []contexty.Message{
		{ID: "new-a1", Origin: &contexty.MessageOrigin{TemplateID: "a", LayerID: "persona"}},
		{ID: "unscoped"},
		{ID: "new-a2", Origin: &contexty.MessageOrigin{TemplateID: "a", LayerID: "persona"}},
	}
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(contexty.DeferredBlock{
		Segment: contexty.SegmentMemory, MergePolicy: contexty.PolicyDeduplicateByLayer,
		Resolve: func(context.Context) (contexty.DeferredResult, error) {
			return contexty.DeferredResult{Messages: incoming}, nil
		},
	}))
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{Memory: existing})
	// Assert: namespace-specific replacement, all incoming messages/order retained.
	require.NoError(t, err)
	require.Equal(t, []string{"keep-b", "new-a1", "unscoped", "new-a2"}, fixtureMessageIDs(result.Payload.Memory))
}

func TestRemediation_ValidMaterializationOwnedParity(t *testing.T) {
	for _, media := range []bool{false, true} {
		var results []contexty.Message
		for _, pointer := range []bool{false, true} {
			// Arrange: equivalent valid value/pointer materialization.
			text := contexty.TextPart{Text: "safe"}
			data := contexty.MediaPart{MIMEType: "audio/wav", Data: []byte{1, 2}}
			var part contexty.ContentPart = text
			if pointer {
				part = &text
			}
			if media {
				part = data
				if pointer {
					part = &data
				}
			}
			policy := contexty.ArtifactMaterializationPolicy{Identity: contexty.Descriptor{ID: "host", Revision: "v1"},
				Materialize: func(context.Context, contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
					return contexty.ArtifactRepresentation{
						Role:  contexty.RoleUser,
						Parts: []contexty.ContentPart{part},
					}, nil
				}}
			resolver, request, _ := fixtureResourceFixture(t)
			resolver.Materialization = &policy
			profile := fixtureEstimateProfile()
			profile.Fallback = &contexty.EstimateFallback{
				Policy: contexty.Descriptor{ID: "fallback", Revision: "v1"},
				Tokens: 7,
			}
			var err error
			resolver.Reporter, err = contexty.NewEstimateReporter(
				contexty.CharTokenEstimator{},
				profile,
				contexty.DefaultJSONSerializer(),
			)
			require.NoError(t, err)
			request.Budget = contexty.EffectiveInputBudget(20)
			// Act: both paths take ownership before validation/output.
			compiled, err := contexty.NewEngine(contexty.WithArtifactMaterialization(policy)).
				CompileSnapshot(context.Background(),
					contexty.CompileRequest{
						Artifacts: []contexty.ContextArtifact{
							contexty.NewMemoryBlock("data", contexty.TextPayload("body")).ContextArtifact,
						},
					})
			require.NoError(t, err)
			resource, err := resolver.Resolve(context.Background(), request)
			require.NoError(t, err)
			// Assert: canonical value equality and no pointer/data alias leaks.
			require.Equal(t, compiled.Payload.Memory[0].Parts, resource.Message.Parts)
			text.Text = "changed"
			data.Data[0] = 99
			if media {
				require.Equal(t, byte(1), compiled.Payload.Memory[0].Parts[0].(contexty.MediaPart).Data[0])
				require.Equal(t, byte(1), resource.Message.Parts[0].(contexty.MediaPart).Data[0])
			}
			results = append(results, compiled.Payload.Memory[0])
		}
		require.Equal(t, results[0], results[1])
	}
}

type invalidCloneProvenance struct{ result contexty.Provenance }

func (invalidCloneProvenance) ProvenanceType() string                 { return "host/invalid-clone" }
func (p invalidCloneProvenance) CloneProvenance() contexty.Provenance { return p.result }

func TestRemediation_CurrentTurnProvenanceCloneParity(t *testing.T) {
	for _, result := range []contexty.Provenance{nil, (*contexty.UserProvenance)(nil), contexty.SystemProvenance{}} {
		for _, promptSafe := range []bool{false, true} {
			for _, stateful := range []bool{false, true} {
				// Arrange: an invalid host clone must not silently erase or replace metadata.
				turn := contexty.NewCurrentTurn(fixtureRollingText("active", "raw"))
				if promptSafe {
					turn = turn.WithPromptSafe(fixtureRollingText("active", "safe"))
					turn.PromptSafe.Provenance = invalidCloneProvenance{result: result}
				} else {
					turn.Raw.Provenance = invalidCloneProvenance{result: result}
				}
				engine := contexty.NewEngine()
				compile := engine.CompileSnapshot
				if stateful {
					compile = engine.Compile
				}
				// Act.
				compiled, err := compile(context.Background(), contexty.CompileRequest{CurrentTurn: &turn})
				// Assert: both entrypoints reject before successful source/output publication.
				require.ErrorIs(t, err, contexty.ErrInvalidProvenance)
				require.Zero(t, compiled)
			}
		}
	}
}

func TestRemediation_DeferredProvenanceCloneParity(t *testing.T) {
	for _, result := range []contexty.Provenance{nil, (*contexty.UserProvenance)(nil), contexty.SystemProvenance{}} {
		for _, durable := range []bool{false, true} {
			// Arrange: callback metadata is validated before taking ownership.
			message := fixtureRollingText("deferred", "raw")
			message.Provenance = invalidCloneProvenance{result: result}
			engine := contexty.NewEngine(contexty.WithDeferredBlocks(contexty.DeferredBlock{
				Resolve: func(context.Context) (contexty.DeferredResult, error) {
					return contexty.DeferredResult{Messages: []contexty.Message{message}}, nil
				},
			}))
			request := contexty.CompileRequest{RequireDurableIdentity: durable}
			// Act.
			compiled, err := engine.CompileSnapshot(context.Background(), request)
			// Assert: failed host cloning never produces successful replacement/empty metadata.
			require.ErrorIs(t, err, contexty.ErrInvalidProvenance)
			require.Zero(t, compiled)
		}
	}
}
