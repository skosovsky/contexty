package contexty

import (
	"context"
	"reflect"
	"slices"
)

// ResourceResolution is metadata-only evidence of actual resolution. Required
// body/projection/message bytes remain separate under host privacy policy.
type ResourceResolution struct {
	Selection ResourceSelection      `json:"selection"`
	Projected ContentRef             `json:"projected"`
	Artifact  ContentRef             `json:"artifact"`
	Estimate  EstimateReport         `json:"estimate"`
	Lineage   Lineage                `json:"lineage"`
	Merge     *ResourceArtifactMerge `json:"merge,omitempty"`
}

func (r ResourceResolution) Validate() error {
	if r.Merge != nil {
		if err := r.Merge.Validate(r.Selection.ID, r.Artifact.ID); err != nil {
			return err
		}
		if baseContentRef(r.Merge.Inputs[1]) != r.Artifact {
			return ErrInvalidResource
		}
	}
	if err := r.Selection.Validate(); err != nil {
		return err
	}
	for _, ref := range []ContentRef{r.Projected, r.Artifact} {
		if ref.Validate() != nil || ref.Occurrence != "" {
			return ErrInvalidResource
		}
	}
	if err := r.Estimate.Validate(); err != nil {
		return err
	}
	if r.Estimate.Budget != r.Selection.Budget || r.Estimate.Total > r.Estimate.EffectiveLimit {
		return ErrStaleEstimate
	}
	profile, err := estimateDigest(r.Selection.Configuration.Estimate)
	if err != nil || profile != r.Estimate.ProfileDigest || len(r.Estimate.Segments) != 1 ||
		r.Estimate.Segments[0].Name != resourceEstimateSegment || len(r.Estimate.Segments[0].Messages) != 1 {
		return ErrStaleEstimate
	}
	if err := r.validateLineage(); err != nil {
		return err
	}
	if r.Merge != nil && r.Merge.Inputs[1] != r.Lineage.Records[2].Outputs[0] {
		return ErrInvalidLineage
	}
	return nil
}

func (r ResourceResolution) validateLineage() error {
	if len(r.Lineage.Records) != 4 || len(r.Lineage.Unresolved) != 0 {
		return ErrInvalidLineage
	}
	decision := r.Lineage.Records[2].DecisionRef
	if r.Selection.Configuration.Labels == resourceMetadataIdentity() && decision != "" {
		return ErrInvalidTrustUpgrade
	}
	request := ResourceResolveRequest{ID: r.Selection.ID,
		Read:   ResourceReadRequest{ScopeRef: "", Resource: r.Selection.Resource, MaxBytes: r.Selection.MaxBytes},
		Budget: r.Selection.Budget}
	expected, err := resourceLineageRefs(
		request,
		r.Selection.Configuration,
		r.Projected,
		r.Artifact,
		r.Estimate,
		decision,
	)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, r.Lineage) {
		return ErrInvalidLineage
	}
	return nil
}

func manifestResourceResolutions(ctx context.Context, source CompileRequest) ([]ResourceResolution, error) {
	state := resourceStateFrom(ctx)
	var records []ResourceResolution
	if state == nil {
		return records, nil
	}
	for index, resource := range state.resources {
		projected, err := ArtifactContentRef(resource.Projected)
		if err != nil {
			return nil, err
		}
		artifact, err := ArtifactContentRef(resource.Artifact)
		if err != nil {
			return nil, err
		}
		estimate, err := resource.Estimate.Clone()
		if err != nil {
			return nil, err
		}
		selection := source.DeferredResources[index]
		selection.Configuration = selection.Configuration.Clone()
		record := ResourceResolution{
			Selection: selection,
			Projected: projected,
			Artifact:  artifact,
			Estimate:  estimate,
			Lineage:   resource.Lineage.Clone(),
			Merge:     nil,
		}
		if projection, found := state.appends[resource.ID]; found {
			merge := projection.record.clone()
			record.Merge = &merge
		}
		if err = record.Validate(); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, ctx.Err()
}

func validateManifestResources(manifest CompileManifest) error {
	var selections []ResourceSelection
	for _, block := range manifest.CompileConfiguration.Deferred {
		selections = append(selections, block.Resources...)
	}
	if len(selections) != len(manifest.Resources) {
		return ErrInvalidResource
	}
	for index, resource := range manifest.Resources {
		if !reflect.DeepEqual(selections[index], resource.Selection) {
			return ErrInvalidResource
		}
		if err := resource.Validate(); err != nil {
			return err
		}
		if err := validateResourceAppendAdmission(manifest, resource.Merge); err != nil {
			return err
		}
		graph, err := resourceManifestGraph(resource, manifest.Stages)
		if err != nil {
			return err
		}
		for _, output := range manifest.Outputs {
			for _, record := range graph.Records {
				if !slices.ContainsFunc(output.Lineage.Records, func(actual LineageRecord) bool {
					return reflect.DeepEqual(record, actual)
				}) {
					return ErrInvalidLineage
				}
			}
		}
	}
	return nil
}

func resourceManifestGraph(resource ResourceResolution, stages map[string]Descriptor) (Lineage, error) {
	graph := resource.Lineage.Clone()
	if resource.Merge != nil {
		if resource.Merge.Lineage.Records[0].Transform != stages["merge"] {
			return Lineage{}, ErrInvalidDescriptor
		}
		graph.Records = append(graph.Records, resource.Merge.Lineage.Records...)
	}
	return graph, nil
}

func manifestTransformContentKind(manifest CompileManifest, ref ContentRef) SavedContentKind {
	for _, resource := range manifest.Resources {
		if resource.Merge != nil && baseContentRef(ref) == resource.Merge.Artifact {
			return SavedArtifact
		}
		if slices.Contains(
			[]ContentRef{resource.Selection.Resource.Content, resource.Projected, resource.Artifact},
			baseContentRef(ref),
		) {
			return SavedArtifact
		}
	}
	return SavedMessage
}
