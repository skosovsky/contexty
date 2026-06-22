package contexty

import (
	"context"
	"slices"
)

// ResourceArtifactMerge describes a derived projection, not a changed resolution.
type ResourceArtifactMerge struct {
	Inputs   []ContentRef `json:"inputs"`
	Artifact ContentRef   `json:"artifact"`
	Message  ContentRef   `json:"message"`
	Admitted bool         `json:"admitted"`
	Lineage  Lineage      `json:"lineage"`
}

const resourceIntrinsicRevision = "intrinsic"
const resourceAppendMaterializeStage = "resource-append-materialize"

func resourceMaterializationIdentity() Descriptor {
	return Descriptor{ID: "contexty/artifact-materialize", Revision: resourceIntrinsicRevision}
}

type resourceAppendProjection struct {
	artifact ContextArtifact
	message  Message
	record   ResourceArtifactMerge
}

func (e *Engine) projectResourceAppend(ctx context.Context, resource ResolvedResource,
	existing ContextArtifact,
) (resourceAppendProjection, error) {
	inputs, err := resourceAppendInputs(existing, resource.Artifact)
	if err != nil {
		return resourceAppendProjection{}, err
	}
	artifact := appendArtifactPayload(existing, resource.Artifact)
	message, err := artifactMessage(artifact)
	if err != nil {
		return resourceAppendProjection{}, err
	}
	labels := LabelProjection{Policy: nil, Registry: nil, RequiredTypes: nil}
	descriptor := Descriptor{ID: "contexty/artifact-append", Revision: resourceIntrinsicRevision}
	if e.trace != nil {
		labels = e.trace.Labels
		descriptor = e.trace.Stages["merge"]
	}
	message, decision, err := labels.Project(ctx, inputs, message, descriptor)
	if canceled := ctx.Err(); canceled != nil {
		return resourceAppendProjection{}, canceled
	}
	if err != nil {
		return resourceAppendProjection{}, err
	}
	artifact.Extensions = cloneExtensions(message.Extensions)
	artifact.SourceRefs = cloneSourceRefs(message.SourceRefs)
	record, err := resourceAppendLineage(
		ctx,
		resource.ID,
		existing,
		resource.Artifact,
		artifact,
		message,
		descriptor,
		decision,
	)
	if err != nil {
		return resourceAppendProjection{}, err
	}
	return resourceAppendProjection{artifact: artifact, message: message, record: record}, ctx.Err()
}

func resourceAppendInputs(existing, incoming ContextArtifact) ([]Message, error) {
	var messages []Message
	for _, artifact := range []ContextArtifact{existing, incoming} {
		if artifact.Blob != nil {
			return nil, ErrResourceUnsupported
		}
		message, err := artifactMessage(artifact)
		if err != nil {
			return nil, err
		}
		for _, part := range message.Parts {
			if _, text := part.(TextPart); !text {
				return nil, ErrResourceUnsupported
			}
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func resourceAppendLineage(ctx context.Context, id string, existing, incoming, artifact ContextArtifact,
	message Message, descriptor Descriptor, decision string,
) (ResourceArtifactMerge, error) {
	inputs, err := manifestArtifactRefs([]ContextArtifact{existing, incoming})
	if err != nil {
		return ResourceArtifactMerge{}, err
	}
	for index, ref := range inputs {
		if trace := traceFromContext(ctx); trace != nil {
			if known, found := trace.latest[ref]; found {
				inputs[index] = known
			} else if trace.profile.RequireOrigins {
				return ResourceArtifactMerge{}, ErrMissingLineage
			}
		}
	}
	output, err := ArtifactContentRef(artifact)
	if err != nil {
		return ResourceArtifactMerge{}, err
	}
	codec := DefaultJSONSerializer()
	if trace := traceFromContext(ctx); trace != nil {
		codec = trace.profile.Codec
	}
	materialized, err := MessageContentRef(message, codec)
	if err != nil {
		return ResourceArtifactMerge{}, err
	}
	derived, rendered := output, materialized
	derived.Occurrence, rendered.Occurrence = id+"/append", id+"/append-materialize"
	graph := Lineage{Records: []LineageRecord{
		{
			ID:          derived.Occurrence,
			Transform:   descriptor,
			Inputs:      uniqueContentRefs(inputs),
			Outputs:     []ContentRef{derived},
			DecisionRef: decision,
			Stage:       "resource-append",
		},
		{
			ID:        rendered.Occurrence,
			Transform: resourceMaterializationIdentity(),
			Inputs: []ContentRef{
				derived,
			},
			Outputs:     []ContentRef{rendered},
			DecisionRef: "",
			Stage:       resourceAppendMaterializeStage,
		},
	}, Unresolved: nil}
	record := ResourceArtifactMerge{Inputs: slices.Clone(inputs), Artifact: output, Message: materialized,
		Admitted: false, Lineage: graph}
	return record, record.Validate(id, incoming.ID)
}

func (r ResourceArtifactMerge) Validate(id, artifactID string) error {
	if len(r.Inputs) != 2 || len(r.Lineage.Records) != 2 || len(r.Lineage.Unresolved) != 0 ||
		r.Artifact.ID != artifactID || r.Message.ID != "artifact:"+artifactID ||
		r.Artifact.Occurrence != "" || r.Message.Occurrence != "" {
		return ErrInvalidResource
	}
	if err := r.Lineage.Validate(); err != nil {
		return err
	}
	derived, rendered := r.Artifact, r.Message
	derived.Occurrence, rendered.Occurrence = id+"/append", id+"/append-materialize"
	first, second := r.Lineage.Records[0], r.Lineage.Records[1]
	if first.ID != derived.Occurrence || first.Stage != "resource-append" ||
		!slices.Equal(
			first.Inputs,
			uniqueContentRefs(r.Inputs),
		) || !slices.Equal(first.Outputs, []ContentRef{derived}) ||
		second.ID != rendered.Occurrence || second.Stage != resourceAppendMaterializeStage || second.DecisionRef != "" ||
		second.Transform != resourceMaterializationIdentity() ||
		!slices.Equal(second.Inputs, []ContentRef{derived}) || !slices.Equal(second.Outputs, []ContentRef{rendered}) {
		return ErrInvalidLineage
	}
	for _, input := range r.Inputs {
		if input.ID != artifactID {
			return ErrInvalidResource
		}
	}
	return nil
}
