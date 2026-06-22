package contexty_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Provenance_Typed(t *testing.T) {
	// Arrange.
	reg := contexty.DefaultProvenanceRegistry()
	msg := contexty.Message{
		Role:       contexty.RoleUser,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "x"}},
		Provenance: contexty.UserProvenance{Channel: "tg", UserID: "u1"},
	}
	data, err := contexty.MarshalMessageJSON(msg, contexty.MessageCodec{Provenance: reg})
	require.NoError(t, err)
	out, err := contexty.UnmarshalMessageJSON(data, contexty.MessageCodec{Provenance: reg})
	require.NoError(t, err)
	prov, ok := out.Provenance.(contexty.UserProvenance)
	require.True(t, ok)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, "tg", prov.Channel)
}

func TestAcceptance_Provenance_ThroughCompile(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	msg := contexty.Message{
		Role:        contexty.RoleUser,
		Parts:       []contexty.ContentPart{contexty.TextPart{Text: "hi"}},
		Annotations: contexty.Annotations{Timestamp: &ts},
		SourceRefs: []contexty.SourceRef{{
			Namespace: "messages",
			Kind:      "external",
			ID:        "r1",
		}},
		Provenance: contexty.UserProvenance{Channel: "api"},
	}
	s0, _ := loadState(ctx, store, "t")
	require.NoError(
		t,
		updateSegment(
			ctx,
			store,
			"t",
			s0.Version(),
			contexty.SegmentHistory,
			[]contexty.Message{msg},
		),
	)
	engine := contexty.NewEngine(contexty.WithConversationID("t"), contexty.WithStateStore(store))
	// Act.
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	payload := result.Payload
	require.Len(t, payload.History, 1)
	assert.Equal(t, "api", payload.History[0].Provenance.(contexty.UserProvenance).Channel)
}

func TestAcceptance_Extensions_RoundTrip(t *testing.T) {
	// Arrange.
	reg := newTestExtensionRegistry()
	codec := contexty.ConversationCodec{
		Provenance: contexty.DefaultProvenanceRegistry(),
		Extensions: reg,
	}
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{{
		ID:         "ext-1",
		Role:       contexty.RoleUser,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "hi"}},
		Extensions: []contexty.Extension{testExtension{Tenant: "acme", Score: 3}},
	}})
	// Act.
	data, err := codec.Encode(snap)
	// Assert.
	require.NoError(t, err)
	decoded, err := codec.Decode(data)
	require.NoError(t, err)
	msgs := decoded.Segment(contexty.SegmentHistory)
	require.Len(t, msgs, 1)
	ext, ok := msgs[0].Extensions[0].(testExtension)
	require.True(t, ok)
	assert.Equal(t, "acme", ext.Tenant)
	assert.InEpsilon(t, float64(3), ext.Score, 0)
}
