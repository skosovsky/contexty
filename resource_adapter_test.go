package contexty_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/resource/memory"
)

func TestResource_AdapterNativeFailures(t *testing.T) {
	for _, scenario := range []string{"missing", "revision", "digest", "denied", "oversize", "unsupported", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: native deferred uses a real authorized reader, not a resolver fake.
			resolver, request, body := fixtureResourceFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bodies := []contexty.ResourceBody{body}
			switch scenario {
			case "missing":
				bodies = nil
			case "revision":
				bodies[0].Reference.Revision = "changed"
			case "digest":
				bodies[0].Artifact.Payload.Text = "changed body"
			case "oversize":
				bodies[0].Artifact.Payload.Text = strings.Repeat("large", 100)
			case "unsupported":
				resolver.Projection = fixtureResourcePolicy(
					func(_ context.Context, received contexty.ResourceBody) (contexty.ContextArtifact, error) {
						received.Artifact.Kind = "unsupported"
						return received.Artifact, nil
					},
				)
			}
			reader, err := memory.New(memory.Config{MaxBodyBytes: 4096,
				Authorize: func(context.Context, contexty.ResourceReadRequest) error {
					if scenario == "canceled" {
						cancel()
						return contexty.ErrResourceDenied
					}
					if scenario == "denied" {
						return contexty.ErrResourceDenied
					}
					return nil
				}}, bodies...)
			require.NoError(t, err)
			resolver.Reader = reader
			engine := contexty.NewEngine(contexty.WithDeferredBlocks(fixtureAdapterResourceBlock(t, resolver, request)))
			// Act / Assert: no missing/error placeholder, permissions or partial prompt.
			result, compileErr := engine.CompileSnapshot(ctx, contexty.CompileRequest{})
			require.ErrorIs(t, compileErr, map[string]error{"missing": contexty.ErrResourceMissing,
				"revision": contexty.ErrResourceMismatch, "digest": contexty.ErrResourceMismatch, "denied": contexty.ErrResourceDenied,
				"oversize": contexty.ErrResourceSizeLimit, "unsupported": contexty.ErrResourceUnsupported, "canceled": context.Canceled}[scenario])
			require.Zero(t, result)
		})
	}
}

func TestResource_AdapterNativeSelection(t *testing.T) {
	// Arrange: two resources have the same name but different opaque identities.
	resolver, request, selected := fixtureResourceFixture(t)
	other := selected.Clone()
	other.Reference.ID = "other-source"
	other.Artifact.ID = "other-body"
	other.Artifact.Payload.Text = "not selected"
	otherDescriptor, err := contexty.DescribeResource(other.Reference, request.Read.Resource.Name, other.Artifact)
	require.NoError(t, err)
	require.Equal(t, request.Read.Resource.Name, otherDescriptor.Name)
	var authorized []string
	reader, err := memory.New(memory.Config{MaxBodyBytes: 4096,
		Authorize: func(_ context.Context, received contexty.ResourceReadRequest) error {
			authorized = append(authorized, received.Resource.Reference.ID)
			require.Equal(t, request.Read.ScopeRef, received.ScopeRef)
			return nil
		}}, selected, other)
	require.NoError(t, err)
	resolver.Reader = reader
	project := resolver.Projection
	resolver.Projection = fixtureResourcePolicy(
		func(ctx context.Context, body contexty.ResourceBody) (contexty.ContextArtifact, error) {
			require.Equal(t, selected.Reference, body.Reference)
			artifact, projectErr := project.ProjectResource(ctx, body)
			artifact.Lifecycle = contexty.ArtifactLifecyclePersistent
			return artifact, projectErr
		},
	)
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(fixtureAdapterResourceBlock(t, resolver, request)))
	// Act: two targets consume the single selected body with no additional reads.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{Name: "one", SourceSegment: contexty.SegmentMemory},
			{Name: "two", SourceSegment: contexty.SegmentMemory},
		},
	})
	// Assert: name collision neither selects the wrong resource nor expands authorization.
	require.NoError(t, err)
	require.Equal(t, []string{selected.Reference.ID}, authorized)
	require.Equal(t, "safe", result.Payload.Memory[0].TextContent())
	for _, projection := range result.Projections {
		require.Equal(t, "safe", projection.Messages[0].TextContent())
	}
	require.Empty(t, result.Source.Memory)
	require.Equal(t, selected.Reference, result.Source.DeferredResources[0].Resource.Reference)
}
