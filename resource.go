package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const resourceEstimateSegment = "resource"
const traceStageResourceMaterialize = "resource-materialize"

var (
	ErrInvalidResource     = errors.New("contexty: invalid selected resource")
	ErrResourceMissing     = errors.New("contexty: resource missing")
	ErrResourceDenied      = errors.New("contexty: resource scope denied")
	ErrResourceMismatch    = errors.New("contexty: resource revision/content mismatch")
	ErrResourceSizeLimit   = errors.New("contexty: resource byte limit exceeded")
	ErrResourceUnsupported = errors.New("contexty: unsupported resource content")
)

// ResourceDescriptor is host-selected metadata, not a discoverable path or access grant.
type ResourceDescriptor struct {
	Reference Descriptor `json:"reference"`
	Name      string     `json:"name"`
	Content   ContentRef `json:"content"`
	Length    int64      `json:"length"`
}

func (d ResourceDescriptor) Validate() error {
	if d.Reference.Validate() != nil || d.Content.Validate() != nil || d.Content.Occurrence != "" || d.Length < 0 {
		return ErrInvalidResource
	}
	return nil
}

// ResourceDescriptorRef identifies selected metadata without retaining body bytes.
func ResourceDescriptorRef(descriptor ResourceDescriptor) (ContentRef, error) {
	if err := descriptor.Validate(); err != nil {
		return ContentRef{}, err
	}
	wire, err := json.Marshal(descriptor)
	if err != nil {
		return ContentRef{}, err
	}
	digest, err := canonicalJSONDigest(wire)
	if err != nil {
		return ContentRef{}, err
	}
	return ContentRef{ID: descriptor.Reference.ID, Digest: digest, Occurrence: ""}, nil
}

// DescribeResource pins complete typed content, including metadata, without I/O.
func DescribeResource(reference Descriptor, name string, artifact ContextArtifact) (ResourceDescriptor, error) {
	if err := validateResourceArtifact(artifact); err != nil {
		return ResourceDescriptor{}, err
	}
	ref, err := ArtifactContentRef(artifact)
	if err != nil {
		return ResourceDescriptor{}, err
	}
	wire, err := json.Marshal(artifact)
	if err != nil {
		return ResourceDescriptor{}, err
	}
	descriptor := ResourceDescriptor{Reference: reference, Name: name, Content: ref, Length: int64(len(wire))}
	if err = descriptor.Validate(); err != nil {
		return ResourceDescriptor{}, err
	}
	return descriptor, nil
}

type ResourceReadRequest struct {
	ScopeRef string
	Resource ResourceDescriptor
	MaxBytes int64
}

type ResourceBody struct {
	Reference Descriptor
	Artifact  ContextArtifact
}

func (b ResourceBody) Clone() ResourceBody {
	return ResourceBody{Reference: b.Reference, Artifact: b.Artifact.Clone()}
}

// ResourceReader must enforce fresh host authorization and bounded loading.
type ResourceReader interface {
	ReadResource(context.Context, ResourceReadRequest) (ResourceBody, error)
}

// ResourceProjectionPolicy selects a prompt artifact, never capabilities/actions.
type ResourceProjectionPolicy interface {
	ProjectResource(context.Context, ResourceBody) (ContextArtifact, error)
}

type ResourceResolveRequest struct {
	ID     string
	Read   ResourceReadRequest
	Budget BudgetRequest
}

type ResolvedResource struct {
	ID            string
	Resource      ResourceDescriptor
	Configuration ResourceConfiguration
	Source        ResourceBody
	Projected     ContextArtifact
	Artifact      ContextArtifact
	Message       Message
	Estimate      EstimateReport
	Lineage       Lineage
}

// ResourceResolver carries caller-owned ports; no global registry or discovery.
type ResourceResolver struct {
	Materialization     *ArtifactMaterializationPolicy
	Reader              ResourceReader
	ReaderIdentity      Descriptor
	Projection          ResourceProjectionPolicy
	ProjectionIdentity  Descriptor
	Labels              LabelProjection
	LabelPolicyIdentity Descriptor
	Codecs              []CodecBinding
	Reporter            *EstimateReporter
}

