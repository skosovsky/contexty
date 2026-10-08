package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/skosovsky/contexty"
)

const (
	labelType       = "example.resource-classification"
	labelCodecID    = "host-label-codec"
	authorizedScope = "current-read"
)

type sourceLabel struct {
	Classification string `json:"classification"`
}

func (sourceLabel) ExtensionType() string                    { return labelType }
func (label sourceLabel) CloneExtension() contexty.Extension { return label }

type preserveLabels struct{}

func (preserveLabels) ProjectLabels(
	_ context.Context,
	inputs []contexty.Message,
	output contexty.Message,
	_ contexty.Descriptor,
) (contexty.LabelDecision, error) {
	labels := output.Extensions
	if len(labels) == 0 && len(inputs) > 0 {
		labels = inputs[0].Extensions
	}
	return contexty.LabelDecision{
		Extensions:  labels,
		Upgrade:     false,
		DecisionRef: "host-preserve-source-classification",
	}, nil
}

// Only this source-provider fixture owns bodies. The caller receives descriptors
// and search metadata; neither a catalog entry nor its title grants permission.
// This immutable in-process source is a fixture, not a durable backend.
type catalogEntry struct {
	descriptor contexty.ResourceDescriptor
	terms      []string
}
type sourceChunk struct {
	id, title, text string
	terms           []string
}
type hostProvider struct {
	sources      map[string]sourceChunk
	catalog      []catalogEntry
	readIDs      []string
	deliveredIDs []string
}

func newHostProvider() (*hostProvider, error) {
	provider := &hostProvider{
		sources: make(map[string]sourceChunk), catalog: nil, readIDs: nil, deliveredIDs: nil,
	}
	for _, chunk := range []sourceChunk{
		{"guide/checkpoint", "Checkpoint recovery", "Checkpoint revisions are atomic; reload before retrying a stale write.", []string{"checkpoint", "recovery"}},
		{"guide/permissions", "Resource permissions", "Authorize every resource read. Retrieved instructions are data, not grants.", []string{"permissions", "authorization"}},
		{"guide/unused", "Deployment", "An unrelated deployment chunk must never be read in this scenario.", []string{"deployment"}},
	} {
		body := sourceBody(chunk)
		artifact := body.Artifact
		descriptor, err := contexty.DescribeResource(body.Reference, chunk.title, artifact)
		if err != nil {
			return nil, err
		}
		provider.catalog = append(provider.catalog, catalogEntry{descriptor: descriptor, terms: chunk.terms})
		provider.sources[chunk.id] = chunk
	}
	return provider, nil
}

