package contexty

import (
	"context"
	"errors"
	"slices"
	"unicode/utf8"
)

var (
	ErrInvalidBlobPolicy = errors.New("contexty: invalid blob offload policy")
	ErrBlobRejected      = errors.New("contexty: blob rejected by host policy")
)

type BlobDisposition string

const (
	BlobInline  BlobDisposition = "inline"
	BlobOffload BlobDisposition = "offload"
	BlobReject  BlobDisposition = "reject"
)

type BlobOffloadCandidate struct {
	Content BlobContent
	Sources []ContentRef
}

type BlobOffloadDecision struct {
	Disposition BlobDisposition
	Preview     BlobContent
}

type BlobOffloadPolicy interface {
	SelectBlob(context.Context, BlobOffloadCandidate) (BlobOffloadDecision, error)
}

type BlobOffloadRequest struct {
	Put                      BlobPutRequest
	MaxPreviewBytes          int64
	AllowedPreviewMediaTypes []string
}

// BlobSelection is pre-budget storage evidence, not a checkpoint commit.
type BlobSelection struct {
	Disposition BlobDisposition      `json:"disposition"`
	Policy      Descriptor           `json:"policy"`
	Threshold   *BlobThresholdLimits `json:"threshold,omitempty"`
	Inline      BlobContent          `json:"inline"`
	Preview     BlobContent          `json:"preview"`
	Stored      *BlobDescriptor      `json:"stored,omitempty"`
	Cleanup     *BlobCleanupIntent   `json:"cleanup,omitempty"`
}

type BlobOffloader struct {
	Policy         BlobOffloadPolicy
	PolicyIdentity Descriptor
	Storage        BlobStore
}

// Project selects storage before consuming prompt budget. Host scope/retention
// refs are passed unchanged to Put; they never authorize an implicit read/delete.
func (o BlobOffloader) Project(ctx context.Context, request BlobOffloadRequest) (BlobSelection, error) {
	if err := ctx.Err(); err != nil {
		return BlobSelection{}, err
	}
	if nilInterfaceValue(o.Policy) || o.PolicyIdentity.Validate() != nil || request.MaxPreviewBytes < 0 ||
		!validBlobMIME(request.Put.Content.MIMEType) {
		return BlobSelection{}, ErrInvalidBlobPolicy
	}
	request.Put = request.Put.clone()
	request.AllowedPreviewMediaTypes = slices.Clone(request.AllowedPreviewMediaTypes)
	for _, source := range request.Put.Sources {
		if source.Validate() != nil {
			return BlobSelection{}, ErrInvalidBlob
		}
	}
	decision, err := o.Policy.SelectBlob(ctx, BlobOffloadCandidate{
		Content: request.Put.Content.clone(), Sources: slices.Clone(request.Put.Sources),
	})
	if canceled := ctx.Err(); canceled != nil {
		return BlobSelection{}, canceled
	}
	if err != nil {
		return BlobSelection{}, err
	}
	return o.applyDecision(ctx, request, decision)
}

func (o BlobOffloader) applyDecision(ctx context.Context, request BlobOffloadRequest,
	decision BlobOffloadDecision,
) (BlobSelection, error) {
	selection := BlobSelection{
		Disposition: decision.Disposition,
		Policy:      o.PolicyIdentity,
		Threshold: blobThresholdConfiguration(
			o.Policy,
		),
		Inline:  BlobContent{MIMEType: "", Bytes: nil},
		Preview: BlobContent{MIMEType: "", Bytes: nil},
		Stored:  nil,
		Cleanup: nil,
	}
	switch decision.Disposition {
	case BlobInline:
		selection.Inline = request.Put.Content.clone()
		return selection, nil
	case BlobReject:
		return BlobSelection{}, ErrBlobRejected
	case BlobOffload:
		if int64(len(decision.Preview.Bytes)) > request.MaxPreviewBytes {
			return BlobSelection{}, ErrBlobSizeLimit
		}
		if !validBlobMIME(decision.Preview.MIMEType) ||
			!slices.Contains(request.AllowedPreviewMediaTypes, decision.Preview.MIMEType) ||
			(decision.Preview.MIMEType == "text/plain" && !utf8.Valid(decision.Preview.Bytes)) {
			return BlobSelection{}, ErrBlobMediaLimit
		}
		selection.Preview = decision.Preview.clone()
		outcome, err := PutBlob(ctx, o.Storage, request.Put)
		if err != nil {
			var failed BlobSelection
			failed.Cleanup = outcome.Cleanup
			return failed, err
		}
		selection.Stored = outcome.Published
		return selection, nil
	default:
		return BlobSelection{}, ErrInvalidBlobPolicy
	}
}
