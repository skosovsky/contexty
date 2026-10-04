package main

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestOpaqueFixtureCheckpointPreservesTypedState(t *testing.T) {
	// Arrange.
	codec := hostCodec()
	initial, err := fixtureMessages(codec)
	require.NoError(t, err)
	before, err := codec.Marshal(initial[1])
	require.NoError(t, err)
	// Act.
	loaded, err := checkpointRoundTrip(t.Context(), codec, initial)
	require.NoError(t, err)
	after, err := codec.Marshal(loaded[1])
	require.NoError(t, err)
	// Assert: ordering, placement, identity and bindings survive alongside bytes.
	require.Equal(t, before, after)
	require.Len(t, loaded[1].Extensions, 2)
	signature, ok := loaded[1].Extensions[0].(contexty.OpaqueState)
	require.True(t, ok)
	compact, ok := loaded[1].Extensions[1].(contexty.OpaqueState)
	require.True(t, ok)
	require.IsType(t, signatureFixture{}, signature.Payload)
	require.IsType(t, opaqueCompactionFixture{}, compact.Payload)
	owned, ok := signature.Payload.(signatureFixture)
	require.True(t, ok)
	owned.Signature[0] = 42
	unchanged, err := codec.Marshal(initial[1])
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
}

func TestOpaqueFixtureDependencyRules(t *testing.T) {
	// Arrange.
	codec := hostCodec()
	initial, err := fixtureMessages(codec)
	require.NoError(t, err)
	engine := fixtureEngine(codec, contexty.OpaqueFailClosed)
	outside := contexty.TextMessage(contexty.RoleUser, "Unbound tail.")
	outside.ID = "outside"
	valid := append(slices.Clone(initial), outside)
	invalid := slices.Clone(initial)
	invalid[0] = contexty.TextMessage(contexty.RoleUser, "Changed input.")
	invalid[0].ID = initial[0].ID
	// Act.
	accepted, acceptedErr := compileFixture(t.Context(), engine, valid)
	_, invalidErr := compileFixture(t.Context(), engine, invalid)
	dropped, dropErr := compileFixture(t.Context(), fixtureEngine(codec, contexty.OpaqueDropInvalid), invalid)
	// Assert.
	require.NoError(t, acceptedErr)
	require.Len(t, accepted.Payload.History[1].Extensions, 2)
	require.ErrorIs(t, invalidErr, contexty.ErrOpaqueStateInvalidated)
	require.NoError(t, dropErr)
	require.Empty(t, dropped.Payload.History[1].Extensions)
	require.Len(t, initial[1].Extensions, 2)
}

func TestOpaqueFixtureMissingCodecAndProfileFail(t *testing.T) {
	// Arrange.
	codec := hostCodec()
	messages, err := fixtureMessages(codec)
	require.NoError(t, err)
	checkpoint, err := contexty.ProjectCheckpoint(
		contexty.EmptyState().WithSegment(contexty.SegmentHistory, messages), codec, hostProfile(),
	)
	require.NoError(t, err)
	wrong := contexty.Descriptor{ID: hostProfile().ID, Revision: "2"}
	// Act.
	missingCodecErr := contexty.ValidateOpaqueState(messages, contexty.DefaultJSONSerializer(), hostProfile())
	wrongProfileErr := contexty.ValidateOpaqueState(messages, codec, wrong)
	encoded, encodeErr := (contexty.ConversationCodec{
		Provenance: codec.Provenance, Extensions: codec.Extensions, OpaqueProfile: hostProfile(),
	}).Encode(checkpoint)
	// Assert.
	require.ErrorIs(t, missingCodecErr, contexty.ErrMissingOpaqueStateCodec)
	require.ErrorIs(t, wrongProfileErr, contexty.ErrOpaqueStateInvalidated)
	require.NoError(t, encodeErr)
	require.True(t, bytes.Contains(encoded, []byte(signatureType)))
	require.True(t, bytes.Contains(encoded, []byte(compactionType)))
}

func TestOpaqueFixtureExecutable(t *testing.T) {
	// Arrange/Act/Assert: the offline recipe includes no SDK or network calls.
	require.NoError(t, run(t.Context()))
}
