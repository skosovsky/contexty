package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestJIT_MetadataDiscoveryThenTwoBoundedReads(t *testing.T) {
	// Arrange: source bytes live inside the provider; search exposes metadata only.
	provider, err := newHostProvider()
	require.NoError(t, err)
	first := provider.Search("checkpoint")
	second := provider.Search("permissions")
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.Empty(t, provider.readIDs)
	require.Empty(t, provider.deliveredIDs)
	require.Equal(t, provider.Search("recovery checkpoint"), provider.Search("checkpoint recovery"))

	// Act: two separate steps choose different chunk descriptors.
	for _, selected := range []contexty.ResourceDescriptor{first[0], second[0]} {
		result, compileErr := compileChunk(context.Background(), provider, selected)
		require.NoError(t, compileErr)

		// Assert: chosen bodies carry canonical version, provenance and labels;
		// ephemeral JIT bodies do not silently become source/checkpoint messages.
		require.Len(t, result.Payload.Memory, 1)
		message := result.Payload.Memory[0]
		require.Equal(t, contexty.RoleUser, message.Role)
		require.Equal(t, "v2", selected.Reference.Revision)
		require.Contains(
			t,
			message.SourceRefs,
			contexty.SourceRef{
				Namespace:    "host-guide",
				Kind:         "chunk",
				ID:           selected.Reference.ID,
				CheckpointID: "",
				URI:          "",
			},
		)
		require.Equal(t, []contexty.Extension{sourceLabel{Classification: "external-evidence"}}, message.Extensions)
		require.Empty(t, result.Source.Memory)
		require.Len(t, result.Source.DeferredResources, 1)
		persisted, persistErr := result.DerivePersistenceState(hostCodec(), contexty.Descriptor{ID: "", Revision: ""})
		require.NoError(t, persistErr)
		require.Empty(t, persisted.Segment(contexty.SegmentMemory))
	}
	require.Equal(t, []string{"guide/checkpoint", "guide/permissions"}, provider.readIDs)
	require.Equal(t, provider.readIDs, provider.deliveredIDs)
}

func TestJIT_StaleDeniedAndBoundedReadExposeNoBody(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		mutate      func(*contexty.ResourceReadRequest)
		want        error
		readerCalls int
	}{
		{"stale revision", func(request *contexty.ResourceReadRequest) { request.Resource.Reference.Revision = "v1" }, contexty.ErrResourceMismatch, 1},
		{"stale digest", func(request *contexty.ResourceReadRequest) {
			request.Resource.Content.Digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}, contexty.ErrResourceMismatch, 1},
		{"denied scope", func(request *contexty.ResourceReadRequest) { request.ScopeRef = "denied" }, contexty.ErrResourceDenied, 1},
		{"insufficient bound", func(request *contexty.ResourceReadRequest) { request.MaxBytes-- }, contexty.ErrResourceSizeLimit, 0},
		{"forged smaller length", func(request *contexty.ResourceReadRequest) { request.Resource.Length--; request.MaxBytes-- }, contexty.ErrResourceSizeLimit, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange.
			provider, err := newHostProvider()
			require.NoError(t, err)
			selected := provider.Search("checkpoint")[0]
			request := contexty.ResourceReadRequest{
				ScopeRef: authorizedScope,
				Resource: selected,
				MaxBytes: selected.Length,
			}
			scenario.mutate(&request)
			resolver, err := hostResolver(provider)
			require.NoError(t, err)

			// Act.
			resolved, resolveErr := resolver.Resolve(
				context.Background(),
				contexty.ResourceResolveRequest{
					ID:     "rejected",
					Read:   request,
					Budget: contexty.EffectiveInputBudget(inputLimit),
				},
			)

			// Assert: genuine resolver+reader contracts fail atomically, without body.
			require.ErrorIs(t, resolveErr, scenario.want)
			require.Equal(t, contexty.ResolvedResource{}, resolved)
			require.Empty(t, provider.deliveredIDs)
			require.Len(t, provider.readIDs, scenario.readerCalls)
		})
	}
}

func TestJIT_DescriptorDoesNotAuthorizeUnusedChunk(t *testing.T) {
	// Arrange: even a genuine current descriptor is no capability grant.
	provider, err := newHostProvider()
	require.NoError(t, err)
	selected := provider.Search("deployment")[0]
	resolver, err := hostResolver(provider)
	require.NoError(t, err)

	// Act.
	resolved, err := resolver.Resolve(
		context.Background(),
		contexty.ResourceResolveRequest{
			ID: "unused",
			Read: contexty.ResourceReadRequest{
				ScopeRef: authorizedScope,
				Resource: selected,
				MaxBytes: selected.Length,
			},
			Budget: contexty.EffectiveInputBudget(inputLimit),
		},
	)

	// Assert.
	require.ErrorIs(t, err, contexty.ErrResourceDenied)
	require.Equal(t, contexty.ResolvedResource{}, resolved)
	require.Empty(t, provider.deliveredIDs)
	require.Equal(t, []string{"guide/unused"}, provider.readIDs)
}

func TestJIT_ResolverVerifiesActualBodyAndRequiredLabels(t *testing.T) {
	for _, scenario := range []string{"changed provider bytes", "missing label codec"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: metadata cannot conceal a changed body or absent host codec.
			provider, err := newHostProvider()
			require.NoError(t, err)
			selected := provider.Search("checkpoint")[0]
			resolver, err := hostResolver(provider)
			require.NoError(t, err)
			expected := contexty.ErrResourceMismatch
			if scenario == "changed provider bytes" {
				changed := provider.sources[selected.Reference.ID]
				changed.text = "Changed source bytes after metadata publication."
				provider.sources[selected.Reference.ID] = changed
			} else {
				resolver.Labels.Registry = nil
				expected = contexty.ErrInvalidRecordingComponent
			}

			// Act.
			resolved, resolveErr := resolver.Resolve(
				context.Background(),
				contexty.ResourceResolveRequest{
					ID: "verify",
					Read: contexty.ResourceReadRequest{
						ScopeRef: authorizedScope,
						Resource: selected,
						MaxBytes: selected.Length,
					},
					Budget: contexty.EffectiveInputBudget(inputLimit),
				},
			)

			// Assert: resolver rejects the chosen raw body before issuing prompt data.
			require.ErrorIs(t, resolveErr, expected)
			require.Equal(t, contexty.ResolvedResource{}, resolved)
		})
	}
}
