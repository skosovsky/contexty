package contexty_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type hostProvenance struct {
	Sources []string `json:"sources"`
}

func (hostProvenance) ProvenanceType() string { return "host/source" }
func (p hostProvenance) CloneProvenance() contexty.Provenance {
	return hostProvenance{Sources: slices.Clone(p.Sources)}
}

func TestRemediation_HostProvenance(t *testing.T) {
	// Arrange: an external package implements the port with mutable owned data.
	registry := contexty.DefaultProvenanceRegistry()
	registry.Register("host/source", func(b []byte) (contexty.Provenance, error) {
		var p hostProvenance
		err := json.Unmarshal(b, &p)
		return p, err
	})
	original := contexty.Message{ID: "event", Role: contexty.RoleUser,
		Provenance: hostProvenance{Sources: []string{"document"}}}
	codec := contexty.JSONSerializer{Provenance: registry}
	// Act: clone and codec roundtrip.
	cloned := original.Clone()
	cloned.Provenance.(hostProvenance).Sources[0] = "changed"
	wire, err := codec.Marshal(original)
	require.NoError(t, err)
	var decoded contexty.Message
	require.NoError(t, codec.Unmarshal(wire, &decoded))
	// Assert: clone owns its fields and wire preserves host type and content.
	require.Equal(t, []string{"document"}, original.Provenance.(hostProvenance).Sources)
	require.Equal(t, original.Provenance, decoded.Provenance)
	for _, p := range []contexty.Provenance{contexty.UserProvenance{Channel: "web"}, contexty.SystemProvenance{Component: "test"}} {
		encoded, encodeErr := contexty.EncodeProvenance(p)
		require.NoError(t, encodeErr)
		restored, decodeErr := registry.Decode(encoded)
		require.NoError(t, decodeErr)
		require.Equal(t, p, restored)
	}
}

func TestRemediation_ArtifactRemovalIDs(t *testing.T) {
	// Arrange: message and artifact share an ID; removal must address only artifact.
	state := contexty.EmptyState().
		WithSegment(contexty.SegmentHistory, []contexty.Message{{ID: "same", Role: contexty.RoleUser}}).
		WithArtifact(contexty.ContextArtifact{ID: "same", Kind: contexty.ArtifactKindMemoryBlock}).
		WithArtifact(contexty.ContextArtifact{ID: "keep", Kind: contexty.ArtifactKindMemoryBlock})
	delta := contexty.ConversationDelta{Operation: contexty.DeltaRemoveArtifact, ArtifactIDs: []string{"same"}}
	codec := contexty.ConversationStateCodec{}
	// Act: serialize, decode, mutate original IDs, apply decoded delta.
	wire, err := codec.EncodeDelta(delta)
	require.NoError(t, err)
	decoded, err := codec.DecodeDelta(wire)
	require.NoError(t, err)
	delta.ArtifactIDs[0] = "keep"
	next, err := contexty.ApplyDelta(state, decoded)
	// Assert: exact artifact removed; message and source state unchanged.
	require.NoError(t, err)
	require.JSONEq(t, `{"operation":"remove_artifact","artifact_ids":["same"],"messages":[]}`, string(wire))
	require.Len(t, next.Artifacts(), 1)
	require.Equal(t, "keep", next.Artifacts()[0].ID)
	require.Len(t, next.Segment(contexty.SegmentHistory), 1)
	require.Len(t, state.Artifacts(), 2)
}

func TestRemediation_RejectLegacyArtifactRemoval(t *testing.T) {
	// Arrange: former artifact removal payload uses message IDs.
	codec := contexty.ConversationStateCodec{}
	legacy := contexty.ConversationDelta{Operation: contexty.DeltaRemoveArtifact, MessageIDs: []string{"old"}}
	// Act / Assert: all boundaries fail explicitly, never silently remove nothing.
	_, err := codec.EncodeDelta(legacy)
	require.ErrorIs(t, err, contexty.ErrInvalidDeltaIDs)
	_, err = codec.DecodeDelta([]byte(`{"operation":"remove_artifact","message_ids":["old"]}`))
	require.ErrorIs(t, err, contexty.ErrInvalidDeltaIDs)
	_, err = contexty.ApplyDelta(contexty.EmptyState(), legacy)
	require.ErrorIs(t, err, contexty.ErrInvalidDeltaIDs)
	// Arrange: artifact IDs accidentally supplied to message removal.
	misplaced := contexty.ConversationDelta{Operation: contexty.DeltaRemoveMessages, ArtifactIDs: []string{"artifact"}}
	// Act / Assert: do not ignore the wrong namespace.
	_, err = codec.EncodeDelta(misplaced)
	require.ErrorIs(t, err, contexty.ErrInvalidDeltaIDs)
	_, err = contexty.ApplyDelta(contexty.EmptyState(), misplaced)
	require.ErrorIs(t, err, contexty.ErrInvalidDeltaIDs)
}

func TestRemediation_RejectLegacyIDPresence(t *testing.T) {
	for _, payload := range []string{
		`{"operation":"remove_artifact","message_ids":[]}`,
		`{"operation":"remove_artifact","message_ids":null}`,
		`{"operation":"remove_artifact","artifact_ids":["same"],"message_ids":[]}`,
		`{"operation":"remove_messages","artifact_ids":[]}`,
		`{"operation":"remove_messages","artifact_ids":null}`,
		`{"operation":"remove_artifact","message_IDS":null,"artifact_ids":["same"]}`,
		`{"operation":"remove_messages","Artifact_IDs":null}`,
	} {
		t.Run(payload, func(t *testing.T) {
			// Arrange: a forbidden namespace key is present, even without IDs.
			codec := contexty.ConversationStateCodec{}
			// Act.
			_, err := codec.DecodeDelta([]byte(payload))
			// Assert: key presence cannot silently disappear at decoding.
			require.ErrorIs(t, err, contexty.ErrInvalidDeltaIDs)
		})
	}
	for _, delta := range []contexty.ConversationDelta{
		{Operation: contexty.DeltaRemoveArtifact, MessageIDs: []string{}},
		{Operation: contexty.DeltaRemoveMessages, ArtifactIDs: []string{}},
	} {
		// Arrange: explicit empty Go slices in the wrong ID namespace.
		codec := contexty.ConversationStateCodec{}
		// Act / Assert: both boundaries enforce the same contract.
		_, err := codec.EncodeDelta(delta)
		require.ErrorIs(t, err, contexty.ErrInvalidDeltaIDs)
		_, err = contexty.ApplyDelta(contexty.EmptyState(), delta)
		require.ErrorIs(t, err, contexty.ErrInvalidDeltaIDs)
	}
}
