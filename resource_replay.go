package contexty

import "context"

func isResourceMessage(manifest CompileManifest, ref ContentRef) bool {
	for _, resource := range manifest.Resources {
		if resource.Estimate.Segments[0].Messages[0] == baseContentRef(ref) {
			return true
		}
	}
	return false
}

// WithReplayResourceCodecs declares the current host codecs for each resolution.
// It is mandatory for resource-bearing replay. All IDs/topologies are checked
// before decoding; these codecs contain no readers or other runtime ports.
func WithReplayResourceCodecs(codecs map[string]ResourceCodec) ReplayOption {
	frozen := cloneResourceCodecs(codecs)
	return func(options *replayOptions) error {
		if len(frozen) == 0 || options.resources != nil {
			return ErrReplayCodec
		}
		for id := range frozen {
			if id == "" {
				return ErrReplayCodec
			}
		}
		options.resources = cloneResourceCodecs(frozen)
		return nil
	}
}

func cloneResourceCodecs(codecs map[string]ResourceCodec) map[string]ResourceCodec {
	if codecs == nil {
		return nil
	}
	result := make(map[string]ResourceCodec, len(codecs))
	for id, codec := range codecs {
		result[id] = codec.snapshot()
	}
	return result
}

func validateReplayResourceCodecs(manifest CompileManifest, codecs map[string]ResourceCodec) error {
	if len(manifest.Resources) != len(codecs) {
		return ErrReplayCodec
	}
	for _, resource := range manifest.Resources {
		codec, found := codecs[resource.Selection.ID]
		if !found {
			return ErrReplayCodec
		}
		if err := codec.validateConfiguration(resource.Selection.Configuration); err != nil {
			return err
		}
	}
	return nil
}

func validateReplayResources(ctx context.Context, manifest CompileManifest,
	index map[ContentRef]SavedContent, codecs map[string]ResourceCodec,
) error {
	for _, record := range manifest.Resources {
		codec := codecs[record.Selection.ID].forContext(ctx)
		resource, err := replayResource(ctx, record, index, codec)
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil {
			return err
		}
		if err = resource.Validate(ctx, codec); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func replayResource(ctx context.Context, record ResourceResolution, index map[ContentRef]SavedContent,
	codec ResourceCodec,
) (ResolvedResource, error) {
	refs := []ContentRef{record.Selection.Resource.Content, record.Projected, record.Artifact}
	artifactCodec := codec.Messages
	artifactCodec.Extensions = codec.Labels
	artifacts, _, err := replayArtifacts(ctx, refs, index, artifactCodec)
	if canceled := ctx.Err(); canceled != nil {
		return ResolvedResource{}, canceled
	}
	if err != nil {
		return ResolvedResource{}, err
	}
	message, err := replayMessage(record.Estimate.Segments[0].Messages[0], index, codec.Messages)
	if canceled := ctx.Err(); canceled != nil {
		return ResolvedResource{}, canceled
	}
	if err != nil {
		return ResolvedResource{}, err
	}
	return ResolvedResource{
		ID:            record.Selection.ID,
		Resource:      record.Selection.Resource,
		Configuration: record.Selection.Configuration.Clone(),
		Source:        ResourceBody{Reference: record.Selection.Resource.Reference, Artifact: artifacts[0]},
		Projected:     artifacts[1],
		Artifact:      artifacts[2],
		Message:       message,
		Estimate:      record.Estimate,
		Lineage:       record.Lineage.Clone(),
	}, nil
}
