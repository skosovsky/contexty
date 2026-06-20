package contexty_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestJSONSerializer_RoundTrip(t *testing.T) {
	serializer := contexty.DefaultJSONSerializer()
	msg := contexty.Message{
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.TextPart{Text: "summary"},
			contexty.ImagePart{URL: "https://example.com/image.png", Detail: "low"},
			contexty.ToolCallPart{ID: "tc1", Name: "fn", Arguments: contexty.JSONPayload(`{"a":1}`)},
		},
		SourceRefs: []contexty.SourceRef{{
			Namespace: "messages",
			Kind:      "external",
			ID:        "ref-1",
		}},
		Provenance: contexty.SystemProvenance{Component: "test"},
	}
	data, err := serializer.Marshal(msg)
	require.NoError(t, err)
	var out contexty.Message
	require.NoError(t, serializer.Unmarshal(data, &out))
	assert.True(t, contexty.MessageEqual(msg, out))
}

func TestConversationCodec_RoundTrip(t *testing.T) {
	codec := contexty.ConversationCodec{Provenance: contexty.DefaultProvenanceRegistry()}
	snap := contexty.EmptySnapshot().WithVersion(3).WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hi"),
	})
	data, err := codec.Encode(snap)
	require.NoError(t, err)
	out, err := codec.Decode(data)
	require.NoError(t, err)
	assert.Equal(t, int64(3), out.Version())
	assert.Equal(t, "hi", out.Segment(contexty.SegmentHistory)[0].TextContent())
}