func (r ResourceResolver) Resolve(ctx context.Context, request ResourceResolveRequest) (ResolvedResource, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedResource{}, err
	}
	if err := r.validate(request); err != nil {
		return ResolvedResource{}, err
	}
	ctx = withArtifactMaterialization(ctx, r.Materialization, r.Reporter.codec)
	r.Labels.Registry = r.Labels.Registry.snapshot()
	r.Labels.RequiredTypes = slices.Clone(r.Labels.RequiredTypes)
	r.Codecs = cloneCodecBindings(r.Codecs)
	configuration, err := r.Configuration()
	if err != nil {
		return ResolvedResource{}, err
	}
	body, err := r.Reader.ReadResource(ctx, request.Read)
	if canceled := ctx.Err(); canceled != nil {
		return ResolvedResource{}, canceled
	}
	if err != nil {
		return ResolvedResource{}, err
	}
	body = body.Clone()
	if err = validateResourceBody(request.Read, body); err != nil {
		return ResolvedResource{}, err
	}
	if err = r.Labels.validateLabels(ctx, body.Artifact.Extensions, false); err != nil {
		return ResolvedResource{}, err
	}
	return r.project(ctx, request, body, configuration)
}

func (r ResourceResolver) validate(request ResourceResolveRequest) error {
	validPorts := !nilInterfaceValue(r.Reader) && !nilInterfaceValue(r.Projection) &&
		r.ReaderIdentity.Validate() == nil && r.ProjectionIdentity.Validate() == nil
	validRequest := request.ID != "" && request.Read.ScopeRef != "" && request.Read.MaxBytes >= 0 &&
		request.Read.Resource.Validate() == nil
	if !validRequest || !validPorts {
		return ErrInvalidResource
	}
	if request.Read.Resource.Length > request.Read.MaxBytes {
		return ErrResourceSizeLimit
	}
	if r.Reporter == nil || nilInterfaceValue(r.Reporter.estimator) || r.Reporter.profile.validate() != nil {
		return ErrInvalidEstimateReport
	}
	_, err := request.Budget.Resolve()
	return err
}

func validateResourceBody(request ResourceReadRequest, body ResourceBody) error {
	if body.Reference != request.Resource.Reference {
		return ErrResourceMismatch
	}
	actual, err := DescribeResource(body.Reference, request.Resource.Name, body.Artifact)
	if err != nil {
		return err
	}
	if actual.Length > request.MaxBytes {
		return ErrResourceSizeLimit
	}
	if actual != request.Resource {
		return ErrResourceMismatch
	}
	return nil
}

func validateResourceArtifact(artifact ContextArtifact) error {
	if artifact.ID == "" {
		return ErrInvalidResource
	}
	if !slices.Contains(
		[]ArtifactLifecycle{"", ArtifactLifecycleTurnBound, ArtifactLifecyclePersistent, ArtifactLifecycleEphemeral},
		artifact.Lifecycle,
	) {
		return ErrInvalidResource
	}
	if !slices.Contains(
		[]ArtifactPersistencePolicy{ArtifactPersistenceDefault, ArtifactPersistenceStore, ArtifactPersistenceSkip},
		artifact.Persistence,
	) {
		return ErrInvalidResource
	}
	if artifact.Budget != nil && artifact.Budget.TokenLimit < 0 {
		return ErrInvalidResource
	}
	if err := validateArtifactBlob(artifact); err != nil {
		return fmt.Errorf("%w: %w", ErrResourceUnsupported, err)
	}
	switch artifact.Kind {
	case ArtifactKindRetrievalDocument, ArtifactKindMemoryBlock:
		_, err := artifactParts(artifact.Payload)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrResourceUnsupported, err)
		}
		return nil
	default:
		return ErrResourceUnsupported
	}
}

