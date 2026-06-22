// Package memory provides a host-owned, immutable reference resource reader.
// It has no discovery, file parsing, permission model or execution capability.
package memory

import (
	"context"
	"errors"

	"github.com/skosovsky/contexty"
)

var ErrConfiguration = errors.New("memory resource: invalid configuration")

type Config struct {
	MaxBodyBytes int64
	Authorize    func(context.Context, contexty.ResourceReadRequest) error
}

type entry struct {
	body       contexty.ResourceBody
	descriptor contexty.ResourceDescriptor
}

// Reader owns immutable body snapshots. Names do not participate in lookup;
// only the opaque reference ID selects an entry, with exact revision/content
// checks. Authorization executes on every read before exposing body/existence.
type Reader struct {
	config  Config
	entries map[string]entry
}

func New(config Config, bodies ...contexty.ResourceBody) (*Reader, error) {
	if config.MaxBodyBytes < 0 || config.Authorize == nil {
		return nil, ErrConfiguration
	}
	reader := &Reader{config: config, entries: make(map[string]entry, len(bodies))}
	for _, body := range bodies {
		frozen := body.Clone()
		descriptor, err := contexty.DescribeResource(frozen.Reference, "", frozen.Artifact)
		if err != nil {
			return nil, err
		}
		if descriptor.Length > config.MaxBodyBytes {
			return nil, contexty.ErrResourceSizeLimit
		}
		if _, duplicate := reader.entries[frozen.Reference.ID]; duplicate {
			return nil, ErrConfiguration
		}
		reader.entries[frozen.Reference.ID] = entry{body: frozen, descriptor: descriptor}
	}
	return reader, nil
}

func (r *Reader) ReadResource(
	ctx context.Context,
	request contexty.ResourceReadRequest,
) (contexty.ResourceBody, error) {
	if err := ctx.Err(); err != nil {
		return contexty.ResourceBody{}, err
	}
	if r == nil || r.config.Authorize == nil || request.ScopeRef == "" {
		return contexty.ResourceBody{}, contexty.ErrResourceDenied
	}
	if request.Resource.Validate() != nil || request.MaxBytes < 0 {
		return contexty.ResourceBody{}, contexty.ErrInvalidResource
	}
	if request.Resource.Length > request.MaxBytes || request.Resource.Length > r.config.MaxBodyBytes {
		return contexty.ResourceBody{}, contexty.ErrResourceSizeLimit
	}
	err := r.config.Authorize(ctx, request)
	if canceled := ctx.Err(); canceled != nil {
		return contexty.ResourceBody{}, canceled
	}
	if err != nil {
		return contexty.ResourceBody{}, err
	}
	return r.selectedBody(ctx, request)
}

func (r *Reader) selectedBody(
	ctx context.Context,
	request contexty.ResourceReadRequest,
) (contexty.ResourceBody, error) {
	selected, found := r.entries[request.Resource.Reference.ID]
	if !found {
		return contexty.ResourceBody{}, contexty.ErrResourceMissing
	}
	if selected.descriptor.Length > request.MaxBytes || selected.descriptor.Length > r.config.MaxBodyBytes {
		return contexty.ResourceBody{}, contexty.ErrResourceSizeLimit
	}
	if selected.body.Reference != request.Resource.Reference ||
		selected.descriptor.Content != request.Resource.Content ||
		selected.descriptor.Length != request.Resource.Length {
		return contexty.ResourceBody{}, contexty.ErrResourceMismatch
	}
	body := selected.body.Clone()
	if err := ctx.Err(); err != nil {
		return contexty.ResourceBody{}, err
	}
	return body, nil
}

var _ contexty.ResourceReader = (*Reader)(nil)
