package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResourceMaterializationAcceptedRepresentation(t *testing.T) {
	// Arrange: projected artifact body and chosen prompt representation differ.
	resolver, request, _ := fixtureResourceFixture(t)
	calls := 0
	resolver.Materialization = &contexty.ArtifactMaterializationPolicy{
		Identity: contexty.Descriptor{ID: "host/resource-data", Revision: "v1"},
		Materialize: func(_ context.Context, artifact contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
			calls++
			require.Equal(t, "safe", artifact.Payload.Text)
			return contexty.ArtifactRepresentation{
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "x"}},
			}, nil
		},
	}
	codec := contexty.ResourceCodec{Messages: contexty.DefaultJSONSerializer()}
	// Act.
	result, err := resolver.Resolve(context.Background(), request)
	require.NoError(t, err)
	wire, err := contexty.EncodeResolvedResource(context.Background(), result, codec)
	require.NoError(t, err)
	restored, err := contexty.DecodeResolvedResource(context.Background(), wire, codec)
	// Assert: estimate and pure restoration bind chosen representation, not artifact text.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, contexty.RoleUser, restored.Message.Role)
	require.Equal(t, "x", restored.Message.TextContent())
	require.Equal(t, "safe", restored.Artifact.Payload.Text)
	require.Equal(t, "private body", restored.Source.Artifact.Payload.Text)
	require.Equal(t, 1, restored.Estimate.Total)
	require.Equal(t, resolver.Materialization.Identity, restored.Lineage.Records[3].Transform)
}
