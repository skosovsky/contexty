package contexty

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// ResourceCodec restores typed metadata, not reader/projection executions.
type ResourceCodec struct {
	Messages JSONSerializer
	Labels   *ExtensionRegistry
}

func validatedResourceProjection(
	ctx context.Context,
	resource ResolvedResource,
	codec ResourceCodec,
) (ResolvedResource, error) {
	if err := resource.Validate(ctx, codec); err != nil {
		return ResolvedResource{}, err
	}
	return resource, nil
}

func (c ResourceCodec) snapshot() ResourceCodec {
	return ResourceCodec{Messages: snapshotJSONSerializer(c.Messages), Labels: c.Labels.snapshot()}
}

// Every decoder boundary checks cancellation, including synchronous codec
// round-trip checks that may otherwise invoke the same host decoder again.
func (c ResourceCodec) forContext(ctx context.Context) ResourceCodec {
	c = c.snapshot()
	c.Messages.Extensions = resourceDecoderContext(ctx, c.Messages.Extensions)
	c.Labels = resourceDecoderContext(ctx, c.Labels)
	return c
}

func resourceDecoderContext(ctx context.Context, registry *ExtensionRegistry) *ExtensionRegistry {
	registry = registry.snapshot()
	if registry == nil {
		return nil
	}
	for name, decoder := range registry.decoders {
		if decoder == nil {
			continue
		}
		registry.decoders[name] = func(wire []byte) (Extension, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			label, err := decoder(wire)
			if canceled := ctx.Err(); canceled != nil {
				return nil, canceled
			}
			return label, err
		}
	}
	return registry
}

func (c ResourceCodec) validateConfiguration(configuration ResourceConfiguration) error {
	var profile TraceProfile
	profile.Codec = c.Messages
	profile.Labels.Registry = c.Labels
	profile.Labels.RequiredTypes = canonicalLabelTypes(configuration.Trace.RequiredLabelTypes)
	topology, err := profile.codecTopology()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReplayCodec, err)
	}
	for _, binding := range configuration.Trace.Codecs {
		key := CodecBinding{Kind: binding.Kind, Type: binding.Type, Descriptor: Descriptor{ID: "", Revision: ""}}
		intrinsic := topology[key]
		if intrinsic != nil && *intrinsic == binding.Descriptor {
			continue
		}
		profile.Codecs = append(profile.Codecs, binding)
	}
	actual, err := profile.configuration(false)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReplayCodec, err)
	}
	if !slices.Equal(actual.Codecs, configuration.Trace.Codecs) {
		return ErrReplayCodec
	}
	return nil
}

// Validate checks internal consistency without invoking a reader, projector,
// label policy or estimator. It proves evidence binding, not host authorization.
func (r ResolvedResource) Validate(ctx context.Context, codec ResourceCodec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.ID == "" || r.Resource.Validate() != nil {
		return ErrInvalidResource
	}
	if err := r.Configuration.Validate(); err != nil {
		return err
	}
	codec = codec.forContext(ctx)
	if err := codec.validateConfiguration(r.Configuration); err != nil {
		return err
	}
	bodyErr := validateResourceBody(
		ResourceReadRequest{ScopeRef: "", Resource: r.Resource, MaxBytes: r.Resource.Length},
		r.Source,
	)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if bodyErr != nil {
		return bodyErr
	}
	if err := validateResolvedArtifacts(ctx, r, codec); err != nil {
		return err
	}
	if err := validateResolvedEstimate(ctx, r, codec); err != nil {
		return err
	}
	return validateResolvedLineage(ctx, r)
}

func validateResolvedArtifacts(ctx context.Context, resource ResolvedResource, codec ResourceCodec) error {
	labels := LabelProjection{Policy: nil, Registry: codec.Labels, RequiredTypes: nil}
	for _, artifact := range []ContextArtifact{resource.Source.Artifact, resource.Projected, resource.Artifact} {
		if err := ctx.Err(); err != nil {
			return err
		}
		artifactErr := validateResourceArtifact(artifact)
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if artifactErr != nil {
			return artifactErr
		}
		if err := labels.validateLabels(ctx, artifact.Extensions, false); err != nil {
			return err
		}
	}
	// Label transport may only change labels and union source ancestry, not the
	// host projection's content, lifecycle, persistence or immutable object binding.
	expected := resource.Projected.Clone()
	expected.Extensions = cloneExtensions(resource.Artifact.Extensions)
	expected.SourceRefs = cloneSourceRefs(resource.Projected.SourceRefs)
	for _, ref := range resource.Source.Artifact.SourceRefs {
		if !slices.Contains(expected.SourceRefs, ref) {
			expected.SourceRefs = append(expected.SourceRefs, ref)
		}
	}
	actualRef, err := ArtifactContentRef(resource.Artifact)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return err
	}
	expectedRef, err := ArtifactContentRef(expected)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil || actualRef != expectedRef {
		return ErrResourceMismatch
	}
	return ctx.Err()
}

func validateResolvedEstimate(ctx context.Context, resource ResolvedResource, codec ResourceCodec) error {
	if err := resource.Estimate.Validate(); err != nil {
		return err
	}
	if resource.Estimate.Total > resource.Estimate.EffectiveLimit {
		return ErrBudgetExceeded
	}
	profile, err := estimateDigest(resource.Configuration.Estimate)
	if err != nil || profile != resource.Estimate.ProfileDigest {
		return ErrStaleEstimate
	}
	if len(resource.Estimate.Segments) != 1 || resource.Estimate.Segments[0].Name != resourceEstimateSegment ||
		len(resource.Estimate.Segments[0].Messages) != 1 {
		return ErrInvalidEstimateReport
	}
	materialized, err := artifactMessage(resource.Artifact)
	if err != nil {
		return err
	}
	expected, err := MessageContentRef(materialized, codec.Messages)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReplayCodec, err)
	}
	actual, err := MessageContentRef(resource.Message, codec.Messages)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReplayCodec, err)
	}
	if expected != actual || actual != resource.Estimate.Segments[0].Messages[0] {
		return ErrStaleEstimate
	}
	return ctx.Err()
}

func validateResolvedLineage(ctx context.Context, resource ResolvedResource) error {
	if err := resource.Lineage.Validate(); err != nil {
		return err
	}
	if len(resource.Lineage.Records) != 4 || len(resource.Lineage.Unresolved) != 0 {
		return ErrInvalidLineage
	}
	projected, err := ArtifactContentRef(resource.Projected)
	if err != nil {
		return err
	}
	decision := resource.Lineage.Records[2].DecisionRef
	if resource.Configuration.Labels == resourceMetadataIdentity() && decision != "" {
		return ErrInvalidTrustUpgrade
	}
	request := ResourceResolveRequest{
		ID:     resource.ID,
		Read:   ResourceReadRequest{ScopeRef: "", Resource: resource.Resource, MaxBytes: resource.Resource.Length},
		Budget: resource.Estimate.Budget,
	}
	expected, err := resourceLineage(
		request,
		resource.Configuration,
		projected,
		resource.Artifact,
		resource.Estimate,
		decision,
	)
	if err != nil {
		return err
	}
	actualWire, err := json.Marshal(resource.Lineage)
	if err != nil {
		return err
	}
	expectedWire, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	actualDigest, err := canonicalJSONDigest(actualWire)
	if err != nil {
		return err
	}
	expectedDigest, err := canonicalJSONDigest(expectedWire)
	if err != nil || expectedDigest != actualDigest {
		return ErrInvalidLineage
	}
	return ctx.Err()
}
