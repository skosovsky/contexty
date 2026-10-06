package contexty

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemediation_SessionBindings(t *testing.T) {
	// Arrange: a shared pass and a branch using the existing callback context bridge.
	parent := newCompileSession(context.Background())
	parent.Recorder = newTransformRecorder(nil)
	parent.Resources = &resourceCompileState{}
	parent.Identity = compileIdentitySettings{turnID: "turn"}
	parent.HasIdentity = true
	ctx := parent.bind()
	// Act: target overrides only branch-local bindings.
	branch := withCompileIdentity(ctx, nil, false, "turn", "target")
	branch = withTransformRecorder(branch, newTransformRecorder(nil))
	// Assert: parent bindings cannot be changed by target binding; resource collection is shared.
	require.Empty(t, parent.Identity.targetName)
	require.NotSame(t, parent.Recorder, transformRecorderFrom(branch))
	require.Same(t, parent.Resources, resourceStateFrom(branch))
	require.Equal(t, "target", compileSessionFrom(branch).Identity.targetName)
	require.Equal(t, "turn", parent.Identity.turnID)
}

func TestRemediation_AppendOwnership(t *testing.T) {
	// Arrange: independently owned source state and delta input.
	old := TextMessage(RoleUser, "old")
	old.ID = "old"
	added := TextMessage(RoleUser, "added")
	added.ID = "added"
	state := EmptyState().WithSegment(SegmentHistory, []Message{old})
	delta := ConversationDelta{Operation: DeltaAppendMessages, Segment: SegmentHistory, Messages: []Message{added}}
	// Act: apply then mutate every caller-owned input/output.
	next, err := ApplyDelta(state, delta)
	require.NoError(t, err)
	delta.Messages[0].Parts[0] = TextPart{Text: "mutated"}
	output := next.Segment(SegmentHistory)
	output[0].Parts[0] = TextPart{Text: "mutated"}
	output[1].Parts[0] = TextPart{Text: "mutated"}
	// Assert: both immutable views retain their data.
	require.Equal(t, "old", state.Segment(SegmentHistory)[0].TextContent())
	require.Equal(t, "old", next.Segment(SegmentHistory)[0].TextContent())
	require.Equal(t, "added", next.Segment(SegmentHistory)[1].TextContent())
}

func TestRemediation_SemanticEnvelope(t *testing.T) {
	// Arrange: semantic wire data need not already be a valid persistence proposal.
	artifact := ContextArtifact{ID: "artifact", Lifecycle: "host-unknown"}
	input := EmptyState().WithVersion(-1).WithArtifact(artifact)
	codec := ConversationCodec{}
	// Act: both semantic directions preserve the same envelope.
	wire, err := codec.Encode(input)
	require.NoError(t, err)
	output, err := codec.Decode(wire)
	// Assert: OCC and lifecycle admission remain separate from lossless codec.
	require.NoError(t, err)
	require.Equal(t, int64(-1), output.Version())
	require.Equal(t, ArtifactLifecycle("host-unknown"), output.Artifacts()[0].Lifecycle)
	_, err = NextConversationVersion(output.Version())
	require.Error(t, err)
}

type sessionTestProvenance struct{ Type string }

func (p sessionTestProvenance) ProvenanceType() string      { return p.Type }
func (p sessionTestProvenance) CloneProvenance() Provenance { return p }

func TestRemediation_RegistryConcurrentFreeze(t *testing.T) {
	// Arrange: a frozen registry before concurrent registration/decode.
	registry := NewProvenanceRegistry()
	frozen := registry.snapshot()
	var wait sync.WaitGroup
	failures := make(chan error, 32)
	// Act: unrelated registrations may proceed while codecs take their own snapshots.
	for index := range 32 {
		name := strconv.Itoa(index)
		wait.Go(func() {
			registry.Register(name, func([]byte) (Provenance, error) { return sessionTestProvenance{Type: name}, nil })
			wire, err := EncodeProvenance(sessionTestProvenance{Type: name})
			if err != nil {
				failures <- err
				return
			}
			if _, err = registry.snapshot().Decode(wire); err != nil {
				failures <- err
			}
		})
	}
	wait.Wait()
	close(failures)
	// Assert: registration/decode race safely, while earlier frozen configuration stays unchanged.
	for failure := range failures {
		require.NoError(t, failure)
	}
	wire, err := EncodeProvenance(sessionTestProvenance{Type: "0"})
	require.NoError(t, err)
	_, err = frozen.Decode(wire)
	require.Error(t, err)
}

func TestRemediation_InvalidEventUTF8(t *testing.T) {
	for _, input := range []struct{ turn, prefix string }{{"\xff", "host"}, {"turn", "\xfe"}} {
		// Arrange: distinct malformed byte strings cannot become JSON replacement identities.
		policy := NewStableMessageIdentityPolicy(input.prefix)
		// Act.
		_, err := policy.ResolveMessageID(MessageIdentityContext{TurnID: input.turn, CurrentTurn: true}, Message{})
		// Assert: no durable identity is published for lossy string encoding.
		require.ErrorIs(t, err, ErrMissingEventIdentity)
	}
}
