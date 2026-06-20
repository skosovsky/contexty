package contexty_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type formatterContextKey struct{}

func TestDoD_Task15_ToolPayloadRoundTripAndToolRoundValidation(t *testing.T) {
	t.Parallel()
	args, err := contexty.StructuredPayload(struct {
		Query string `json:"query"`
	}{Query: "typed"})
	require.NoError(t, err)

	assistant := contexty.Message{
		ID:   "assistant-call",
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{
				ID:        "call-1",
				Name:      "lookup",
				Arguments: args,
			},
		},
	}
	result := contexty.Message{
		ID:   "tool-result",
		Role: contexty.RoleTool,
		Parts: []contexty.ContentPart{
			contexty.ToolResultPart{
				ToolCallID: "call-1",
				Name:       "lookup",
				Payload: contexty.BinaryPayload(
					[]byte{0xde, 0xad, 0xbe, 0xef},
					"application/octet-stream",
				),
			},
		},
	}

	data, err := contexty.MarshalMessages(
		[]contexty.Message{assistant, result},
		contexty.DefaultMessageCodec(),
	)
	require.NoError(t, err)
	assert.Contains(t, string(data), "binary_hex")
	assert.NotContains(t, string(data), "<|")

	decoded, err := contexty.UnmarshalMessages(data, contexty.DefaultMessageCodec())
	require.NoError(t, err)
	require.Len(t, decoded, 2)
	payload := decoded[1].ToolResultParts()[0].Payload
	assert.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, payload.Binary)
	assert.Equal(t, "application/octet-stream", payload.MIMEType)

	round, err := contexty.ToolRoundFromMessages(decoded, 0)
	require.NoError(t, err)
	require.NoError(t, round.Validate())

	incomplete := contexty.ToolRound{Assistant: assistant}
	require.Error(t, incomplete.Validate())

	duplicateCall := contexty.ToolRound{
		Assistant: contexty.Message{
			ID:   "assistant-duplicate",
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{ID: "call-1", Name: "lookup", Arguments: args},
				contexty.ToolCallPart{ID: "call-1", Name: "lookup", Arguments: args},
			},
		},
	}
	require.Error(t, duplicateCall.Validate())

	unknownResult := contexty.ToolRound{
		Assistant: assistant,
		Results: []contexty.Message{{
			ID:   "tool-unknown",
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "missing", Payload: contexty.TextPayload("nope")},
			},
		}},
	}
	require.Error(t, unknownResult.Validate())

	emptyResult := contexty.ToolRound{
		Assistant: assistant,
		Results: []contexty.Message{{
			ID:    "tool-empty",
			Role:  contexty.RoleTool,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "not a tool result"}},
		}},
	}
	require.Error(t, emptyResult.Validate())

	duplicateResult := contexty.ToolRound{
		Assistant: assistant,
		Results: []contexty.Message{
			result,
			result,
		},
	}
	require.Error(t, duplicateResult.Validate())
}

func TestDoD_Task15_MessageCodecRoundTripsTypedExtensions(t *testing.T) {
	t.Parallel()
	codec := contexty.MessageCodec{
		Provenance: contexty.DefaultProvenanceRegistry(),
		Extensions: newTestExtensionRegistry(),
	}
	msg := contexty.Message{
		ID:    "with-extension",
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "typed"}},
		Extensions: []contexty.Extension{
			testExtension{Tenant: "acme", Score: 7},
		},
	}

	data, err := contexty.MarshalMessageJSON(msg, codec)
	require.NoError(t, err)
	decoded, err := contexty.UnmarshalMessageJSON(data, codec)
	require.NoError(t, err)

	require.Len(t, decoded.Extensions, 1)
	ext, ok := decoded.Extensions[0].(testExtension)
	require.True(t, ok)
	assert.Equal(t, "acme", ext.Tenant)
	assert.InEpsilon(t, float64(7), ext.Score, 0)
}

