// Progressive disclosure: host discovery/selection, bounded authorized reading,
// and resource evidence in native deferred compile. Run with: go run .
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/skosovsky/contexty"
)

const (
	inputLimit     = 256
	pinnedIdentity = "pinned"
)

type previewPolicy struct{}

func (previewPolicy) ProjectResource(_ context.Context, body contexty.ResourceBody) (contexty.ContextArtifact, error) {
	artifact := body.Artifact.Clone()
	artifact.ID = "preview/" + body.Reference.ID
	artifact.Lifecycle = contexty.ArtifactLifecycleEphemeral
	// Host chooses the representation. Core does not parse instructions or grant
	// capabilities because of their text or role.
	return artifact, nil
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	provider, err := newHostProvider()
	if err != nil {
		return err
	}
	for _, query := range []string{"checkpoint", "permissions"} {
		choices := provider.Search(query)
		if len(choices) == 0 {
			return fmt.Errorf("no chunk for %q", query)
		}
		selected := choices[0]
		fmt.Printf(
			"Search %q: %s revision=%s bytes=%d\n",
			query,
			selected.Name,
			selected.Reference.Revision,
			selected.Length,
		)
		result, compileErr := compileChunk(ctx, provider, selected)
		if compileErr != nil {
			return compileErr
		}
		fmt.Printf("Selected content: %s\n", result.Payload.Memory[0].TextContent())
		persisted, persistErr := result.DerivePersistenceState(hostCodec(), contexty.Descriptor{ID: "", Revision: ""})
		if persistErr != nil {
			return persistErr
		}
		fmt.Printf(
			"Source descriptors=%d source bodies=%d persisted bodies=%d\n",
			len(result.Source.DeferredResources),
			len(result.Source.Memory),
			len(persisted.Segment(contexty.SegmentMemory)),
		)
	}
	selected := provider.Search("checkpoint")[0]
	stale := selected
	stale.Reference.Revision = "obsolete"
	resolver, err := hostResolver(provider)
	if err != nil {
		return err
	}
	for _, rejection := range []struct {
		request  contexty.ResourceReadRequest
		expected error
	}{
		{contexty.ResourceReadRequest{ScopeRef: authorizedScope, Resource: stale, MaxBytes: stale.Length}, contexty.ErrResourceMismatch},
		{contexty.ResourceReadRequest{ScopeRef: "denied", Resource: selected, MaxBytes: selected.Length}, contexty.ErrResourceDenied},
	} {
		_, resolveErr := resolver.Resolve(
			ctx,
			contexty.ResourceResolveRequest{
				ID:     "rejected-read",
				Read:   rejection.request,
				Budget: contexty.EffectiveInputBudget(inputLimit),
			},
		)
		if !errors.Is(resolveErr, rejection.expected) {
			return fmt.Errorf("expected %s, got %w", rejection.expected.Error(), resolveErr)
		}
		fmt.Printf("Rejected read: %v\n", resolveErr)
	}
	fmt.Printf("Delivered bodies: %v; reader attempts: %v\n", provider.deliveredIDs, provider.readIDs)
	return nil
}

func compileChunk(
	ctx context.Context,
	provider *hostProvider,
	selected contexty.ResourceDescriptor,
) (contexty.CompileResult, error) {
	block, err := resourceBlock(provider, selected)
	if err != nil {
		return contexty.CompileResult{}, err
	}
	engine := contexty.NewEngine(
		contexty.WithArtifactMaterialization(*hostMaterialization()),
		contexty.WithTraceProfile(hostTrace()),
		contexty.WithDeferredBlocks(block),
	)
	return engine.CompileSnapshot(
		ctx,
		contexty.CompileRequest{ //nolint:exhaustruct_v5 // only selected resources are issued
			CompilationID: "jit/" + selected.Reference.ID,
			Targets: []contexty.CompileTarget{
				{
					Name:               "selected",
					Segments:           []contexty.SegmentName{contexty.SegmentMemory},
					IncludeArtifacts:   true,
					View:               "",
					ArtifactRefs:       nil,
					IncludeCurrentTurn: false,
					Selection:          nil,
					Budget:             nil,
					Formatter:          nil,
				},
			},
		},
	)
}

func resourceBlock(
	reader contexty.ResourceReader,
	selected contexty.ResourceDescriptor,
) (contexty.DeferredBlock, error) {
	resolver, err := hostResolver(reader)
	if err != nil {
		return contexty.DeferredBlock{}, err
	}
	request := contexty.ResourceResolveRequest{ID: "selected-read", Read: contexty.ResourceReadRequest{
		ScopeRef: authorizedScope,
		Resource: selected,
		MaxBytes: selected.Length,
	}, Budget: contexty.EffectiveInputBudget(inputLimit)}
	configuration, err := resolver.Configuration()
	if err != nil {
		return contexty.DeferredBlock{}, err
	}
	return contexty.DeferredBlock{ //nolint:exhaustruct_v5 // default memory segment/append placement
		Name:          "selected-content",
		ResourceCodec: contexty.ResourceCodec{Messages: hostCodec(), Labels: hostCodec().Extensions},
		Resources: []contexty.ResourceSelection{{ID: request.ID, Resource: selected, Configuration: configuration,
			Budget: request.Budget, MaxBytes: request.Read.MaxBytes}},
		Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
			resolved, resolveErr := resolver.Resolve(ctx, request)
			return contexty.DeferredResult{Messages: nil, Resources: []contexty.ResolvedResource{resolved}}, resolveErr
		},
	}, nil
}

func estimateProfile() contexty.EstimateProfile {
	return contexty.EstimateProfile{ //nolint:exhaustruct_v5 // no permissive fallback or host extensions
		Model:     contexty.Descriptor{ID: "host-model", Revision: pinnedIdentity},
		Estimator: contexty.Descriptor{ID: "character-count", Revision: pinnedIdentity},
		Method:    contexty.Descriptor{ID: "approximate-characters", Revision: pinnedIdentity},
		Encoding:  contexty.Descriptor{ID: "typed-json", Revision: pinnedIdentity},
		Extensions: map[string]contexty.EstimateExtensionPolicy{
			labelType: {
				Codec:        contexty.Descriptor{ID: labelCodecID, Revision: pinnedIdentity},
				Policy:       contexty.Descriptor{ID: "host-label-estimate", Revision: pinnedIdentity},
				MetadataOnly: true,
			},
		},
		Capabilities: map[contexty.EstimateKind]contexty.EstimateQuality{
			contexty.EstimateText: contexty.EstimateEstimated, contexty.EstimateToolResult: contexty.EstimateEstimated,
			contexty.EstimateImage: contexty.EstimateUnknown, contexty.EstimateToolCall: contexty.EstimateUnknown,
			contexty.EstimateMedia: contexty.EstimateUnknown, contexty.EstimateExtension: contexty.EstimateUnknown,
		},
	}
}
