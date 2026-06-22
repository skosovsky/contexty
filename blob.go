package contexty

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"mime"
	"slices"
	"strings"
)

var (
	ErrInvalidBlob        = errors.New("contexty: invalid blob descriptor or request")
	ErrBlobMissing        = errors.New("contexty: blob missing")
	ErrBlobExpired        = errors.New("contexty: blob expired")
	ErrBlobDenied         = errors.New("contexty: blob scope denied")
	ErrBlobSizeLimit      = errors.New("contexty: blob byte limit exceeded")
	ErrBlobMediaLimit     = errors.New("contexty: blob media type not allowed")
	ErrBlobDigestMismatch = errors.New("contexty: blob digest mismatch")
)

// BlobContent is raw host-owned payload, not an implicit prompt projection.
type BlobContent struct {
	MIMEType string `json:"mime_type"`
	Bytes    []byte `json:"bytes"`
}

func (c BlobContent) clone() BlobContent {
	return BlobContent{MIMEType: c.MIMEType, Bytes: slices.Clone(c.Bytes)}
}

// BlobDescriptor identifies immutable storage content. Refs never confer access.
type BlobDescriptor struct {
	Object       Descriptor   `json:"object"`
	Digest       string       `json:"digest"`
	Length       int64        `json:"length"`
	MIMEType     string       `json:"mime_type"`
	ScopeRef     string       `json:"scope_ref"`
	RetentionRef string       `json:"retention_ref"`
	Sources      []ContentRef `json:"sources"`
}

func (d BlobDescriptor) Clone() BlobDescriptor {
	d.Sources = slices.Clone(d.Sources)
	return d
}

func (d BlobDescriptor) Validate() error {
	if d.Object.Validate() != nil || d.Length < 0 || d.ScopeRef == "" || d.RetentionRef == "" ||
		!validBlobMIME(d.MIMEType) {
		return ErrInvalidBlob
	}
	if (ContentRef{ID: d.Object.ID, Digest: d.Digest, Occurrence: ""}).Validate() != nil {
		return ErrInvalidBlob
	}
	for _, source := range d.Sources {
		if source.Validate() != nil {
			return ErrInvalidBlob
		}
	}
	return nil
}

func validBlobMIME(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	major, minor, complete := strings.Cut(mediaType, "/")
	return err == nil && complete && major != "" && minor != "" && major != "*" && minor != "*"
}

func blobDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

type BlobPutRequest struct {
	ScopeRef     string
	RetentionRef string
	Content      BlobContent
	Sources      []ContentRef
}

func (r BlobPutRequest) clone() BlobPutRequest {
	r.Content = r.Content.clone()
	r.Sources = slices.Clone(r.Sources)
	return r
}

type BlobGetRequest struct {
	ScopeRef string
	Object   Descriptor
	MaxBytes int64
}

// BlobStore owns backend, authorization and streaming byte-bound enforcement.
type BlobStore interface {
	Put(context.Context, BlobPutRequest) (BlobDescriptor, error)
	Get(context.Context, BlobGetRequest) (BlobContent, error)
}

// BlobCleanupIntent is an uncommitted write to reconcile under host claims.
// It is not a command to delete an object or bypass retention.
type BlobCleanupIntent struct {
	Object       Descriptor `json:"object"`
	ScopeRef     string     `json:"scope_ref"`
	RetentionRef string     `json:"retention_ref"`
}

type BlobPutOutcome struct {
	Published *BlobDescriptor
	Cleanup   *BlobCleanupIntent
}

