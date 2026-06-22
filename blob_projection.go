package contexty

import "context"

// BlobDecoder is host-owned typed materialization, not an instruction executor.
// Both verified bytes and returned messages are defensively copied.
type BlobDecoder interface {
	DecodeBlob(context.Context, BlobContent) ([]Message, error)
}

type BlobResolveRequest struct {
	ID       string
	ScopeRef string
	Blob     BlobDescriptor
	Limits   BlobLimits
	Budget   BudgetRequest
}

// BlobProjection contains verified materialization and cost/provenance evidence.
type BlobProjection struct {
	Blob     BlobDescriptor
	Decoder  Descriptor
	Messages []Message
	Estimate EstimateReport
	Lineage  Lineage
}

// BlobResolver composes explicit storage, materialization and cost ports.
// It never chooses a model, fetches an implicit dependency or grants access.
type BlobResolver struct {
	Storage         BlobStore
	Decoder         BlobDecoder
	DecoderIdentity Descriptor
	Reporter        *EstimateReporter
}

// BlobDescriptorRef identifies complete storage metadata, separately from bytes.
// Revision/retention/source changes cannot alias the same raw payload digest.
func BlobDescriptorRef(descriptor BlobDescriptor) (ContentRef, error) {
	wire, err := EncodeBlobDescriptor(descriptor)
	if err != nil {
		return ContentRef{}, err
	}
	digest, err := canonicalJSONDigest(wire)
	if err != nil {
		return ContentRef{}, err
	}
	return ContentRef{ID: descriptor.Object.ID, Digest: digest, Occurrence: ""}, nil
}

func (r BlobResolver) Resolve(ctx context.Context, request BlobResolveRequest) (BlobProjection, error) {
	if err := ctx.Err(); err != nil {
		return BlobProjection{}, err
	}
	if err := r.validate(request); err != nil {
		return BlobProjection{}, err
	}
	request.Blob = request.Blob.Clone()
	content, err := ResolveBlob(ctx, r.Storage, request.ScopeRef, request.Blob, request.Limits)
	if err != nil {
		return BlobProjection{}, err
	}
	messages, err := r.Decoder.DecodeBlob(ctx, content.clone())
	if canceled := ctx.Err(); canceled != nil {
		return BlobProjection{}, canceled
	}
	if err != nil {
		return BlobProjection{}, err
	}
	messages = cloneMessageSlice(messages)
	if validationErr := validateUniqueMessageIDs(messages); validationErr != nil {
		return BlobProjection{}, validationErr
	}
	report, err := r.Reporter.Report(ctx, EstimateRequest{Segments: []EstimateSegment{
		{Name: "blob", Messages: messages},
	}, Budget: request.Budget, ManifestRef: nil, WireRef: nil})
	if err != nil {
		return BlobProjection{}, err
	}
	if report.Total > report.EffectiveLimit {
		return BlobProjection{}, ErrBudgetExceeded
	}
	graph, err := blobProjectionLineage(request, r.DecoderIdentity, report)
	if err != nil {
		return BlobProjection{}, err
	}
	if err := ctx.Err(); err != nil {
		return BlobProjection{}, err
	}
	return BlobProjection{Blob: request.Blob.Clone(), Decoder: r.DecoderIdentity,
		Messages: messages, Estimate: report, Lineage: graph}, nil
}

func (r BlobResolver) validate(request BlobResolveRequest) error {
	if request.ID == "" || nilInterfaceValue(r.Storage) || nilInterfaceValue(r.Decoder) ||
		r.Reporter == nil || r.DecoderIdentity.Validate() != nil {
		return ErrInvalidBlob
	}
	if nilInterfaceValue(r.Reporter.estimator) || r.Reporter.profile.validate() != nil {
		return ErrInvalidEstimateReport
	}
	_, err := request.Budget.Resolve()
	return err
}

func blobProjectionLineage(request BlobResolveRequest, decoder Descriptor, report EstimateReport) (Lineage, error) {
	source, err := BlobDescriptorRef(request.Blob)
	if err != nil {
		return Lineage{}, err
	}
	outputs := make([]ContentRef, 0, len(report.Segments[0].Messages))
	for _, output := range report.Segments[0].Messages {
		output.Occurrence = request.ID
		outputs = append(outputs, output)
	}
	graph := Lineage{Records: []LineageRecord{{ID: request.ID, Transform: decoder, Inputs: []ContentRef{source},
		Outputs: outputs, DecisionRef: "", Stage: "blob-decode"}}, Unresolved: nil}
	if err := graph.Validate(); err != nil {
		return Lineage{}, err
	}
	return graph, nil
}
