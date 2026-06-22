package contexty

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Clone owns every result container and never invokes runtime ports or codecs.
func (r ResolvedResource) Clone() (ResolvedResource, error) {
	estimate, err := r.Estimate.Clone()
	if err != nil {
		return ResolvedResource{}, err
	}
	return ResolvedResource{
		ID:            r.ID,
		Resource:      r.Resource,
		Configuration: r.Configuration.Clone(),
		Source:        r.Source.Clone(),
		Projected:     r.Projected.Clone(),
		Artifact:      r.Artifact.Clone(),
		Message:       r.Message.Clone(),
		Estimate:      estimate,
		Lineage:       r.Lineage.Clone(),
	}, nil
}

type resolvedResourceWire struct {
	ID              string                `json:"id"`
	Resource        ResourceDescriptor    `json:"resource"`
	Configuration   ResourceConfiguration `json:"configuration"`
	SourceReference Descriptor            `json:"source_reference"`
	Source          json.RawMessage       `json:"source"`
	Projected       json.RawMessage       `json:"projected"`
	Artifact        json.RawMessage       `json:"artifact"`
	Message         json.RawMessage       `json:"message"`
	Estimate        EstimateReport        `json:"estimate"`
	Lineage         Lineage               `json:"lineage"`
	Digest          string                `json:"digest"`
}

func (w resolvedResourceWire) digest() (string, error) {
	w.Digest = ""
	wire, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	return canonicalJSONDigest(wire)
}

// EncodeResolvedResource stores local evidence, including original private body.
// It is NOT an export envelope or an accepted checkpoint. The host must approve
// raw retention explicitly before calling it; compile privacy capture is separate.
// Codecs may run; readers, projectors, label policies and estimators never run.
func EncodeResolvedResource(ctx context.Context, resource ResolvedResource, codec ResourceCodec) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frozen, err := resource.Clone()
	if err != nil {
		return nil, err
	}
	codec = codec.forContext(ctx)
	if err = frozen.Validate(ctx, codec); err != nil {
		return nil, err
	}
	wire, err := encodeResourceValues(ctx, frozen, codec)
	if err != nil {
		return nil, err
	}
	wire.Digest, err = wire.digest()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(wire)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	return encoded, err
}

func encodeResourceValues(
	ctx context.Context,
	resource ResolvedResource,
	codec ResourceCodec,
) (resolvedResourceWire, error) {
	var values []json.RawMessage
	for _, artifact := range []ContextArtifact{resource.Source.Artifact, resource.Projected, resource.Artifact} {
		if err := ctx.Err(); err != nil {
			return resolvedResourceWire{}, err
		}
		wire, err := json.Marshal(artifact)
		if canceled := ctx.Err(); canceled != nil {
			return resolvedResourceWire{}, canceled
		}
		if err != nil {
			return resolvedResourceWire{}, err
		}
		values = append(values, wire)
	}
	message, err := codec.Messages.Marshal(resource.Message)
	if canceled := ctx.Err(); canceled != nil {
		return resolvedResourceWire{}, canceled
	}
	if err != nil {
		return resolvedResourceWire{}, fmt.Errorf("%w: %w", ErrReplayCodec, err)
	}
	return resolvedResourceWire{ID: resource.ID, Resource: resource.Resource, Configuration: resource.Configuration,
		SourceReference: resource.Source.Reference, Source: values[0], Projected: values[1], Artifact: values[2],
		Message: message, Estimate: resource.Estimate, Lineage: resource.Lineage, Digest: ""}, nil
}

// DecodeResolvedResource restores explicit saved bytes with current host codecs.
// Missing/changed required content fails; it never invokes resource resolution.
func DecodeResolvedResource(ctx context.Context, encoded []byte, codec ResourceCodec) (ResolvedResource, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedResource{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire resolvedResourceWire
	if err := decoder.Decode(&wire); err != nil {
		return ResolvedResource{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ResolvedResource{}, ErrInvalidResource
	}
	digest, err := wire.digest()
	if err != nil || digest != wire.Digest {
		return ResolvedResource{}, ErrReplayContentMismatch
	}
	codec = codec.forContext(ctx)
	if err = wire.Configuration.Validate(); err != nil {
		return ResolvedResource{}, err
	}
	if err = codec.validateConfiguration(wire.Configuration); err != nil {
		return ResolvedResource{}, err
	}
	resource, err := decodeResourceValues(ctx, wire, codec)
	if err != nil {
		return ResolvedResource{}, err
	}
	if err = resource.Validate(ctx, codec); err != nil {
		return ResolvedResource{}, err
	}
	return resource, nil
}

func decodeResourceValues(
	ctx context.Context,
	wire resolvedResourceWire,
	codec ResourceCodec,
) (ResolvedResource, error) {
	var artifacts []ContextArtifact
	for _, value := range []json.RawMessage{wire.Source, wire.Projected, wire.Artifact} {
		if err := ctx.Err(); err != nil {
			return ResolvedResource{}, err
		}
		if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ResolvedResource{}, ErrMissingReplayDependency
		}
		artifact, err := UnmarshalArtifactJSON(value, codec.Labels)
		if canceled := ctx.Err(); canceled != nil {
			return ResolvedResource{}, canceled
		}
		if err != nil {
			return ResolvedResource{}, fmt.Errorf("%w: %w", ErrReplayCodec, err)
		}
		artifacts = append(artifacts, artifact)
	}
	var message Message
	if len(wire.Message) == 0 || bytes.Equal(bytes.TrimSpace(wire.Message), []byte("null")) {
		return ResolvedResource{}, ErrMissingReplayDependency
	}
	err := codec.Messages.Unmarshal(wire.Message, &message)
	if canceled := ctx.Err(); canceled != nil {
		return ResolvedResource{}, canceled
	}
	if err != nil {
		return ResolvedResource{}, fmt.Errorf("%w: %w", ErrReplayCodec, err)
	}
	return ResolvedResource{
		ID:            wire.ID,
		Resource:      wire.Resource,
		Configuration: wire.Configuration.Clone(),
		Source: ResourceBody{
			Reference: wire.SourceReference,
			Artifact:  artifacts[0],
		},
		Projected: artifacts[1],
		Artifact:  artifacts[2],
		Message:   message,
		Estimate:  wire.Estimate,
		Lineage:   wire.Lineage.Clone(),
	}, nil
}
