package contexty_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

type opaqueExportPayload struct {
	Bytes []byte `json:"bytes"`
}

func (p opaqueExportPayload) ExtensionType() string { return "test.export.opaque" }

func (p opaqueExportPayload) CloneExtension() contexty.Extension {
	return opaqueExportPayload{Bytes: append([]byte(nil), p.Bytes...)}
}

func opaqueExportFixture(t *testing.T) (contexty.CompileProjection, contexty.JSONSerializer, contexty.Descriptor) {
	t.Helper()
	codec := contexty.DefaultJSONSerializer()
	identity := contexty.Descriptor{ID: "test.export.codec", Revision: "1"}
	codec.Extensions.RegisterOpaquePayload(
		"test.export.opaque",
		identity,
		func(data []byte) (contexty.Extension, error) {
			var payload opaqueExportPayload
			if err := json.Unmarshal(data, &payload); err != nil {
				return nil, err
			}
			return payload, nil
		},
	)
	dependency := contexty.TextMessage(contexty.RoleUser, "approved context")
	dependency.ID = "dependency"
	ref, err := contexty.MessageContentRef(dependency, codec)
	require.NoError(t, err)
	profile := contexty.Descriptor{ID: "test.export.consumer", Revision: "1"}
	owner := contexty.TextMessage(contexty.RoleAssistant, "answer")
	owner.ID = "owner"
	owner.Extensions = []contexty.Extension{
		contexty.OpaqueState{
			ID:        "signed-state",
			Codec:     identity,
			Payload:   opaqueExportPayload{Bytes: []byte("OPAQUE-SECRET")},
			Placement: contexty.OpaquePlacement{AfterPart: 0},
			Binding: contexty.OpaqueBinding{
				Profile:  profile,
				Required: []contexty.ContentRef{ref},
				Prefix:   []contexty.ContentRef{ref},
				Boundary: dependency.ID,
			},
		},
	}
	return contexty.CompileProjection{Messages: []contexty.Message{dependency, owner}}, codec, profile
}

func TestOpaqueExport_DefaultMetadataAllowlistDoesNotDiscloseState(t *testing.T) {
	// Arrange: generic extension metadata approval does not approve opaque protocol state.
	projection, codec, _ := opaqueExportFixture(t)
	// Act.
	exported, err := contexty.ExportProjection(projection, contexty.ExportSelection{
		MessageIDs: []string{"dependency", "owner"},
		Metadata: contexty.ExportMetadata{
			ExtensionTypes: []string{contexty.OpaqueStateExtensionType, "test.export.opaque"},
		},
	}, codec)
	// Assert.
	require.NoError(t, err)
	wire, err := json.Marshal(exported)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "signed-state")
	var owner contexty.Message
	require.NoError(t, codec.Unmarshal(exported.Messages[1], &owner))
	require.Empty(t, owner.Extensions)
	require.Len(t, projection.Messages[1].Extensions, 1)
}

func TestOpaqueExport_ExplicitCompatibleStateRoundTrips(t *testing.T) {
	// Arrange.
	projection, codec, profile := opaqueExportFixture(t)
	// Act.
	exported, err := contexty.ExportProjection(projection, contexty.ExportSelection{
		MessageIDs: []string{"dependency", "owner"}, OpaqueStateIDs: []string{"signed-state"}, OpaqueProfile: profile,
	}, codec)
	// Assert: bytes, ordering and dependency binding are exactly the approved values.
	require.NoError(t, err)
	var owner contexty.Message
	require.NoError(t, codec.Unmarshal(exported.Messages[1], &owner))
	require.Equal(t, projection.Messages[1].Extensions, owner.Extensions)
	require.NoError(t, contexty.ValidateOpaqueState([]contexty.Message{projection.Messages[0], owner}, codec, profile))
}

