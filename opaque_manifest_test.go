package contexty

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

type manifestOpaquePayload struct {
	Bytes []byte `json:"bytes"`
}

func (manifestOpaquePayload) ExtensionType() string       { return "test.manifest.opaque" }
func (p manifestOpaquePayload) CloneExtension() Extension { p.Bytes = slices.Clone(p.Bytes); return p }

func opaqueEvidenceFixture(
	t *testing.T,
	invalid bool,
) (*OpaqueStateDecision, map[ContentRef]SavedContent, JSONSerializer, []Message) {
	t.Helper()
	codec := DefaultJSONSerializer()
	encoding := Descriptor{ID: "host/encoding", Revision: "1"}
	profile := Descriptor{ID: "host/profile", Revision: "1"}
	codec.Extensions.RegisterOpaquePayload("test.manifest.opaque", encoding, func(data []byte) (Extension, error) {
		var p manifestOpaquePayload
		err := json.Unmarshal(data, &p)
		return p, err
	})
	dependency := TextMessage(RoleUser, "original")
	dependency.ID = "dependency"
	ref, err := MessageContentRef(dependency, codec)
	require.NoError(t, err)
	owner := TextMessage(RoleAssistant, "answer")
	owner.ID = "owner"
	owner.Extensions = []Extension{
		OpaqueState{
			ID:        "signature",
			Codec:     encoding,
			Payload:   manifestOpaquePayload{Bytes: []byte{0, 255}},
			Placement: OpaquePlacement{AfterPart: 0},
			Binding:   OpaqueBinding{Profile: profile, Required: []ContentRef{ref}},
		},
	}
	if invalid {
		dependency.Parts = []ContentPart{TextPart{Text: "changed"}}
	}
	accepted := owner.Clone()
	accepted.Extensions = nil
	before := AbstractPayload{History: []Message{dependency, owner}}
	after := AbstractPayload{History: []Message{dependency, accepted}}
	inputs, err := manifestSnapshotSegments(payloadSnapshot(before), codec)
	require.NoError(t, err)
	outputs, err := manifestSnapshotSegments(payloadSnapshot(after), codec)
	require.NoError(t, err)
	ownerRef, err := MessageContentRef(owner, codec)
	require.NoError(t, err)
	decision := &OpaqueStateDecision{
		Configuration: OpaqueStateConfiguration{
			Policy:      Descriptor{ID: "host/policy", Revision: "1"},
			Profile:     profile,
			Invalidated: OpaqueDropInvalid,
		},
		Inputs:  inputs,
		Outputs: outputs,
		Dropped: []OpaqueStateDrop{{Message: ownerRef, StateID: "signature"}},
	}
	index := make(map[ContentRef]SavedContent)
	for _, message := range []Message{dependency, owner, accepted} {
		messageRef, refErr := MessageContentRef(message, codec)
		require.NoError(t, refErr)
		wire, wireErr := codec.Marshal(message)
		require.NoError(t, wireErr)
		index[messageRef] = SavedContent{
			Ref:  messageRef,
			Kind: SavedMessage,
			Wire: wire,
		}
	}
	return decision, index, codec, after.History
}

func TestOpaqueEvidence_ExactDropProofAndNoHiddenMutation(t *testing.T) {
	// Arrange: changed dependency invalidates an unchanged signed state.
	decision, index, codec, final := opaqueEvidenceFixture(t, true)
	// Act / Assert: exact state removal is valid without host calls.
	require.NoError(t, decision.Validate())
	require.NoError(t, validateReplayOpaqueTransition(decision, index, codec, final))
	// Arrange: forged drop changes unrelated accepted text as well.
	changed := final[1].Clone()
	changed.Parts = []ContentPart{TextPart{Text: "hidden edit"}}
	ref, err := MessageContentRef(changed, codec)
	require.NoError(t, err)
	wire, err := codec.Marshal(changed)
	require.NoError(t, err)
	index[ref] = SavedContent{
		Ref:  ref,
		Kind: SavedMessage,
		Wire: wire,
	}
	decision.Outputs[1].Messages[1] = ref
	// Act / Assert: lineage alone cannot authorize a content edit.
	require.ErrorIs(
		t,
		validateReplayOpaqueTransition(decision, index, codec, []Message{final[0], changed}),
		ErrInvalidOpaqueState,
	)
}

func TestOpaqueEvidence_RejectValidStateRemovalAndForgedIdentity(t *testing.T) {
	// Arrange: dependencies are unchanged, so the state remains applicable.
	decision, index, codec, final := opaqueEvidenceFixture(t, false)
	// Act / Assert.
	require.ErrorIs(t, validateReplayOpaqueTransition(decision, index, codec, final), ErrInvalidOpaqueState)
	decision.Dropped[0].StateID = "invented"
	require.ErrorIs(t, validateReplayOpaqueTransition(decision, index, codec, final), ErrInvalidOpaqueState)
	decision.Configuration.Invalidated = OpaqueFailClosed
	require.ErrorIs(t, decision.Validate(), ErrInvalidOpaqueState)
}

func TestOpaqueEvidence_ConfigurationCloneAndReplayIdentity(t *testing.T) {
	// Arrange.
	decision, _, _, _ := opaqueEvidenceFixture(t, true)
	config := CompileConfiguration{
		OpaqueState: &decision.Configuration,
	}
	cloned := config.clone()
	// Act.
	cloned.OpaqueState.Profile.Revision = "2"
	// Assert: caller updates cannot mutate an accepted expectation.
	require.Equal(t, "1", config.OpaqueState.Profile.Revision)
	require.NotEqual(t, *config.OpaqueState, *cloned.OpaqueState)
	cloneDecision := decision.clone()
	cloneDecision.Inputs[1].Messages[0].ID = "different"
	require.NotEqual(t, decision.Inputs, cloneDecision.Inputs)
}