func (r ResourceResolver) project(
	ctx context.Context,
	request ResourceResolveRequest,
	body ResourceBody,
	configuration ResourceConfiguration,
) (ResolvedResource, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedResource{}, err
	}
	artifact, err := r.Projection.ProjectResource(ctx, body.Clone())
	if canceled := ctx.Err(); canceled != nil {
		return ResolvedResource{}, canceled
	}
	if err != nil {
		return ResolvedResource{}, err
	}
	artifact = artifact.Clone()
	if err = validateResourceArtifact(artifact); err != nil {
		return ResolvedResource{}, err
	}
	projectedRef, err := ArtifactContentRef(artifact)
	if err != nil {
		return ResolvedResource{}, err
	}
	projected := artifact.Clone()
	source, err := artifactSourceMessage(body.Artifact)
	if err != nil {
		return ResolvedResource{}, err
	}
	message, err := artifactSourceMessage(artifact)
	if err != nil {
		return ResolvedResource{}, err
	}
	message, decision, err := r.Labels.Project(ctx, []Message{source}, message, configuration.Labels)
	if err != nil {
		return ResolvedResource{}, err
	}
	artifact.Extensions, artifact.SourceRefs = cloneExtensions(message.Extensions), cloneSourceRefs(message.SourceRefs)
	message, err = artifactMessage(ctx, artifact)
	if err != nil {
		return ResolvedResource{}, err
	}
	report, err := r.Reporter.Report(
		ctx,
		EstimateRequest{Segments: []EstimateSegment{{Name: resourceEstimateSegment, Messages: []Message{message}}},
			Budget: request.Budget, ManifestRef: nil, WireRef: nil},
	)
	if err != nil {
		return ResolvedResource{}, err
	}
	if report.Total > report.EffectiveLimit {
		return ResolvedResource{}, ErrBudgetExceeded
	}
	graph, err := resourceLineage(request, configuration, projectedRef, artifact, report, decision)
	if err != nil {
		return ResolvedResource{}, err
	}
	if err = ctx.Err(); err != nil {
		return ResolvedResource{}, err
	}
	return validatedResourceProjection(ctx, ResolvedResource{
		ID:            request.ID,
		Resource:      request.Read.Resource,
		Configuration: configuration.Clone(),
		Source:        body.Clone(),
		Projected:     projected,
		Artifact:      artifact.Clone(),
		Message:       message.Clone(),
		Estimate:      report,
		Lineage:       graph,
	}, ResourceCodec{Messages: r.Reporter.codec, Labels: r.Labels.Registry, Codecs: cloneCodecBindings(r.Codecs)})
}

func resourceLineage(
	request ResourceResolveRequest,
	configuration ResourceConfiguration,
	projectedRef ContentRef,
	artifact ContextArtifact,
	report EstimateReport,
	decision string,
) (Lineage, error) {
	output, err := ArtifactContentRef(artifact)
	if err != nil {
		return Lineage{}, err
	}
	return resourceLineageRefs(request, configuration, projectedRef, output, report, decision)
}

func resourceLineageRefs(request ResourceResolveRequest, configuration ResourceConfiguration,
	projectedRef, output ContentRef, report EstimateReport, decision string,
) (Lineage, error) {
	root, err := ResourceDescriptorRef(request.Read.Resource)
	if err != nil {
		return Lineage{}, err
	}
	source := request.Read.Resource.Content
	source.Occurrence = request.ID + "/read"
	projectedRef.Occurrence = request.ID + "/project"
	output.Occurrence = request.ID + "/labels"
	message := report.Segments[0].Messages[0]
	message.Occurrence = request.ID + "/materialize"
	graph := Lineage{Records: []LineageRecord{
		{
			ID:          source.Occurrence,
			Transform:   configuration.Reader,
			Inputs:      []ContentRef{root},
			Outputs:     []ContentRef{source},
			DecisionRef: "",
			Stage:       "resource-read",
		},
		{
			ID:          projectedRef.Occurrence,
			Transform:   configuration.Projection,
			Inputs:      []ContentRef{source},
			Outputs:     []ContentRef{projectedRef},
			DecisionRef: "",
			Stage:       "resource-project",
		},
		{ID: output.Occurrence, Transform: configuration.Labels, Inputs: []ContentRef{source, projectedRef},
			Outputs: []ContentRef{output}, DecisionRef: decision, Stage: "resource-labels"},
		{
			ID:        message.Occurrence,
			Transform: configuration.Materialization,
			Inputs: []ContentRef{
				output,
			},
			Outputs:     []ContentRef{message},
			DecisionRef: "",
			Stage:       traceStageResourceMaterialize,
		},
	}, Unresolved: nil}
	if err = graph.Validate(); err != nil {
		return Lineage{}, fmt.Errorf("%w: %w", ErrInvalidResource, err)
	}
	return graph, nil
}
