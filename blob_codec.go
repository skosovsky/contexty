package contexty

import (
	"encoding/json"
	"fmt"
)

// EncodeBlobDescriptor stores metadata only, without bytes or backend handles.
func EncodeBlobDescriptor(descriptor BlobDescriptor) ([]byte, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(descriptor.Clone())
}

// DecodeBlobDescriptor validates a stored ref without fetching or authorizing it.
func DecodeBlobDescriptor(wire []byte) (BlobDescriptor, error) {
	var descriptor BlobDescriptor
	if err := json.Unmarshal(wire, &descriptor); err != nil {
		return BlobDescriptor{}, fmt.Errorf("%w: %w", ErrInvalidBlob, err)
	}
	if err := descriptor.Validate(); err != nil {
		return BlobDescriptor{}, err
	}
	return descriptor.Clone(), nil
}
