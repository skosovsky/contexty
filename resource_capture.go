package contexty

import "context"

func captureResolvedResource(ctx context.Context, resource ResolvedResource) error {
	capture := contentCaptureFrom(ctx)
	if capture == nil {
		return nil
	}
	for _, candidate := range []struct {
		artifact ContextArtifact
		stage    string
	}{{resource.Source.Artifact, "resource-read"}, {resource.Projected, "resource-project"}, {resource.Artifact, "resource-labels"}} {
		err := capture.artifactAtStage(ctx, candidate.artifact, CaptureTransform, candidate.stage)
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil {
			return err
		}
	}
	err := capture.message(ctx, resource.Message, CaptureTransform, "resource-materialize")
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	return err
}
