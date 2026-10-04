package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestExport_PrivateEnvelope(t *testing.T) {
	// Arrange: a projection retains private local snapshots and metadata.
	private := contexty.TextMessage(contexty.RoleSystem, "PRIVATE-SYSTEM-PAYLOAD")
	private.ID = "private-system"
	public := contexty.TextMessage(contexty.RoleUser, "approved payload")
	public.ID = "public"
	public.Actor = &contexty.Actor{ID: "PRIVATE-ACTOR"}
	public.SourceRefs = []contexty.SourceRef{{URI: "PRIVATE-RESOLVER-HANDLE"}}
	artifact := contexty.NewMemoryBlock("approved-artifact", contexty.TextPayload("approved blob")).ContextArtifact
	artifact.ArtifactType = "PRIVATE-DOMAIN-TYPE"
	artifact.OwnerRef = &contexty.SourceRef{URI: "PRIVATE-OWNER"}
	artifact.BoundTurnID = "PRIVATE-TURN"
	artifact.SourceRefs = []contexty.SourceRef{{URI: "PRIVATE-ARTIFACT-HANDLE"}}
	result, err := fixtureEngine(contexty.WithTraceProfile(fixtureTraceProfile())).CompileSnapshot(
		context.Background(), contexty.CompileRequest{
			CompilationID: "PRIVATE-COMPILE-ID",
			System:        []contexty.Message{private},
			History:       []contexty.Message{public},
			Artifacts:     []contexty.ContextArtifact{artifact},
			Targets: []contexty.CompileTarget{
				{Segments: []contexty.SegmentName{contexty.SegmentHistory}, IncludeArtifacts: true, Name: "consumer"},
			},
		})
	require.NoError(t, err)
	selection := contexty.ExportSelection{
		MessageIDs:          []string{"public"},
		ArtifactPayloadRefs: []contexty.ContentRef{fixtureArtifactContentRef(t, artifact)},
		Metadata:            contexty.ExportMetadata{Lineage: true},
	}
	projection := result.Projections["consumer"]
	// Act: serialize the entire envelope, not just visible text.
	envelope, err := contexty.ExportProjection(projection,
		selection, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	wire, err := json.Marshal(envelope)
	// Assert: no implicit local metadata, snapshots or handles are disclosed.
	require.NoError(t, err)
	require.NotContains(t, string(wire), "PRIVATE-")
	require.NotContains(t, string(wire), "private-system")
	for _, key := range []string{"input_snapshot", "bound_turn_id", "owner_ref", "lifecycle", "persistence"} {
		require.NotContains(t, string(wire), key)
	}
	require.Contains(t, string(wire), "approved payload")
	require.Contains(t, string(wire), "approved blob")
	require.Len(t, envelope.Lineage, 1)
	require.Equal(t, 1, envelope.Lineage[0].OmittedInputs)
	require.Empty(t, envelope.Lineage[0].Inputs)
	require.Equal(t, "PRIVATE-RESOLVER-HANDLE", result.Projections["consumer"].Messages[0].SourceRefs[0].URI)
	require.Equal(t, "PRIVATE-OWNER", artifact.OwnerRef.URI)
}

func TestExport_SameIDDoesNotApproveRawRevision(t *testing.T) {
	// Arrange: the same message identity has a secret raw and approved safe revision.
	raw := contexty.TextMessage(contexty.RoleUser, "PRIVATE-RAW-PAYLOAD")
	raw.ID = "turn"
	safe := contexty.TextMessage(contexty.RoleUser, "safe")
	safe.ID = raw.ID
	rawRef := fixtureRefForMessage(t, raw)
	safeRef := fixtureRefForMessage(t, safe)
	projection := contexty.CompileProjection{Messages: []contexty.Message{safe},
		Lineage: contexty.Lineage{Records: []contexty.LineageRecord{{ID: "PRIVATE-INVOCATION",
			Transform: contexty.Descriptor{ID: "redact", Revision: "pinned"},
			Inputs:    []contexty.ContentRef{rawRef}, Outputs: []contexty.ContentRef{safeRef}}}}}
	selection := contexty.ExportSelection{
		MessageIDs: []string{safe.ID},
		Metadata:   contexty.ExportMetadata{Lineage: true},
	}
	// Act.
	envelope, err := contexty.ExportProjection(projection, selection, contexty.DefaultJSONSerializer())
	// Assert: ID permission never silently authorizes the historical raw digest.
	require.NoError(t, err)
	require.Len(t, envelope.Lineage, 1)
	require.Equal(t, 1, envelope.Lineage[0].OmittedInputs)
	wire, err := json.Marshal(envelope)
	require.NoError(t, err)
	require.NotContains(t, string(wire), rawRef.Digest)
	require.NotContains(t, string(wire), "PRIVATE-")
	// Arrange: explicit opaque-digest permission discloses a hash, not payload.
	selection.AllowOpaqueDigests = true
	// Act.
	envelope, err = contexty.ExportProjection(projection, selection, contexty.DefaultJSONSerializer())
	// Assert.
	require.NoError(t, err)
	require.Equal(t, []string{rawRef.Digest}, envelope.Lineage[0].OpaqueInputDigests)
	require.Empty(t, envelope.Lineage[0].Inputs)
}

func TestExport_UnorderedGraph(t *testing.T) {
	// Arrange: imported graph is valid but deliberately not topologically ordered.
	a := contexty.TextMessage(contexty.RoleUser, "a")
	a.ID = "a"
	b := contexty.TextMessage(contexty.RoleUser, "b")
	b.ID = "b"
	c := contexty.TextMessage(contexty.RoleUser, "c")
	c.ID = "c"
	refs := []contexty.ContentRef{fixtureRefForMessage(t, a), fixtureRefForMessage(t, b), fixtureRefForMessage(t, c)}
	descriptor := contexty.Descriptor{ID: "host-transform", Revision: "pinned"}
	projection := contexty.CompileProjection{Messages: []contexty.Message{c}, Lineage: contexty.Lineage{
		Records: []contexty.LineageRecord{
			{ID: "b-to-c", Transform: descriptor, Inputs: refs[1:2], Outputs: refs[2:3]},
			{ID: "a-to-b", Transform: descriptor, Inputs: refs[0:1], Outputs: refs[1:2]},
		}}}
	selection := contexty.ExportSelection{MessageIDs: []string{"c"}, LineageRefs: refs,
		Metadata: contexty.ExportMetadata{Lineage: true, TransformDescriptors: true}}
	// Act.
	envelope, err := contexty.ExportProjection(projection, selection, contexty.DefaultJSONSerializer())
	// Assert: the entire approved ancestor chain survives, without raw input bodies.
	require.NoError(t, err)
	require.Len(t, envelope.Lineage, 2)
	require.Equal(t, refs[0:1], envelope.Lineage[1].Inputs)
	require.Equal(t, &descriptor, envelope.Lineage[0].Transform)
	require.Len(t, envelope.Messages, 1)
}

func TestExport_SelectionErrors(t *testing.T) {
	// Arrange: private source is not part of the public projection.
	public := contexty.TextMessage(contexty.RoleUser, "safe")
	public.ID = "public"
	private := contexty.TextMessage(contexty.RoleSystem, "private")
	private.ID = "private"
	projection := contexty.CompileProjection{Messages: []contexty.Message{public},
		Source: contexty.CompileRequest{System: []contexty.Message{private}}}
	for _, ids := range [][]string{{"private"}, {"public", "public"}, {""}} {
		// Act.
		_, err := contexty.ExportProjection(
			projection,
			contexty.ExportSelection{MessageIDs: ids},
			contexty.DefaultJSONSerializer(),
		)
		// Assert.
		require.ErrorIs(t, err, contexty.ErrInvalidExportSelection)
	}
	// Act / Assert: artifact permission cannot resolve a nonexistent object.
	_, err := contexty.ExportProjection(
		projection,
		contexty.ExportSelection{
			ArtifactPayloadRefs: []contexty.ContentRef{{ID: "missing", Digest: fixtureRefForMessage(t, public).Digest}},
		},
		contexty.DefaultJSONSerializer(),
	)
	require.ErrorIs(t, err, contexty.ErrInvalidExportSelection)
}

func TestExport_RejectsForeignArtifact(t *testing.T) {
	// Arrange: a local projection only contains its own participating artifacts.
	artifact := contexty.NewMemoryBlock("foreign", contexty.TextPayload("PRIVATE")).ContextArtifact
	projection := contexty.CompileProjection{}
	// Act.
	envelope, err := contexty.ExportProjection(
		projection,
		contexty.ExportSelection{ArtifactPayloadRefs: []contexty.ContentRef{fixtureArtifactContentRef(t, artifact)}},
		contexty.DefaultJSONSerializer(),
	)
	// Assert.
	require.Error(t, err)
	require.Zero(t, envelope)
}