func TestDoD_Task15_ActorProjectionAndSourceRefsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	msg := contexty.Message{
		ID:   "domain-speaker",
		Role: contexty.RoleUser,
		Actor: &contexty.Actor{
			Kind:        "alert",
			ID:          "actor-1",
			DisplayName: "System alert",
			SourceRefs: []contexty.SourceRef{{
				Namespace:    "source",
				Kind:         "event",
				ID:           "evt-1",
				CheckpointID: "chk-1",
			}},
		},
		SourceRefs: []contexty.SourceRef{{
			Namespace:    "messages",
			Kind:         "external",
			ID:           "msg-1",
			CheckpointID: "stable-1",
			URI:          "urn:message:msg-1",
		}},
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "alert text"}},
	}
	codec := contexty.ConversationCodec{Provenance: contexty.DefaultProvenanceRegistry()}
	data, err := codec.Encode(
		contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{msg}),
	)
	require.NoError(t, err)
	decoded, err := codec.Decode(data)
	require.NoError(t, err)
	got := decoded.Segment(contexty.SegmentHistory)[0]
	require.NotNil(t, got.Actor)
	assert.Equal(t, "alert", got.Actor.Kind)
	require.Len(t, got.SourceRefs, 1)
	assert.Equal(t, "stable-1", got.SourceRefs[0].CheckpointID)

	engine := contexty.NewEngine(
		contexty.WithRoleProjectionPolicy(
			contexty.RoleProjectionFunc(func(msg contexty.Message) (contexty.Role, error) {
				if msg.Actor != nil && msg.Actor.Kind == "alert" {
					return contexty.RoleSystem, nil
				}
				return msg.Role, nil
			}),
		),
	)
	result, err := engine.CompileSnapshot(
		ctx,
		contexty.CompileRequest{History: []contexty.Message{msg}},
	)
	require.NoError(t, err)
	assert.Equal(t, contexty.RoleSystem, result.Payload.History[0].Role)
	assert.Equal(t, contexty.RoleUser, result.Source.History[0].Role)
	assert.Equal(t, contexty.RoleUser, msg.Role)
}

func TestDoD_Task15_ContextAwareFormatterReceivesContextAndPropagatesError(t *testing.T) {
	t.Parallel()
	expectedErr := errors.New("formatter failed")
	ctx := context.WithValue(context.Background(), formatterContextKey{}, "trace-1")
	engine := contexty.NewEngine(
		contexty.WithSegmentFormatter(
			contexty.SegmentMemory,
			func(ctx context.Context, _ []contexty.Message) ([]contexty.Message, error) {
				assert.Equal(t, "trace-1", ctx.Value(formatterContextKey{}))
				return nil, expectedErr
			},
		),
	)

	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{contexty.TextMessage(contexty.RoleSystem, "memory")},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
}

func TestDoD_Task15_NamedViewFormatterReceivesContextAndCanFail(t *testing.T) {
	t.Parallel()
	expectedErr := errors.New("view formatter failed")
	ctx := context.WithValue(context.Background(), formatterContextKey{}, "view-trace")
	engine := contexty.NewEngine(
		contexty.WithNamedView("ctx-view", contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Formatter: func(ctx context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				assert.Equal(t, "view-trace", ctx.Value(formatterContextKey{}))
				return msgs, expectedErr
			},
		}),
	)
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hello"),
	})

	_, err := engine.RenderView(ctx, snap, "ctx-view")
	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
}

func TestDoD_Task15_ContextArtifactsLifecycleAndBudgetPreflight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	visible := contexty.NewRetrievalDocument(
		"visible",
		contexty.TextPayload("visible artifact"),
	).ContextArtifact
	visible.BoundTurnID = "turn-1"
	hidden := contexty.NewRetrievalDocument(
		"hidden",
		contexty.TextPayload("hidden artifact"),
	).ContextArtifact
	hidden.BoundTurnID = "turn-2"
	unbound := contexty.NewRetrievalDocument(
		"unbound",
		contexty.TextPayload("unbound retrieval"),
	).ContextArtifact
	persistent := contexty.NewMemoryBlock(
		"persistent",
		contexty.TextPayload("persistent artifact"),
	).ContextArtifact
	oversized := contexty.NewMemoryBlock(
		"oversized",
		contexty.TextPayload("this artifact is too large"),
	).ContextArtifact.WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 3})

	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{
					TokenLimit: 30,
					DropHead:   contexty.DropHeadConfig{MinMessages: 1},
				},
				&contexty.FixedEstimator{TokensPerMessage: 10},
			),
		),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		TurnID:    "turn-1",
		Artifacts: []contexty.ContextArtifact{visible, hidden, unbound, persistent, oversized},
		History: []contexty.Message{
			{
				ID:    "old",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}},
			},
			{
				ID:    "new",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "new"}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 2)
	assert.ElementsMatch(t, []string{"visible", "persistent"}, artifactIDs(result.Artifacts))
	memoryText := flattenText(result.Payload.Memory)
	assert.Contains(t, memoryText, "visible artifact")
	assert.Contains(t, memoryText, "persistent artifact")
	assert.NotContains(t, memoryText, "hidden artifact")
	assert.NotContains(t, memoryText, "unbound retrieval")
	assert.NotContains(t, memoryText, "this artifact is too large")
	assert.Len(t, result.Payload.History, 1)
	assert.Equal(t, "new", result.Payload.History[0].ID)
}