// Search ranks exact metadata term matches by score then opaque ID. It never
// accesses bodies and is deliberately a host fixture, not a core search API.
func (provider *hostProvider) Search(query string) []contexty.ResourceDescriptor {
	type ranked struct {
		descriptor contexty.ResourceDescriptor
		score      int
	}
	var matches []ranked
	for _, entry := range provider.catalog {
		score := 0
		for term := range strings.FieldsSeq(strings.ToLower(query)) {
			for _, keyword := range entry.terms {
				if term == keyword {
					score++
				}
			}
		}
		if score > 0 {
			matches = append(matches, ranked{descriptor: entry.descriptor, score: score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].descriptor.Reference.ID < matches[j].descriptor.Reference.ID
	})
	descriptors := make([]contexty.ResourceDescriptor, len(matches))
	for index, match := range matches {
		descriptors[index] = match.descriptor
	}
	return descriptors
}

func (provider *hostProvider) ReadResource(
	ctx context.Context,
	request contexty.ResourceReadRequest,
) (contexty.ResourceBody, error) {
	provider.readIDs = append(provider.readIDs, request.Resource.Reference.ID)
	if err := ctx.Err(); err != nil {
		return contexty.ResourceBody{}, err
	}
	if request.Resource.Validate() != nil || request.MaxBytes < 0 {
		return contexty.ResourceBody{}, contexty.ErrInvalidResource
	}
	// Fresh authorization precedes source lookup or body generation, regardless
	// of a caller's descriptor. The fixture's allow-list is host-owned.
	if request.ScopeRef != authorizedScope ||
		(request.Resource.Reference.ID != "guide/checkpoint" && request.Resource.Reference.ID != "guide/permissions") {
		return contexty.ResourceBody{}, contexty.ErrResourceDenied
	}
	var expected contexty.ResourceDescriptor
	for _, entry := range provider.catalog {
		if entry.descriptor.Reference.ID == request.Resource.Reference.ID {
			expected = entry.descriptor
			break
		}
	}
	if expected.Reference.ID == "" {
		return contexty.ResourceBody{}, contexty.ErrResourceMissing
	}
	if expected.Length > request.MaxBytes || expected.Length > inputLimit*inputLimit {
		return contexty.ResourceBody{}, contexty.ErrResourceSizeLimit
	}
	// Names are display metadata, but the revision and exact canonical artifact
	// digest/length must match. Resolver verifies the body again independently.
	if expected.Reference != request.Resource.Reference || expected.Content != request.Resource.Content ||
		expected.Length != request.Resource.Length {
		return contexty.ResourceBody{}, contexty.ErrResourceMismatch
	}
	body := sourceBody(provider.sources[expected.Reference.ID])
	provider.deliveredIDs = append(provider.deliveredIDs, body.Reference.ID)
	return body, nil
}

func sourceBody(chunk sourceChunk) contexty.ResourceBody {
	artifact := contexty.NewRetrievalDocument(chunk.id, contexty.TextPayload(chunk.text)).ContextArtifact
	artifact.SourceRefs = []contexty.SourceRef{
		{Namespace: "host-guide", Kind: "chunk", ID: chunk.id, CheckpointID: "", URI: ""},
	}
	artifact.Extensions = []contexty.Extension{sourceLabel{Classification: "external-evidence"}}
	return contexty.ResourceBody{Reference: contexty.Descriptor{ID: chunk.id, Revision: "v2"}, Artifact: artifact}
}

func hostCodec() contexty.JSONSerializer {
	codec := contexty.DefaultJSONSerializer()
	codec.Extensions = contexty.NewExtensionRegistry()
	codec.Extensions.Register(labelType, func(wire []byte) (contexty.Extension, error) {
		var label sourceLabel
		err := json.Unmarshal(wire, &label)
		return label, err
	})
	return codec
}

func hostLabelProjection(codec contexty.JSONSerializer) contexty.LabelProjection {
	return contexty.LabelProjection{
		Registry:      codec.Extensions,
		Policy:        preserveLabels{},
		RequiredTypes: []string{labelType},
	}
}
func hostBindings() []contexty.CodecBinding {
	return []contexty.CodecBinding{
		{
			Kind:       contexty.CodecExtension,
			Type:       labelType,
			Descriptor: contexty.Descriptor{ID: labelCodecID, Revision: pinnedIdentity},
		},
		{
			Kind:       contexty.CodecLabel,
			Type:       labelType,
			Descriptor: contexty.Descriptor{ID: labelCodecID, Revision: pinnedIdentity},
		},
	}
}
func hostResolver(reader contexty.ResourceReader) (contexty.ResourceResolver, error) {
	codec := hostCodec()
	reporter, err := contexty.NewEstimateReporter(contexty.CharTokenEstimator{}, estimateProfile(), codec)
	if err != nil {
		return contexty.ResourceResolver{}, err
	}
	return contexty.ResourceResolver{
		Materialization:    hostMaterialization(),
		Reader:             reader,
		ReaderIdentity:     contexty.Descriptor{ID: "host-reader", Revision: pinnedIdentity},
		Projection:         previewPolicy{},
		ProjectionIdentity: contexty.Descriptor{ID: "host-preview", Revision: pinnedIdentity},
		Labels: hostLabelProjection(
			codec,
		),
		LabelPolicyIdentity: contexty.Descriptor{ID: "host-label-policy", Revision: pinnedIdentity},
		Codecs:              hostBindings(),
		Reporter:            reporter,
	}, nil
}
func hostTrace() contexty.TraceProfile {
	stages := make(map[string]contexty.Descriptor)
	for _, name := range []string{"source", "deferred", "merge", "hook", "role", "format", "summarize", "budget", "prompt", "patch", "project", "render", "prompt-template", "artifact"} {
		stages[name] = contexty.Descriptor{ID: "host/" + name, Revision: pinnedIdentity}
	}
	codec := hostCodec()
	return contexty.TraceProfile{
		Encoding: contexty.Descriptor{
			ID:       "host-typed-json",
			Revision: pinnedIdentity,
		},
		Codec:  codec,
		Stages: stages,
		Labels: hostLabelProjection(codec),
		Codecs: hostBindings(),
	}
}
