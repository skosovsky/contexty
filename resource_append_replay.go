package contexty

import "context"

func validateReplayResourceAppends(ctx context.Context, manifest CompileManifest,
	index map[ContentRef]SavedContent, codec JSONSerializer, codecs map[string]ResourceCodec,
) error {
	for _, resolution := range manifest.Resources {
		if resolution.Merge == nil {
			continue
		}
		merge := resolution.Merge
		artifacts, _, err := replayArtifacts(ctx, []ContentRef{merge.Inputs[0], merge.Artifact}, index, codec)
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil {
			return err
		}
		if err = validateReplayAppendBudget(manifest, artifacts[1]); err != nil {
			return err
		}
		resourceCodec := codecs[resolution.Selection.ID].forContext(ctx)
		inputCodec := resourceCodec.Messages
		inputCodec.Extensions = resourceCodec.Labels
		incoming, _, err := replayArtifacts(ctx, []ContentRef{resolution.Artifact}, index, inputCodec)
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil {
			return err
		}
		err = validateReplayAppendContent(
			ctx,
			artifacts[0],
			incoming[0],
			artifacts[1],
			merge,
			index,
			codec,
		)
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func validateReplayAppendBudget(manifest CompileManifest, artifact ContextArtifact) error {
	ref, err := ArtifactContentRef(artifact)
	if err != nil {
		return err
	}
	for _, request := range manifest.ArtifactBudgets {
		if request.Input != ref {
			continue
		}
		if artifact.Budget == nil || request.TokenLimit != artifact.Budget.TokenLimit {
			return ErrInvalidEstimateReport
		}
		return nil
	}
	if artifact.Budget != nil {
		return ErrMissingEstimateReport
	}
	return nil
}

func validateReplayAppendContent(ctx context.Context, existing, incoming, derived ContextArtifact,
	merge *ResourceArtifactMerge, index map[ContentRef]SavedContent, codec JSONSerializer,
) error {
	if incoming.MergePolicy != PolicyAppend || incoming.Lifecycle == ArtifactLifecycleEphemeral {
		return ErrResourceMismatch
	}
	if err := validateAppendArtifacts(existing, incoming); err != nil {
		return err
	}
	expected := appendArtifactPayload(existing, incoming)
	// Labels are host decisions captured in the graph, never executed on replay.
	expected.Extensions = cloneExtensions(derived.Extensions)
	expectedRef, err := ArtifactContentRef(expected)
	if err != nil || expectedRef != merge.Artifact {
		return ErrReplayContentMismatch
	}
	message, err := replayMessage(merge.Message, index, codec)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return err
	}
	if materializationErr := validateMaterializedArtifactMessage(derived, message); materializationErr != nil {
		return materializationErr
	}
	actual, err := MessageContentRef(message, codec)
	if err != nil || actual != merge.Message {
		return ErrReplayContentMismatch
	}
	return ctx.Err()
}

func validateAppendArtifacts(existing, incoming ContextArtifact) error {
	for _, artifact := range []ContextArtifact{existing, incoming} {
		if artifact.Blob != nil {
			return ErrResourceUnsupported
		}
		parts, err := artifactParts(artifact.Payload)
		if err != nil {
			return err
		}
		for _, part := range parts {
			if _, text := part.(TextPart); !text {
				return ErrResourceUnsupported
			}
		}
	}
	return nil
}
