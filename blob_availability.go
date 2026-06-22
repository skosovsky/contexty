package contexty

import "context"

// BlobAvailability is a host metadata check, not a content fetch. A descriptor
// does not authorize it; callers must provide a fresh explicit scope.
type BlobAvailability interface {
	CheckBlob(context.Context, BlobAvailabilityRequest) error
}

type BlobAvailabilityRequest struct {
	ScopeRef string
	Blob     BlobDescriptor
}
