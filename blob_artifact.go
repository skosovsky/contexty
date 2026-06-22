package contexty

import (
	"context"
	"encoding/json"
	"slices"
)

// ArtifactBlob binds a prompt preview to the original immutable typed payload.
// It grants no read permission and never implicitly resolves an object.
type ArtifactBlob struct {
	Object        BlobDescriptor       `json:"object"`
	Original      ContentRef           `json:"original"`
	PreviewDigest string               `json:"preview_digest"`
	Policy        Descriptor           `json:"policy"`
	Threshold     *BlobThresholdLimits `json:"threshold,omitempty"`
}

func (b *ArtifactBlob) clone() *ArtifactBlob {
	if b == nil {
		return nil
	}
	copyBlob := *b
	copyBlob.Object = b.Object.Clone()
	if b.Threshold != nil {
		limits := *b.Threshold
		copyBlob.Threshold = &limits
	}
	return &copyBlob
}

func validateArtifactBlob(artifact ContextArtifact) error {
	if artifact.Blob == nil {
		return nil
	}
	blob := artifact.Blob
	if blob.Object.Validate() != nil || blob.Original.Validate() != nil ||
		blob.Original.ID != artifact.ID || blob.Policy.Validate() != nil ||
		blob.Object.MIMEType != mimeApplicationJSON || !slices.Contains(blob.Object.Sources, blob.Original) {
		return ErrInvalidBlob
	}
	if blob.Threshold != nil && (blob.Threshold.MaxInlineBytes < 0 ||
		blob.Threshold.MaxBlobBytes < blob.Threshold.MaxInlineBytes) {
		return ErrInvalidBlobPolicy
	}
	digest, err := artifactPayloadDigest(artifact.Payload)
	if err != nil || digest != blob.PreviewDigest {
		return ErrBlobDigestMismatch
	}
	return nil
}

func validateArtifactBlobs(artifacts []ContextArtifact) error {
	for _, artifact := range artifacts {
		if err := validateArtifactBlob(artifact); err != nil {
			return err
		}
	}
	return nil
}

func artifactPayloadDigest(payload ToolPayload) (string, error) {
	wire, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return canonicalJSONDigest(wire)
}

type BlobArtifactRequest struct {
	ID                       string
	Artifact                 ContextArtifact
	ScopeRef                 string
	RetentionRef             string
	MaxPreviewBytes          int64
	AllowedPreviewMediaTypes []string
	Extensions               *ExtensionRegistry
}

// BlobArtifactOutcome is passed to compile only after successful preparation.
// On a failed write Selection may contain cleanup evidence, never an Artifact.
type BlobArtifactOutcome struct {
	Artifact  *ContextArtifact
	Selection BlobSelection
	Lineage   Lineage
}

// ProjectArtifact runs explicitly before CompileSnapshot/artifact admission.
// It offloads the complete ToolPayload wire contract, not a mutable workspace
// file or an already rendered prompt. Artifact identity and host metadata stay.
func (o BlobOffloader) ProjectArtifact(ctx context.Context, request BlobArtifactRequest) (BlobArtifactOutcome, error) {
	if err := ctx.Err(); err != nil {
		return BlobArtifactOutcome{}, err
	}
	if request.ID == "" || request.Artifact.Blob != nil {
		return BlobArtifactOutcome{}, ErrInvalidBlob
	}
	artifact := request.Artifact.Clone()
	labels := LabelProjection{Policy: nil, Registry: request.Extensions.snapshot(), RequiredTypes: nil}
	if err := labels.validateLabels(ctx, artifact.Extensions, false); err != nil {
		return BlobArtifactOutcome{}, err
	}
	original, err := ArtifactContentRef(artifact)
	if err != nil {
		return BlobArtifactOutcome{}, err
	}
	wire, err := json.Marshal(artifact.Payload)
	if err != nil {
		return BlobArtifactOutcome{}, err
	}
	selection, err := o.Project(ctx, BlobOffloadRequest{
		Put: BlobPutRequest{ScopeRef: request.ScopeRef, RetentionRef: request.RetentionRef,
			Content: BlobContent{MIMEType: mimeApplicationJSON, Bytes: wire}, Sources: []ContentRef{original}},
		MaxPreviewBytes: request.MaxPreviewBytes, AllowedPreviewMediaTypes: request.AllowedPreviewMediaTypes,
	})
	if err != nil {
		var failed BlobArtifactOutcome
		failed.Selection = selection
		return failed, err
	}
	if selection.Disposition == BlobInline {
		return BlobArtifactOutcome{
			Artifact:  &artifact,
			Selection: selection,
			Lineage:   Lineage{Records: nil, Unresolved: nil},
		}, nil
	}
	projected, err := projectedBlobArtifact(ctx, request.ID, artifact, original, selection)
	if err != nil {
		var failed BlobArtifactOutcome
		failed.Selection.Cleanup = &BlobCleanupIntent{Object: selection.Stored.Object,
			ScopeRef: request.ScopeRef, RetentionRef: request.RetentionRef}
		return failed, err
	}
	return projected, nil
}

func projectedBlobArtifact(ctx context.Context, id string, artifact ContextArtifact,
	original ContentRef, selection BlobSelection,
) (BlobArtifactOutcome, error) {
	artifact.Payload = blobPreviewPayload(selection.Preview)
	digest, err := artifactPayloadDigest(artifact.Payload)
	if err != nil {
		return BlobArtifactOutcome{}, err
	}
	artifact.Blob = &ArtifactBlob{Object: selection.Stored.Clone(), Original: original,
		PreviewDigest: digest, Policy: selection.Policy, Threshold: selection.Threshold}
	artifact.Blob = artifact.Blob.clone()
	output, err := ArtifactContentRef(artifact)
	if err != nil {
		return BlobArtifactOutcome{}, err
	}
	output.Occurrence = id
	graph := Lineage{Records: []LineageRecord{{ID: id, Transform: selection.Policy,
		Inputs: []ContentRef{original}, Outputs: []ContentRef{output}, DecisionRef: "", Stage: "artifact-offload"}},
		Unresolved: nil}
	if err := graph.Validate(); err != nil {
		return BlobArtifactOutcome{}, err
	}
	if err := ctx.Err(); err != nil {
		return BlobArtifactOutcome{}, err
	}
	return BlobArtifactOutcome{Artifact: &artifact, Selection: selection, Lineage: graph}, nil
}

func blobPreviewPayload(preview BlobContent) ToolPayload {
	payload := ToolPayload{Text: "", Data: nil, Binary: nil, MIMEType: preview.MIMEType,
		Error: nil, Progress: nil, Control: nil}
	if preview.MIMEType == "text/plain" {
		payload.Text = string(preview.Bytes)
	} else {
		payload.Binary = slices.Clone(preview.Bytes)
	}
	return payload
}
