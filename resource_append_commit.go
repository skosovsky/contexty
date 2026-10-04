package contexty

import "context"

func (e *Engine) admitResourceAppend(ctx context.Context, state *resourceCompileState,
	resource ResolvedResource, index int,
) (bool, error) {
	existing := state.active[index].Clone()
	projection, err := e.projectResourceAppend(ctx, resource, existing)
	if err != nil {
		return false, err
	}
	admitted, err := e.selectArtifacts(ctx, state.turnID, []ContextArtifact{projection.artifact})
	if err != nil {
		return false, err
	}
	projection.record.Prepared = len(admitted) != 0
	if err = importResourceAppend(ctx, projection.record.Lineage); err != nil {
		return false, err
	}
	err = captureResourceAppend(ctx, existing, projection)
	if canceled := ctx.Err(); canceled != nil {
		return false, canceled
	}
	if err != nil {
		return false, err
	}
	state.appends[resource.ID] = projection
	if projection.record.Prepared {
		state.active[index] = projection.artifact.Clone()
		state.removed["artifact:"+existing.ID] = true
	}
	return projection.record.Prepared, ctx.Err()
}

func importResourceAppend(ctx context.Context, graph Lineage) error {
	if trace := traceFromContext(ctx); trace != nil {
		for _, record := range graph.Records {
			if err := trace.appendRecord(record); err != nil {
				return err
			}
			for _, ref := range record.Outputs {
				trace.latest[baseContentRef(ref)] = ref
			}
		}
	}
	return ctx.Err()
}

func captureResourceAppend(ctx context.Context, existing ContextArtifact, projection resourceAppendProjection) error {
	if capture := contentCaptureFrom(ctx); capture != nil {
		for _, artifact := range []ContextArtifact{existing, projection.artifact} {
			if err := capture.artifactAtStage(ctx, artifact, CaptureTransform, "resource-append"); err != nil {
				return err
			}
		}
		if err := capture.message(
			ctx,
			projection.message,
			CaptureTransform,
			"resource-append-materialize",
		); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func resourceMaterialization(ctx context.Context, resource ResolvedResource) Message {
	if state := resourceStateFrom(ctx); state != nil {
		if projection, found := state.appends[resource.ID]; found {
			return projection.message.Clone()
		}
	}
	return resource.Message.Clone()
}

func compileArtifactEvidence(ctx context.Context, source []ContextArtifact) []ContextArtifact {
	artifacts := compileArtifactInputs(ctx, source)
	if state := resourceStateFrom(ctx); state != nil {
		for _, resource := range state.resources {
			if projection, found := state.appends[resource.ID]; found {
				artifacts = append(artifacts, projection.artifact.Clone())
			}
		}
	}
	return artifacts
}

func manifestDerivedArtifactRefs(manifest CompileManifest) []ContentRef {
	var refs []ContentRef
	for _, resource := range manifest.Resources {
		if resource.Merge != nil {
			refs = append(refs, resource.Merge.Artifact)
		}
	}
	return uniqueContentRefs(refs)
}

func (r ResourceArtifactMerge) clone() ResourceArtifactMerge {
	r.Inputs = append([]ContentRef(nil), r.Inputs...)
	r.Lineage = r.Lineage.Clone()
	return r
}
