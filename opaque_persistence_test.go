package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type durableOpaquePayload struct {
	Bytes []byte `json:"bytes"`
}

func (durableOpaquePayload) ExtensionType() string { return "fixture.durable-state" }
func (p durableOpaquePayload) CloneExtension() contexty.Extension {
	p.Bytes = append([]byte(nil), p.Bytes...)
	return p
}

func durableOpaqueFixture(
	t *testing.T,
) (contexty.Message, contexty.Message, contexty.JSONSerializer, contexty.Descriptor) {
	t.Helper()
	profile := contexty.Descriptor{ID: "fixture.profile", Revision: "1"}
	identity := contexty.Descriptor{ID: "fixture.codec", Revision: "1"}
	registry := contexty.NewExtensionRegistry()
	registry.RegisterOpaquePayload("fixture.durable-state", identity, func(raw []byte) (contexty.Extension, error) {
		var payload durableOpaquePayload
		err := json.Unmarshal(raw, &payload)
		return payload, err
	})
	codec := contexty.JSONSerializer{Provenance: contexty.DefaultProvenanceRegistry(), Extensions: registry}
	dependency := contexty.TextMessage(contexty.RoleUser, "original")
	dependency.ID = "dependency"
	ref, err := contexty.MessageContentRef(dependency, codec)
	require.NoError(t, err)
	carrier := contexty.TextMessage(contexty.RoleAssistant, "answer")
	carrier.ID = "carrier"
	carrier.Extensions = []contexty.Extension{contexty.OpaqueState{
		ID: "signature", Codec: identity, Payload: durableOpaquePayload{Bytes: []byte{0, 255, 7}},
		Placement: contexty.OpaquePlacement{AfterPart: 0}, Binding: contexty.OpaqueBinding{
			Profile:  profile,
			Required: []contexty.ContentRef{ref},
			Prefix:   []contexty.ContentRef{ref},
			Boundary: dependency.ID,
		},
	}}
	return dependency, carrier, codec, profile
}

func TestOpaqueCheckpointRoundTripAndAtomicInvalidation(t *testing.T) {
	// Arrange: durable state binds exact history bytes and their ordered prefix.
	dependency, carrier, codec, profile := durableOpaqueFixture(t)
	state := contexty.EmptyState().WithSegment(contexty.SegmentHistory, []contexty.Message{dependency, carrier})
	store := contexty.NewMemoryConversationStateStore(contexty.WithMemoryStateCodec(contexty.ConversationCodec{
		Provenance: codec.Provenance, Extensions: codec.Extensions, OpaqueProfile: profile,
	}))
	// Act: persist and restore through the declared codec and checkpoint boundary.
	checkpoint, err := contexty.ProjectCheckpoint(state, codec, profile)
	require.NoError(t, err)
	err = store.CommitState(context.Background(), "opaque", 0, contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment,
		Segment:   contexty.SegmentHistory,
		Messages:  checkpoint.Segment(contexty.SegmentHistory),
	})
	require.NoError(t, err)
	restored, err := store.LoadState(context.Background(), "opaque")
	require.NoError(t, err)
	// Assert: bindings and host bytes survive, and invalid writes publish no state.
	require.True(
		t,
		contexty.MessagesEqual(state.Segment(contexty.SegmentHistory), restored.Segment(contexty.SegmentHistory)),
	)
	changed := dependency.Clone()
	changed.Parts = []contexty.ContentPart{contexty.TextPart{Text: "changed"}}
	err = store.CommitState(context.Background(), "opaque", restored.Version(), contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment,
		Segment:   contexty.SegmentHistory,
		Messages:  []contexty.Message{changed, carrier},
	})
	require.ErrorIs(t, err, contexty.ErrOpaqueStateInvalidated)
	after, err := store.LoadState(context.Background(), "opaque")
	require.NoError(t, err)
	require.Equal(t, restored.Version(), after.Version())
	require.True(
		t,
		contexty.MessagesEqual(restored.Segment(contexty.SegmentHistory), after.Segment(contexty.SegmentHistory)),
	)
}

func TestOpaquePersistenceProjectionChecksRestoredFullScope(t *testing.T) {
	// Arrange: persistence restores source across system/history instead of using accepted prompt bytes.
	dependency, carrier, codec, profile := durableOpaqueFixture(t)
	result := contexty.CompileResult{}
	result.Source.System = []contexty.Message{dependency}
	result.Source.History = []contexty.Message{carrier}
	result.Payload.System = []contexty.Message{dependency}
	result.Payload.History = []contexty.Message{carrier}
	// Act / Assert: full state validates across segment boundaries.
	restored, err := result.DerivePersistenceState(codec, profile)
	require.NoError(t, err)
	require.Len(t, restored.Segment(contexty.SegmentHistory), 1)
	// Arrange: evict a dependency while preserving its state carrier.
	result.Transformations = map[string]contexty.TransformChain{
		dependency.ID: {{Action: contexty.ActionEvicted, Reason: "fixture"}},
	}
	_, err = result.DerivePersistenceState(codec, profile)
	require.ErrorIs(t, err, contexty.ErrOpaqueStateInvalidated)
}

func TestOpaqueCheckpointIncludesHostDefinedSegments(t *testing.T) {
	// Arrange: the carrier is in a host-defined segment beyond the standard channels.
	dependency, carrier, codec, profile := durableOpaqueFixture(t)
	state := contexty.EmptyState().WithSegment(contexty.SegmentHistory, []contexty.Message{dependency}).
		WithSegment(contexty.SegmentName("host-native"), []contexty.Message{carrier})
	// Act / Assert: its complete dependency scope is validated, never silently skipped.
	_, err := contexty.ProjectCheckpoint(state, codec, profile)
	require.NoError(t, err)
	_, err = contexty.ProjectCheckpoint(state.WithSegment(contexty.SegmentHistory, nil), codec, profile)
	require.ErrorIs(t, err, contexty.ErrOpaqueStateInvalidated)
}