// PutBlob checks the storage receipt before exposing an immutable reference.
func PutBlob(ctx context.Context, storage BlobStore, request BlobPutRequest) (BlobPutOutcome, error) {
	if err := ctx.Err(); err != nil {
		return BlobPutOutcome{}, err
	}
	if nilInterfaceValue(storage) || request.ScopeRef == "" || request.RetentionRef == "" ||
		!validBlobMIME(request.Content.MIMEType) {
		return BlobPutOutcome{}, ErrInvalidBlob
	}
	request = request.clone()
	expected := BlobDescriptor{
		Object: Descriptor{ID: "pending", Revision: "pending"},
		Digest: blobDigest(
			request.Content.Bytes,
		),
		Length:       int64(len(request.Content.Bytes)),
		MIMEType:     request.Content.MIMEType,
		ScopeRef:     request.ScopeRef,
		RetentionRef: request.RetentionRef,
		Sources:      slices.Clone(request.Sources),
	}
	if err := expected.Validate(); err != nil {
		return BlobPutOutcome{}, err
	}
	receipt, err := storage.Put(ctx, request.clone())
	outcome := blobCleanupOutcome(receipt, request)
	if canceled := ctx.Err(); canceled != nil {
		return outcome, canceled
	}
	if err != nil {
		return outcome, err
	}
	expected.Object = receipt.Object
	if receipt.Validate() != nil || !equalBlobReceipt(receipt, expected) {
		return outcome, ErrInvalidBlob
	}
	receipt = receipt.Clone()
	return BlobPutOutcome{Published: &receipt, Cleanup: nil}, nil
}

func equalBlobReceipt(actual, expected BlobDescriptor) bool {
	return actual.Object == expected.Object && actual.Digest == expected.Digest && actual.Length == expected.Length &&
		actual.MIMEType == expected.MIMEType && actual.ScopeRef == expected.ScopeRef &&
		actual.RetentionRef == expected.RetentionRef && slices.Equal(actual.Sources, expected.Sources)
}

func blobCleanupOutcome(receipt BlobDescriptor, request BlobPutRequest) BlobPutOutcome {
	if receipt.Object.Validate() != nil {
		return BlobPutOutcome{}
	}
	return BlobPutOutcome{Published: nil, Cleanup: &BlobCleanupIntent{
		Object: receipt.Object, ScopeRef: request.ScopeRef, RetentionRef: request.RetentionRef,
	}}
}

type BlobLimits struct {
	MaxBytes          int64
	AllowedMediaTypes []string
}

// ResolveBlob reads only by an explicit host scope and verifies immutable bytes.
func ResolveBlob(ctx context.Context, storage BlobStore, scopeRef string, descriptor BlobDescriptor,
	limits BlobLimits,
) (BlobContent, error) {
	if err := ctx.Err(); err != nil {
		return BlobContent{}, err
	}
	descriptor = descriptor.Clone()
	limits.AllowedMediaTypes = slices.Clone(limits.AllowedMediaTypes)
	if nilInterfaceValue(storage) || scopeRef == "" || limits.MaxBytes < 0 || descriptor.Validate() != nil {
		return BlobContent{}, ErrInvalidBlob
	}
	if descriptor.Length > limits.MaxBytes {
		return BlobContent{}, ErrBlobSizeLimit
	}
	if !slices.Contains(limits.AllowedMediaTypes, descriptor.MIMEType) {
		return BlobContent{}, ErrBlobMediaLimit
	}
	content, err := storage.Get(
		ctx,
		BlobGetRequest{ScopeRef: scopeRef, Object: descriptor.Object, MaxBytes: limits.MaxBytes},
	)
	if canceled := ctx.Err(); canceled != nil {
		return BlobContent{}, canceled
	}
	if err != nil {
		return BlobContent{}, err
	}
	if int64(len(content.Bytes)) > limits.MaxBytes || int64(len(content.Bytes)) != descriptor.Length {
		return BlobContent{}, ErrBlobSizeLimit
	}
	if content.MIMEType != descriptor.MIMEType {
		return BlobContent{}, ErrBlobMediaLimit
	}
	if blobDigest(content.Bytes) != descriptor.Digest {
		return BlobContent{}, ErrBlobDigestMismatch
	}
	return content.clone(), nil
}
