package contexty

import "context"

type BlobThresholdLimits struct {
	MaxInlineBytes int64 `json:"max_inline_bytes"`
	MaxBlobBytes   int64 `json:"max_blob_bytes"`
}

// BlobPreviewer is a host-owned rendering port; input bytes are copied.
type BlobPreviewer interface {
	PreviewBlob(context.Context, BlobContent) (BlobContent, error)
}

type blobThresholdPolicy struct {
	limits  BlobThresholdLimits
	preview BlobPreviewer
}

// NewBlobThresholdPolicy captures byte thresholds without inspecting a domain.
// Payloads above the inline threshold need an explicit host preview renderer.
func NewBlobThresholdPolicy(limits BlobThresholdLimits, preview BlobPreviewer) (BlobOffloadPolicy, error) {
	if limits.MaxInlineBytes < 0 || limits.MaxBlobBytes < limits.MaxInlineBytes ||
		(limits.MaxBlobBytes > limits.MaxInlineBytes && nilInterfaceValue(preview)) {
		return nil, ErrInvalidBlobPolicy
	}
	return blobThresholdPolicy{limits: limits, preview: preview}, nil
}

func (p blobThresholdPolicy) SelectBlob(
	ctx context.Context,
	candidate BlobOffloadCandidate,
) (BlobOffloadDecision, error) {
	if err := ctx.Err(); err != nil {
		return BlobOffloadDecision{}, err
	}
	size := int64(len(candidate.Content.Bytes))
	if size <= p.limits.MaxInlineBytes {
		return BlobOffloadDecision{Disposition: BlobInline, Preview: BlobContent{MIMEType: "", Bytes: nil}}, nil
	}
	if size > p.limits.MaxBlobBytes {
		return BlobOffloadDecision{Disposition: BlobReject, Preview: BlobContent{MIMEType: "", Bytes: nil}}, nil
	}
	preview, err := p.preview.PreviewBlob(ctx, candidate.Content.clone())
	if canceled := ctx.Err(); canceled != nil {
		return BlobOffloadDecision{}, canceled
	}
	if err != nil {
		return BlobOffloadDecision{}, err
	}
	return BlobOffloadDecision{Disposition: BlobOffload, Preview: preview.clone()}, nil
}

func blobThresholdConfiguration(policy BlobOffloadPolicy) *BlobThresholdLimits {
	if threshold, ok := policy.(blobThresholdPolicy); ok {
		limits := threshold.limits
		return &limits
	}
	return nil
}
