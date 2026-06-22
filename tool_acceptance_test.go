package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Tool_PartsEndToEnd(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	msgs := []contexty.Message{
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{
					ID:        "c1",
					Name:      "search",
					Arguments: contexty.JSONPayload(`{"q":"x"}`),
				},
			},
		},
		{
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "c1", Payload: contexty.TextPayload("ok")},
			},
		},
	}
	store := contexty.NewMemoryConversationStateStore()
	s0, err := loadState(ctx, store, "tools")
	require.NoError(t, err)
	require.NoError(
		t,
		updateSegment(ctx, store, "tools", s0.Version(), contexty.SegmentHistory, msgs),
	)
	engine := contexty.NewEngine(contexty.WithConversationID("tools"), contexty.WithStateStore(store))
	// Act.
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	payload := result.Payload
	require.Len(t, payload.History, 2)
	assert.True(t, contexty.ToolTurnUsesCanonicalLayout(payload.History, 0))
	require.Len(t, payload.History[0].ToolCallParts(), 1)
	require.Len(t, payload.History[1].ToolResultParts(), 1)

	msg := contexty.Message{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{
				ID:        "c1",
				Name:      "search",
				Arguments: contexty.JSONPayload(`{"q":"x"}`),
			},
			contexty.ToolResultPart{ToolCallID: "c1", Payload: contexty.TextPayload("ok")},
		},
	}
	data, err := contexty.MarshalMessageJSON(
		msg,
		contexty.MessageCodec{Provenance: contexty.DefaultProvenanceRegistry()},
	)
	require.NoError(t, err)
	out, err := contexty.UnmarshalMessageJSON(
		data,
		contexty.MessageCodec{Provenance: contexty.DefaultProvenanceRegistry()},
	)
	require.NoError(t, err)
	require.Len(t, out.ToolCallParts(), 1)
	require.Len(t, out.ToolResultParts(), 1)
	assert.Equal(t, "c1", out.ToolResultParts()[0].ToolCallID)
}

func TestAcceptance_Tool_PartsStorageRoundTrip(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	msgs := []contexty.Message{
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{
					ID:        "c1",
					Name:      "search",
					Arguments: contexty.JSONPayload(`{}`),
				},
			},
		},
		{
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "c1", Payload: contexty.TextPayload("ok")},
			},
		},
	}
	s0, err := loadState(ctx, store, "roundtrip")
	require.NoError(t, err)
	require.NoError(
		t,
		updateSegment(ctx, store, "roundtrip", s0.Version(), contexty.SegmentHistory, msgs),
	)
	snap, err := loadState(ctx, store, "roundtrip")
	require.NoError(t, err)
	got := snap.Segment(contexty.SegmentHistory)
	require.Len(t, got, 2)
	assert.True(t, contexty.ToolTurnUsesCanonicalLayout(got, 0))
	require.Len(t, got[0].ToolCallParts(), 1)
	// Act / Assert: exercise the contract and check its result.
	require.Len(t, got[1].ToolResultParts(), 1)
}

func TestAcceptance_Tool_TurnCanonicalLayout(t *testing.T) {
	// Arrange.
	msgs := []contexty.Message{
		{
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{ID: "a", Name: "fn", Arguments: contexty.JSONPayload("{}")},
			},
		},
		{
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{ToolCallID: "a", Payload: contexty.TextPayload("r")},
			},
		},
	}
	assert.True(t, contexty.ToolTurnUsesCanonicalLayout(msgs, 0))
	inMessage := []contexty.Message{{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{ID: "b", Name: "fn", Arguments: contexty.JSONPayload("{}")},
			contexty.ToolResultPart{ToolCallID: "b", Payload: contexty.TextPayload("inline")},
		},
	}}
	// Act / Assert: exercise the contract and check its result.
	assert.False(t, contexty.ToolTurnUsesCanonicalLayout(inMessage, 0))
}

func TestAcceptance_Tool_PayloadRoundTripAndToolRoundValidation(t *testing.T) {
	// Arrange.
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
	// Act / Assert: exercise the contract and check its result.
	require.Error(t, duplicateResult.Validate())
}

func TestAcceptance_Tool_RoundsStayAlignedWithHistoryDeltas(t *testing.T) {
	// Arrange.
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
	// Act / Assert: exercise the contract and check its result.
	assert.Empty(t, state.ToolRounds())
}