func TestDoD_Task15_ContextArtifactPersistenceAndMergePolicies(t *testing.T) {
	t.Parallel()
	left := contexty.NewMemoryBlock(
		"merge-1",
		contexty.TextPayload("left"),
	).ContextArtifact
	right := contexty.NewMemoryBlock(
		"merge-1",
		contexty.TextPayload("right"),
	).ContextArtifact
	right.MergePolicy = contexty.PolicyAppend

	state, err := contexty.ApplyDeltas(
		contexty.EmptyState(),
		contexty.ConversationDelta{Operation: contexty.DeltaUpsertArtifact, Artifact: &left},
		contexty.ConversationDelta{Operation: contexty.DeltaUpsertArtifact, Artifact: &right},
	)
	require.NoError(t, err)
	require.Len(t, state.Artifacts(), 1)
	assert.Equal(t, "left\nright", state.Artifacts()[0].Payload.PlainText())

	storeArtifact := contexty.NewMemoryBlock(
		"store",
		contexty.TextPayload("store"),
	).ContextArtifact.WithPersistence(contexty.ArtifactPersistenceStore).WithOwner(contexty.SourceRef{
		Namespace: "tenant",
		Kind:      "workspace",
		ID:        "workspace-1",
	})
	skipArtifact := contexty.NewMemoryBlock(
		"skip",
		contexty.TextPayload("skip"),
	).ContextArtifact.WithPersistence(contexty.ArtifactPersistenceSkip)
	turnDefault := contexty.NewRetrievalDocument(
		"turn-default",
		contexty.TextPayload("turn default"),
	).ContextArtifact.WithTurn("turn-1")
	turnStored := contexty.NewRetrievalDocument(
		"turn-stored",
		contexty.TextPayload("turn stored"),
	).ContextArtifact.WithTurn("turn-1").WithPersistence(contexty.ArtifactPersistenceStore)
	ephemeral := contexty.NewMemoryBlock(
		"ephemeral",
		contexty.TextPayload("ephemeral"),
	).ContextArtifact
	ephemeral.Lifecycle = contexty.ArtifactLifecycleEphemeral

	codec := contexty.ConversationStateCodec{Provenance: contexty.DefaultProvenanceRegistry()}
	data, err := codec.EncodeState(
		contexty.EmptyState().WithArtifacts([]contexty.ContextArtifact{
			storeArtifact,
			skipArtifact,
			turnDefault,
			turnStored,
			ephemeral,
		}),
	)
	require.NoError(t, err)
	decoded, err := codec.DecodeState(data)
	require.NoError(t, err)
	assert.Equal(t, []string{"store", "turn-stored"}, artifactIDs(decoded.Artifacts()))
	require.NotNil(t, decoded.Artifacts()[0].OwnerRef)
	assert.Equal(t, "workspace-1", decoded.Artifacts()[0].OwnerRef.ID)
}

