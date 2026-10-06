//go:build fuzz

package contexty

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func FuzzRemediationEventIdentity(f *testing.F) {
	f.Add("turn", "other", 0)
	f.Add("a\x00:b", "a:b\x00", 2147483647)
	f.Add("\xff", "\xfe", 0)
	f.Fuzz(func(t *testing.T, turn, other string, ordinal int) {
		// Arrange / Act: arbitrary host identity, including delimiters and malformed UTF-8.
		policy := NewStableMessageIdentityPolicy("host")
		input := MessageIdentityContext{TurnID: turn, Ordinal: ordinal, CurrentTurn: true}
		id, err := policy.ResolveMessageID(input, Message{})
		// Assert: reject insufficient/invalid identity; history positions/content are irrelevant.
		if turn == "" || ordinal < 0 || !utf8.ValidString(turn) {
			require.ErrorIs(t, err, ErrMissingEventIdentity)
			return
		}
		require.NoError(t, err)
		input.Index = 900
		retry, err := policy.ResolveMessageID(input, TextMessage(RoleUser, "changed"))
		require.NoError(t, err)
		require.Equal(t, id, retry)
		input.TurnID = other
		second, err := policy.ResolveMessageID(input, Message{})
		if other != "" && utf8.ValidString(other) {
			require.NoError(t, err)
			if other != turn {
				require.NotEqual(t, id, second)
			}
		}
	})
}

func FuzzRemediationPartsAndCodec(f *testing.F) {
	f.Add("hello", []byte(`{"schema":"unknown"}`))
	f.Add("", []byte(`null`))
	f.Add("日本語", []byte(`{`))
	f.Fuzz(func(t *testing.T, text string, wire []byte) {
		// Arrange / Act: the same valid typed part through pointer/value ownership boundaries.
		if !utf8.ValidString(text) {
			return
		}
		part := TextPart{Text: text}
		value, err := ownCompileMessage(Message{Parts: []ContentPart{part}})
		require.NoError(t, err)
		pointer, err := ownCompileMessage(Message{Parts: []ContentPart{&part}})
		require.NoError(t, err)
		// Assert: equivalent canonical values, and accepted codec data re-encodes/re-decodes.
		require.True(t, MessageEqual(value, pointer))
		codec := ConversationCodec{}
		state, err := codec.Decode(wire)
		if err != nil {
			return
		}
		encoded, err := codec.Encode(state)
		require.NoError(t, err)
		decoded, err := codec.Decode(encoded)
		require.NoError(t, err)
		require.Equal(t, state.Version(), decoded.Version())
	})
}
