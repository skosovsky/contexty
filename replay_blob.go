package contexty

import (
	"context"
	"fmt"
	"slices"
)

// ReplayOption supplies explicit host capabilities, never implicit fetches.
type ReplayOption func(*replayOptions) error

type replayOptions struct {
	scope        string
	availability BlobAvailability
	resources    map[string]ResourceCodec
}

// WithReplayBlobAvailability requires a fresh host-authorized scope. Every blob
// referenced by returned artifacts/tool arguments is checked before any output
// is issued. The port checks metadata only; it must not fetch content or execute
// resolution/compilation. Without this option a blob-bearing replay fails closed.
func WithReplayBlobAvailability(scope string, availability BlobAvailability) ReplayOption {
	return func(options *replayOptions) error {
		if scope == "" || nilInterfaceValue(availability) || options.availability != nil {
			return fmt.Errorf("%w: %w", ErrMissingReplayDependency, ErrInvalidBlob)
		}
		options.scope, options.availability = scope, availability
		return nil
	}
}

func prepareReplayOptions(options []ReplayOption) (replayOptions, error) {
	var result replayOptions
	for _, option := range options {
		if option == nil {
			return replayOptions{}, fmt.Errorf("%w: %w", ErrMissingReplayDependency, ErrInvalidBlob)
		}
		if err := option(&result); err != nil {
			return replayOptions{}, err
		}
	}
	return result, nil
}

func checkReplayBlobs(ctx context.Context, result ReplayResult, options replayOptions) error {
	blobs := replayBlobs(result)
	// Validate all references before invoking any host callback. Deduplicate exact
	// descriptors only: claims on the same object may differ and need authorization.
	unique := make(map[ContentRef]struct{}, len(blobs))
	var required []BlobDescriptor
	for _, blob := range blobs {
		ref, err := BlobDescriptorRef(blob)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrMissingReplayDependency, err)
		}
		if _, exists := unique[ref]; !exists {
			unique[ref] = struct{}{}
			required = append(required, blob.Clone())
		}
	}
	if len(required) != 0 && nilInterfaceValue(options.availability) {
		return ErrMissingReplayDependency
	}
	for _, blob := range required {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := options.availability.CheckBlob(ctx, BlobAvailabilityRequest{ScopeRef: options.scope, Blob: blob.Clone()})
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrMissingReplayDependency, err)
		}
	}
	return ctx.Err()
}

func replayBlobs(result ReplayResult) []BlobDescriptor {
	var blobs []BlobDescriptor
	for _, output := range result.Outputs {
		keys := make([]string, 0, len(output.Segments))
		for key := range output.Segments {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			for _, message := range output.Segments[key] {
				blobs = append(blobs, messageBlobs(message)...)
			}
		}
		if output.Rendered != nil {
			blobs = append(blobs, messageBlobs(*output.Rendered)...)
		}
	}
	artifacts := cloneArtifacts(result.Artifacts)
	for _, output := range result.Outputs {
		artifacts = append(artifacts, cloneArtifacts(output.Artifacts)...)
	}
	for _, artifact := range artifacts {
		if artifact.Blob != nil {
			blobs = append(blobs, artifact.Blob.Object.Clone())
		}
	}
	return blobs
}

func messageBlobs(message Message) []BlobDescriptor {
	var blobs []BlobDescriptor
	for _, part := range message.Parts {
		switch call := part.(type) {
		case ToolCallPart:
			if call.ArgumentsBlob != nil {
				blobs = append(blobs, call.ArgumentsBlob.Clone())
			}
		case *ToolCallPart:
			if call != nil && call.ArgumentsBlob != nil {
				blobs = append(blobs, call.ArgumentsBlob.Clone())
			}
		}
	}
	return blobs
}