func TestOpaqueExport_InvalidSelectionsFailAtomically(t *testing.T) {
	// Arrange.
	projection, codec, profile := opaqueExportFixture(t)
	selections := []contexty.ExportSelection{
		{MessageIDs: []string{"owner"}, OpaqueStateIDs: []string{"signed-state"}, OpaqueProfile: profile},
		{MessageIDs: []string{"owner", "dependency"}, OpaqueStateIDs: []string{"signed-state"}, OpaqueProfile: profile},
		{MessageIDs: []string{"dependency"}, OpaqueStateIDs: []string{"signed-state"}, OpaqueProfile: profile},
		{MessageIDs: []string{"dependency", "owner"}, OpaqueStateIDs: []string{"missing"}, OpaqueProfile: profile},
		{
			MessageIDs:     []string{"dependency", "owner"},
			OpaqueStateIDs: []string{"signed-state", "signed-state"},
			OpaqueProfile:  profile,
		},
		{MessageIDs: []string{"dependency", "owner"}, OpaqueStateIDs: []string{"signed-state"}},
		{
			MessageIDs:     []string{"dependency", "owner"},
			OpaqueStateIDs: []string{"signed-state"},
			OpaqueProfile:  contexty.Descriptor{ID: profile.ID, Revision: "2"},
		},
	}
	for _, selection := range selections {
		// Act.
		exported, err := contexty.ExportProjection(projection, selection, codec)
		// Assert.
		require.ErrorIs(t, err, contexty.ErrInvalidExportSelection)
		require.Zero(t, exported)
	}
}

func TestOpaqueExport_MetadataFilteringCannotPreserveStaleBinding(t *testing.T) {
	// Arrange: the state was signed for metadata that this consumer does not approve.
	projection, codec, profile := opaqueExportFixture(t)
	projection.Messages[0].SourceRefs = []contexty.SourceRef{{ID: "private-source"}}
	ref, err := contexty.MessageContentRef(projection.Messages[0], codec)
	require.NoError(t, err)
	state, ok := projection.Messages[1].Extensions[0].(contexty.OpaqueState)
	require.True(t, ok)
	state.Binding.Required = []contexty.ContentRef{ref}
	state.Binding.Prefix = []contexty.ContentRef{ref}
	projection.Messages[1].Extensions[0] = state
	selection := contexty.ExportSelection{
		MessageIDs:     []string{"dependency", "owner"},
		OpaqueStateIDs: []string{"signed-state"},
		OpaqueProfile:  profile,
	}
	// Act.
	exported, err := contexty.ExportProjection(projection, selection, codec)
	// Assert: dependencies are never recovered by silently revealing their metadata.
	require.ErrorIs(t, err, contexty.ErrInvalidExportSelection)
	require.Zero(t, exported)
	require.Equal(t, "private-source", projection.Messages[0].SourceRefs[0].ID)
	// Act: explicitly approved metadata allows the unchanged bound revision.
	selection.Metadata.SourceRefs = true
	exported, err = contexty.ExportProjection(projection, selection, codec)
	// Assert.
	require.NoError(t, err)
	require.Len(t, exported.Messages, 2)
}

func TestOpaqueExport_MissingOrLossyHostCodecFailsClosed(t *testing.T) {
	// Arrange: the exact context is approved, but the consumer lacks the required codec.
	projection, codec, profile := opaqueExportFixture(t)
	selection := contexty.ExportSelection{
		MessageIDs:     []string{"dependency", "owner"},
		OpaqueStateIDs: []string{"signed-state"},
		OpaqueProfile:  profile,
	}
	unknown := codec
	unknown.Extensions = contexty.NewExtensionRegistry()
	// Act.
	exported, err := contexty.ExportProjection(projection, selection, unknown)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingOpaqueStateCodec)
	require.Zero(t, exported)
	// Arrange: a decoder claims the right identity but mutates the host-owned bytes.
	lossy := codec
	lossy.Extensions = contexty.NewExtensionRegistry()
	lossy.Extensions.RegisterOpaquePayload(
		"test.export.opaque",
		contexty.Descriptor{ID: "test.export.codec", Revision: "1"},
		func(_ []byte) (contexty.Extension, error) {
			return opaqueExportPayload{Bytes: []byte("different bytes")}, nil
		},
	)
	// Act.
	exported, err = contexty.ExportProjection(projection, selection, lossy)
	// Assert.
	require.Error(t, err)
	require.Zero(t, exported)
}
