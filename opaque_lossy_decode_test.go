package contexty_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func lossyOpaqueDecoderCodec() contexty.JSONSerializer {
	codec := contexty.DefaultJSONSerializer()
	codec.Extensions.RegisterOpaquePayload(
		"acceptance.host.state",
		contexty.Descriptor{ID: "host/encoding", Revision: "1"},
		func([]byte) (contexty.Extension, error) {
			return acceptanceOpaquePayload{Bytes: []byte("replacement")}, nil
		},
	)
	return codec
}

func TestAcceptance_OpaqueUnmarshalRejectsLossyHostDecoder(t *testing.T) {
	// Arrange: persisted state was encoded by a lossless registered host codec.
	messages, originalCodec, _ := acceptanceOpaqueFixture(t)
	wire, err := originalCodec.Marshal(messages[2])
	require.NoError(t, err)
	codec := lossyOpaqueDecoderCodec()
	sentinel := fixtureRollingText("unchanged", "owned by caller")
	decoded := sentinel.Clone()
	// Act: another host decoder claims the same identity but overwrites payload.
	err = codec.Unmarshal(wire, &decoded)
	// Assert: bytes cannot silently change and caller destination stays untouched.
	require.ErrorIs(t, err, contexty.ErrMissingOpaqueStateCodec)
	require.True(t, contexty.MessageEqual(sentinel, decoded))
}

func TestAcceptance_OpaqueConversationDecodeRejectsLossyHostDecoder(t *testing.T) {
	// Arrange: the checkpoint contains the full declared dependency scope.
	messages, originalCodec, profile := acceptanceOpaqueFixture(t)
	original := contexty.ConversationCodec{
		Provenance:    originalCodec.Provenance,
		Extensions:    originalCodec.Extensions,
		OpaqueProfile: profile,
	}
	snapshot := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, messages)
	wire, err := original.Encode(snapshot)
	require.NoError(t, err)
	lossless := lossyOpaqueDecoderCodec()
	decoder := contexty.ConversationCodec{
		Provenance:    lossless.Provenance,
		Extensions:    lossless.Extensions,
		OpaqueProfile: profile,
	}
	// Act: decode must verify the payload against incoming wire, not its own re-encode.
	decoded, err := decoder.Decode(wire)
	// Assert: no successfully reconstructed checkpoint may contain substituted state.
	require.ErrorIs(t, err, contexty.ErrMissingOpaqueStateCodec)
	require.Empty(t, decoded.Segment(contexty.SegmentHistory))
}
