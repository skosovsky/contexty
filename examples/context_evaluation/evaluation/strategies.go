package evaluation

import (
	"context"
	"fmt"
	"slices"

	"github.com/skosovsky/contexty"
	blobmemory "github.com/skosovsky/contexty/adapters/blob/memory"
	resourcememory "github.com/skosovsky/contexty/adapters/resource/memory"
)

type fixturePreview struct{}

func (fixturePreview) PreviewBlob(_ context.Context, _ contexty.BlobContent) (contexty.BlobContent, error) {
	return contexty.BlobContent{
		MIMEType: "text/plain",
		Bytes:    []byte("Completed evidence offloaded; restore requires explicit bounded host read."),
	}, nil
}

func prepareOffload(ctx context.Context, fixture Fixture, callbacks *Callbacks) (contexty.CompileRequest, error) {
	request := contexty.CompileRequest{}
	storage, err := blobmemory.New(blobmemory.Config{Namespace: "evaluation", MaxObjectBytes: maxFixtureBody,
		Authorize: func(_ context.Context, access blobmemory.Access) error {
			if access.ScopeRef != "evaluation-write" {
				return contexty.ErrBlobDenied
			}
			if access.Action == blobmemory.ActionPut {
				callbacks.BlobWrites++
			}
			return nil
		}})
	if err != nil {
		return request, err
	}
	policy, err := contexty.NewBlobThresholdPolicy(
		contexty.BlobThresholdLimits{MaxInlineBytes: inlineThreshold, MaxBlobBytes: maxFixtureBody},
		fixturePreview{},
	)
	if err != nil {
		return request, err
	}
	offloader := contexty.BlobOffloader{
		Policy:         policy,
		PolicyIdentity: identity("fixture-explicit-offload"),
		Storage:        storage,
	}
	rounds, err := contexty.InspectToolRoundStates(fixture.Messages, nil)
	if err != nil {
		return request, err
	}
	roundByStart := make(map[int]contexty.ToolRoundObservation)
	for _, round := range rounds {
		roundByStart[round.Start] = round
	}
	for i := 0; i < len(fixture.Messages); i++ {
		round, ok := roundByStart[i]
		if !ok || round.State != contexty.ToolRoundComplete {
			request.History = append(request.History, fixture.Messages[i].Clone())
			continue
		}
		block := fixture.Messages[round.Start : round.End+1]
		large := largeRound(block)
		if !large {
			request.History = append(request.History, cloneMessages(block)...)
			i = round.End
			continue
		}
		// The whole completed round is host-selected out. Its call arguments and
		// original result are never mutated; separately derived data gets a preview.
		artifacts, projectErr := offloadRound(ctx, offloader, block)
		if projectErr != nil {
			return contexty.CompileRequest{}, projectErr
		}
		request.Artifacts = append(request.Artifacts, artifacts...)
		i = round.End
	}
	return request, nil
}

type retrievalProjection struct{}

func (retrievalProjection) ProjectResource(
	_ context.Context,
	body contexty.ResourceBody,
) (contexty.ContextArtifact, error) {
	// Selected chunks keep their original bytes, including adversarial data.
	// The host materializer assigns a data role; no instruction is executed here.
	return body.Artifact.Clone(), nil
}