func TestDoD_Task15_ConversationDeltaStateCodecAndStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	initial := contexty.EmptyState().WithSegment(contexty.SegmentHistory, []contexty.Message{
		{
			ID:    "m1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "one"}},
		},
	})
	artifact := contexty.NewMemoryBlock(
		"mem-1",
		contexty.TextPayload("durable memory"),
	).ContextArtifact
	state, err := contexty.ApplyDeltas(
		initial,
		contexty.ConversationDelta{
			Operation: contexty.DeltaAppendMessages,
			Segment:   contexty.SegmentHistory,
			Messages: []contexty.Message{
				{
					ID:    "m2",
					Role:  contexty.RoleAssistant,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "two"}},
				},
			},
		},
		contexty.ConversationDelta{
			Operation: contexty.DeltaUpsertArtifact,
			Artifact:  &artifact,
		},
	)
	require.NoError(t, err)
	assert.Len(t, initial.Segment(contexty.SegmentHistory), 1)
	assert.Len(t, state.Segment(contexty.SegmentHistory), 2)
	require.Len(t, state.Artifacts(), 1)

	codec := contexty.ConversationStateCodec{Provenance: contexty.DefaultProvenanceRegistry()}
	stateData, err := codec.EncodeState(state)
	require.NoError(t, err)
	decodedState, err := codec.DecodeState(stateData)
	require.NoError(t, err)
	assert.True(
		t,
		contexty.MessagesEqual(
			state.Segment(contexty.SegmentHistory),
			decodedState.Segment(contexty.SegmentHistory),
		),
	)
	assert.Equal(t, "mem-1", decodedState.Artifacts()[0].ID)

	deltaData, err := codec.EncodeDelta(contexty.ConversationDelta{
		Operation:  contexty.DeltaRemoveMessages,
		Segment:    contexty.SegmentHistory,
		MessageIDs: []string{"m1"},
	})
	require.NoError(t, err)
	decodedDelta, err := codec.DecodeDelta(deltaData)
	require.NoError(t, err)
	reduced, err := contexty.ApplyDelta(decodedState, decodedDelta)
	require.NoError(t, err)
	assert.Equal(t, []string{"m2"}, messageIDs(reduced.Segment(contexty.SegmentHistory)))
	assert.Equal(t, "mem-1", reduced.Artifacts()[0].ID)

	store := contexty.NewMemoryConversationStateStore()
	err = store.ApplyDelta(ctx, "conversation-1", 0, contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment,
		Segment:   contexty.SegmentHistory,
		Messages:  reduced.Segment(contexty.SegmentHistory),
	})
	require.NoError(t, err)
	stored, err := store.LoadState(ctx, "conversation-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), stored.Version())
	assert.Equal(t, []string{"m2"}, messageIDs(stored.Segment(contexty.SegmentHistory)))

	args, err := contexty.StructuredPayload(struct {
		Query string `json:"query"`
	}{Query: "from store"})
	require.NoError(t, err)
	assistant := contexty.Message{
		ID:   "assistant-call",
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{
				ID:        "call-1",
				Name:      "lookup",
				Arguments: args,
			},
		},
	}
	toolResult := contexty.Message{
		ID:   "tool-result",
		Role: contexty.RoleTool,
		Parts: []contexty.ContentPart{
			contexty.ToolResultPart{
				ToolCallID: "call-1",
				Name:       "lookup",
				Payload:    contexty.TextPayload("tool payload"),
			},
		},
	}
	round := contexty.ToolRound{
		Assistant: assistant,
		Results:   []contexty.Message{toolResult},
	}
	err = store.ApplyDelta(ctx, "conversation-1", 1, contexty.ConversationDelta{
		Operation: contexty.DeltaAppendToolRound,
		ToolRound: &round,
	})
	require.NoError(t, err)
	stored, err = store.LoadState(ctx, "conversation-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), stored.Version())
	assert.Equal(
		t,
		[]string{"m2", "assistant-call", "tool-result"},
		messageIDs(stored.Segment(contexty.SegmentHistory)),
	)
	assert.True(t, contexty.ToolTurnUsesCanonicalLayout(stored.Segment(contexty.SegmentHistory), 1))
	require.Len(t, stored.ToolRounds(), 1)

	engine := contexty.NewEngine(
		contexty.WithConversationID("conversation-1"),
		contexty.WithStateStore(store),
	)
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"m2", "assistant-call", "tool-result"}, messageIDs(result.Payload.History))
	err = store.ApplyDelta(
		ctx,
		"conversation-1",
		0,
		contexty.ConversationDelta{Operation: contexty.DeltaClearState},
	)
	require.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
}

