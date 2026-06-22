// Progressive disclosure: host discovery/selection, bounded authorized reading,
// and resource evidence in native deferred compile. Run with: go run .
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/resource/memory"
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
	bodies := hostBodies()
	catalog, err := hostCatalog(bodies)
	if err != nil {
		return err
	}
	for _, descriptor := range catalog {
		fmt.Printf("Available: %s (%s)\n", descriptor.Name, descriptor.Reference.ID)
	}
	// Selection/discovery remain in the application. Equal names never resolve
	// ambiguously because the selected descriptor pins its own opaque identity.
	selected := catalog[1]
	reads := 0
	reader, err := memory.New(memory.Config{MaxBodyBytes: inputLimit * inputLimit,
		Authorize: func(_ context.Context, request contexty.ResourceReadRequest) error {
			if request.ScopeRef != "current-read" || request.Resource.Reference.ID != selected.Reference.ID {
				return contexty.ErrResourceDenied
			}
			reads++
			return nil
		}}, bodies...)
	if err != nil {
		return err
	}
	block, err := resourceBlock(reader, selected)
	if err != nil {
		return err
	}
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(block))
	result, err := engine.CompileSnapshot(
		ctx,
		contexty.CompileRequest{ //nolint:exhaustruct_v5 // optional inputs omitted
			Targets: []contexty.CompileTarget{
				{Name: "selected", SourceSegment: contexty.SegmentMemory, View: "", Budget: nil, Formatter: nil},
			},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Loaded bodies: %d\n", reads)
	fmt.Printf("Selected content: %s\n", result.Payload.Memory[0].TextContent())
	fmt.Printf(
		"Descriptors in Source: %d; body messages in Source: %d\n",
		len(result.Source.DeferredResources),
		len(result.Source.Memory),
	)
	fmt.Printf("Ordinary persisted messages: %d\n", len(result.DerivePersistenceProjection(contexty.SegmentMemory)))
	return nil
}

func hostBodies() []contexty.ResourceBody {
	var bodies []contexty.ResourceBody
	for _, source := range []string{"source-a", "source-b"} {
		artifact := contexty.NewRetrievalDocument(
			source,
			contexty.TextPayload("Host-selected document from "+source),
		).ContextArtifact
		artifact.SourceRefs = []contexty.SourceRef{{ID: source}} //nolint:exhaustruct_v5 // opaque source identity only
		bodies = append(
			bodies,
			contexty.ResourceBody{
				Reference: contexty.Descriptor{ID: source, Revision: pinnedIdentity},
				Artifact:  artifact,
			},
		)
	}
	return bodies
}

func hostCatalog(bodies []contexty.ResourceBody) ([]contexty.ResourceDescriptor, error) {
	var catalog []contexty.ResourceDescriptor
	for _, body := range bodies {
		descriptor, err := contexty.DescribeResource(body.Reference, "guide", body.Artifact)
		if err != nil {
			return nil, err
		}
		catalog = append(catalog, descriptor)
	}
	return catalog, nil
}

func resourceBlock(
	reader contexty.ResourceReader,
	selected contexty.ResourceDescriptor,
) (contexty.DeferredBlock, error) {
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		estimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	if err != nil {
		return contexty.DeferredBlock{}, err
	}
	resolver := contexty.ResourceResolver{ //nolint:exhaustruct_v5 // no host label policy or extra codecs
		Reader:             reader,
		ReaderIdentity:     contexty.Descriptor{ID: "host-reader", Revision: pinnedIdentity},
		Projection:         previewPolicy{},
		ProjectionIdentity: contexty.Descriptor{ID: "host-preview", Revision: pinnedIdentity},
		Reporter:           reporter,
	}
	request := contexty.ResourceResolveRequest{ID: "selected-read", Read: contexty.ResourceReadRequest{
		ScopeRef: "current-read",
		Resource: selected,
		MaxBytes: selected.Length,
	}, Budget: contexty.EffectiveInputBudget(inputLimit)}
	configuration, err := resolver.Configuration()
	if err != nil {
		return contexty.DeferredBlock{}, err
	}
	return contexty.DeferredBlock{ //nolint:exhaustruct_v5 // default memory segment/append placement
		Name:          "selected-content",
		ResourceCodec: contexty.ResourceCodec{Messages: contexty.DefaultJSONSerializer(), Labels: nil},
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
		Capabilities: map[contexty.EstimateKind]contexty.EstimateQuality{
			contexty.EstimateText: contexty.EstimateEstimated, contexty.EstimateToolResult: contexty.EstimateEstimated,
			contexty.EstimateImage: contexty.EstimateUnknown, contexty.EstimateToolCall: contexty.EstimateUnknown,
			contexty.EstimateMedia: contexty.EstimateUnknown, contexty.EstimateExtension: contexty.EstimateUnknown,
		},
	}
}