func prepareRetrieval(ctx context.Context, fixture Fixture, reporter *contexty.EstimateReporter, budget int,
	callbacks *Callbacks) (contexty.CompileRequest, error) {
	request := contexty.CompileRequest{}
	// The source archive exists independently of the working window. Selection
	// uses only pinned source IDs, never an inferred provider capability.
	inputs, err := retrievalInputs(fixture)
	if err != nil {
		return request, err
	}
	request.History = inputs.history
	bodies, pendingIDs, selected := inputs.bodies, inputs.pendingIDs, inputs.selected
	reader, err := resourcememory.New(resourcememory.Config{MaxBodyBytes: maxFixtureBody,
		Authorize: func(_ context.Context, request contexty.ResourceReadRequest) error {
			if request.ScopeRef != "evaluation-read" {
				return contexty.ErrResourceDenied
			}
			callbacks.ResourceReads++
			return nil
		}}, bodies...)
	if err != nil {
		return request, err
	}
	policy := materialization()
	resolver := contexty.ResourceResolver{
		Materialization: &policy, Reader: reader, ReaderIdentity: identity("fixture-archive-reader"),
		Projection: retrievalProjection{}, ProjectionIdentity: identity("fixture-source-chunk"), Reporter: reporter}
	for _, body := range bodies {
		descriptor, descriptorErr := contexty.DescribeResource(body.Reference, "source chunk", body.Artifact)
		if descriptorErr != nil {
			return contexty.CompileRequest{}, descriptorErr
		}
		resolved, resolveErr := resolver.Resolve(ctx, contexty.ResourceResolveRequest{
			ID: "read/" + body.Reference.ID,
			Read: contexty.ResourceReadRequest{
				ScopeRef: "evaluation-read",
				Resource: descriptor,
				MaxBytes: descriptor.Length,
			},
			Budget: contexty.EffectiveInputBudget(budget),
		})
		if resolveErr != nil {
			return contexty.CompileRequest{}, fmt.Errorf("selected source: %w", resolveErr)
		}
		request.Memory = append(request.Memory, resolved.Message.Clone())
	}
	// Keep the current task/question under the same issued budget as every other strategy.
	if len(fixture.Messages) > 0 {
		last := fixture.Messages[len(fixture.Messages)-1]
		if !pendingIDs[last.ID] && !selected[last.ID] && !slices.Contains(fixture.RequiredIDs, last.ID) {
			request.History = append(request.History, last.Clone())
		}
	}
	// Pending protocol state is protected by the library, not archived as data.
	rounds, err := contexty.InspectToolRoundStates(fixture.Messages, nil)
	if err != nil {
		return request, err
	}
	for _, round := range rounds {
		if round.State != contexty.ToolRoundComplete {
			request.History = append(request.History, cloneMessages(fixture.Messages[round.Start:round.End+1])...)
		}
	}
	return request, nil
}

func largeRound(block []contexty.Message) bool {
	for _, message := range block {
		if len(messageText(message)) > inlineThreshold {
			return true
		}
	}
	return false
}

func offloadRound(
	ctx context.Context,
	offloader contexty.BlobOffloader,
	block []contexty.Message,
) ([]contexty.ContextArtifact, error) {
	var artifacts []contexty.ContextArtifact
	for _, result := range block[1:] {
		artifact := contexty.NewRetrievalDocument(
			"evidence-"+result.ID,
			contexty.TextPayload(messageText(result)),
		).ContextArtifact
		artifact.SourceRefs = slices.Clone(result.SourceRefs)
		artifact.Lifecycle = contexty.ArtifactLifecycleEphemeral
		outcome, projectErr := offloader.ProjectArtifact(
			ctx,
			contexty.BlobArtifactRequest{
				ID:                       "offload-" + result.ID,
				Artifact:                 artifact,
				ScopeRef:                 "evaluation-write",
				RetentionRef:             "evaluation-retain-" + result.ID,
				MaxPreviewBytes:          previewBound,
				AllowedPreviewMediaTypes: []string{"text/plain"},
			},
		)
		if projectErr != nil {
			return nil, projectErr
		}
		artifacts = append(artifacts, outcome.Artifact.Clone())
	}
	return artifacts, nil
}

type retrievalInput struct {
	history    []contexty.Message
	bodies     []contexty.ResourceBody
	pendingIDs map[string]bool
	selected   map[string]bool
}

func retrievalInputs(fixture Fixture) (retrievalInput, error) {
	inputs := retrievalInput{}
	selected := make(map[string]bool)
	for _, sourceID := range fixture.QuerySourceIDs {
		selected[sourceID] = true
	}
	rounds, err := contexty.InspectToolRoundStates(fixture.Messages, nil)
	if err != nil {
		return retrievalInput{}, err
	}
	pendingIDs := make(map[string]bool)
	for _, round := range rounds {
		if round.State != contexty.ToolRoundComplete {
			for _, message := range fixture.Messages[round.Start : round.End+1] {
				pendingIDs[message.ID] = true
			}
		}
	}
	var bodies []contexty.ResourceBody
	for _, message := range fixture.Messages {
		if pendingIDs[message.ID] {
			continue
		}
		if slices.Contains(fixture.RequiredIDs, message.ID) {
			inputs.history = append(inputs.history, message.Clone())
			continue
		}
		if !selected[message.ID] {
			continue
		}
		artifact := contexty.NewRetrievalDocument(
			"archive-"+message.ID,
			contexty.TextPayload(messageText(message)),
		).ContextArtifact
		artifact.SourceRefs = slices.Clone(message.SourceRefs)
		bodies = append(bodies, contexty.ResourceBody{Reference: identity("archive/" + message.ID), Artifact: artifact})
	}
	inputs.bodies, inputs.pendingIDs, inputs.selected = bodies, pendingIDs, selected
	return inputs, nil
}
