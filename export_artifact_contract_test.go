package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestExport_CanonicalPayloadOptInAfterPromptProjection(t *testing.T) {
	// Arrange: the host redacts a retrieval prompt but retains its canonical artifact locally.
	artifact := contexty.NewRetrievalDocument(
		"retrieved",
		contexty.TextPayload("PRIVATE-RETRIEVAL"),
	).ContextArtifact.WithTurn(
		"turn",
	)
	policy := contexty.OutputPolicy{
		Identity: contexty.Descriptor{ID: "host-redaction", Revision: "1"},
		Project: func(_ context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
			for i := range input.Payload.Memory {
				input.Payload.Memory[i].Parts = []contexty.ContentPart{contexty.TextPart{Text: "approved retrieval"}}
			}
			return input.Payload, nil
		},
	}
	result, err := fixtureEngine(
		contexty.WithOutputPolicy(policy),
	).CompileSnapshot(t.Context(), contexty.CompileRequest{
		TurnID: "turn", Artifacts: []contexty.ContextArtifact{artifact},
		Targets: []contexty.CompileTarget{{Name: "consumer", IncludeArtifacts: true}},
	})
	require.NoError(t, err)
	projection := result.Projections["consumer"]
	require.Len(t, projection.Messages, 1)
	canonical := fixtureArtifactContentRef(t, artifact)
	// Act: selecting only the accepted message never approves the source payload.
	prompt, err := contexty.ExportProjection(projection,
		contexty.ExportSelection{MessageIDs: []string{projection.Messages[0].ID}}, contexty.DefaultJSONSerializer())
	// Assert.
	require.NoError(t, err)
	wire, err := json.Marshal(prompt)
	require.NoError(t, err)
	require.Contains(t, string(wire), "approved retrieval")
	require.NotContains(t, string(wire), "PRIVATE-RETRIEVAL")
	require.Empty(t, prompt.Artifacts)
	// Act: a separate exact revision opt-in intentionally exports the original payload.
	source, err := contexty.ExportProjection(
		projection,
		contexty.ExportSelection{
			ArtifactPayloadRefs: []contexty.ContentRef{canonical},
		},
		contexty.DefaultJSONSerializer(),
	)
	// Assert.
	require.NoError(t, err)
	wire, err = json.Marshal(source)
	require.NoError(t, err)
	require.Contains(t, string(wire), "PRIVATE-RETRIEVAL")
	require.Empty(t, source.Messages)
	require.Len(t, source.Artifacts, 1)
}

func TestExport_RejectsUnapprovedArtifactRevisionAtomically(t *testing.T) {
	// Arrange: a participating artifact with a safe output and an exact canonical ref.
	artifact := contexty.NewMemoryBlock("source", contexty.TextPayload("original")).ContextArtifact
	message := contexty.TextMessage(contexty.RoleUser, "safe")
	message.ID = "accepted"
	projection := contexty.CompileProjection{
		Messages: []contexty.Message{
			message,
		},
		Artifacts:   []contexty.ContextArtifact{artifact},
		ArtifactIDs: []string{artifact.ID},
	}
	canonical := fixtureArtifactContentRef(t, artifact)
	staleArtifact := artifact.Clone()
	staleArtifact.Payload = contexty.TextPayload("different revision")
	stale := fixtureArtifactContentRef(t, staleArtifact)
	occurrence := canonical
	occurrence.Occurrence = "historical/source"
	foreign := canonical
	foreign.ID = "foreign"
	for _, refs := range [][]contexty.ContentRef{
		{stale}, {occurrence}, {foreign}, {{ID: canonical.ID, Digest: "invalid"}}, {canonical, canonical},
	} {
		// Act: even a previously selected message cannot escape on artifact rejection.
		envelope, err := contexty.ExportProjection(projection,
			contexty.ExportSelection{MessageIDs: []string{message.ID}, ArtifactPayloadRefs: refs},
			contexty.DefaultJSONSerializer())
		// Assert.
		require.ErrorIs(t, err, contexty.ErrInvalidExportSelection)
		require.Zero(t, envelope)
	}
	// Arrange: the canonical artifact exists locally but did not participate in this output.
	projection.ArtifactIDs = nil
	// Act.
	envelope, err := contexty.ExportProjection(
		projection,
		contexty.ExportSelection{
			ArtifactPayloadRefs: []contexty.ContentRef{canonical},
		},
		contexty.DefaultJSONSerializer(),
	)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrInvalidExportSelection)
	require.Zero(t, envelope)
}