func TestDoD_Task15_ToolRoundsStayAlignedWithHistoryDeltas(t *testing.T) {
	t.Parallel()
	codec := contexty.ConversationStateCodec{
		Provenance: contexty.DefaultProvenanceRegistry(),
		Extensions: newTestExtensionRegistry(),
	}
	assistant := contexty.Message{
		ID:         "assistant-call",
		Role:       contexty.RoleAssistant,
		Extensions: []contexty.Extension{testExtension{Tenant: "acme", Score: 11}},
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{
				ID:        "call-1",
				Name:      "lookup",
				Arguments: contexty.JSONPayload("{}"),
			},
		},
	}
	toolResult := contexty.Message{
		ID:   "tool-result",
		Role: contexty.RoleTool,
		Parts: []contexty.ContentPart{
			contexty.ToolResultPart{
				ToolCallID: "call-1",
				Name:       "lookup",
				Payload:    contexty.TextPayload("ok"),
			},
		},
	}
	round := contexty.ToolRound{Assistant: assistant, Results: []contexty.Message{toolResult}}

	deltaData, err := codec.EncodeDelta(contexty.ConversationDelta{
		Operation: contexty.DeltaAppendToolRound,
		ToolRound: &round,
	})
	require.NoError(t, err)
	assert.Contains(t, string(deltaData), `"tool_round":[`)
	decodedDelta, err := codec.DecodeDelta(deltaData)
	require.NoError(t, err)
	require.NotNil(t, decodedDelta.ToolRound)
	require.Len(t, decodedDelta.ToolRound.Assistant.Extensions, 1)
	ext, ok := decodedDelta.ToolRound.Assistant.Extensions[0].(testExtension)
	require.True(t, ok)
	assert.Equal(t, "acme", ext.Tenant)

	state, err := contexty.ApplyDelta(contexty.EmptyState(), decodedDelta)
	require.NoError(t, err)
	require.Len(t, state.ToolRounds(), 1)

	_, err = contexty.ApplyDelta(state, contexty.ConversationDelta{
		Operation:  contexty.DeltaRemoveMessages,
		Segment:    contexty.SegmentHistory,
		MessageIDs: []string{"tool-result"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "splits tool round")

	state, err = contexty.ApplyDelta(state, contexty.ConversationDelta{
		Operation:  contexty.DeltaRemoveMessages,
		Segment:    contexty.SegmentHistory,
		MessageIDs: []string{"assistant-call", "tool-result"},
	})
	require.NoError(t, err)
	assert.Empty(t, state.ToolRounds())
	data, err := codec.EncodeState(state)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "assistant-call")
	assert.NotContains(t, string(data), "tool_rounds")
	decoded, err := codec.DecodeState(data)
	require.NoError(t, err)
	assert.Empty(t, decoded.ToolRounds())

	state, err = contexty.ApplyDelta(contexty.EmptyState(), contexty.ConversationDelta{
		Operation: contexty.DeltaAppendToolRound,
		ToolRound: &round,
	})
	require.NoError(t, err)
	state, err = contexty.ApplyDelta(state, contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment,
		Segment:   contexty.SegmentHistory,
		Messages: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "replacement"),
		},
	})
	require.NoError(t, err)
	assert.Empty(t, state.ToolRounds())

	state, err = contexty.ApplyDelta(contexty.EmptyState(), contexty.ConversationDelta{
		Operation: contexty.DeltaAppendToolRound,
		ToolRound: &round,
	})
	require.NoError(t, err)
	state, err = contexty.ApplyDelta(state, contexty.ConversationDelta{
		Operation: contexty.DeltaClearSegment,
		Segment:   contexty.SegmentHistory,
	})
	require.NoError(t, err)
	assert.Empty(t, state.ToolRounds())
}

func artifactIDs(artifacts []contexty.ContextArtifact) []string {
	ids := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		ids = append(ids, artifact.ID)
	}
	return ids
}

func flattenText(msgs []contexty.Message) string {
	var b strings.Builder
	for _, msg := range msgs {
		b.WriteString(msg.TextContent())
		b.WriteString("\n")
	}
	return b.String()
}

func messageIDs(msgs []contexty.Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		ids = append(ids, msg.ID)
	}
	return ids
}
