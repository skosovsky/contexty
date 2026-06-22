package contexty

import (
	"context"
	"fmt"
)

const traceStagePromptTemplate = "prompt-template"
const traceStageArtifact = "artifact"

func tracePromptTemplate(ctx context.Context, raw, prompt Message) (Message, error) {
	trace := traceFromContext(ctx)
	if trace == nil {
		return prompt, nil
	}
	input, err := trace.inputRef(raw)
	if err != nil {
		return Message{}, err
	}
	ref, err := MessageContentRef(prompt, trace.profile.Codec)
	if err != nil {
		return Message{}, err
	}
	if err := trace.profile.Labels.validateLabels(ctx, prompt.Extensions, true); err != nil {
		return Message{}, err
	}
	if err := trace.materialize(ctx, traceStagePromptTemplate, input, ref, prompt); err != nil {
		return Message{}, err
	}
	return prompt.Clone(), nil
}

func traceArtifactSources(ctx context.Context, artifacts []ContextArtifact) error {
	trace := traceFromContext(ctx)
	if trace == nil {
		return nil
	}
	for _, artifact := range artifacts {
		input, err := ArtifactContentRef(artifact)
		if err != nil {
			return err
		}
		if known, found := trace.latest[input]; found {
			input = known
		} else if trace.profile.RequireOrigins {
			return fmt.Errorf("%w: artifact %s", ErrMissingLineage, artifact.ID)
		} else {
			trace.graph.Unresolved = uniqueContentRefs(append(trace.graph.Unresolved, input))
		}
		message, err := artifactMessage(artifact)
		if err != nil {
			return err
		}
		output, err := MessageContentRef(message, trace.profile.Codec)
		if err != nil {
			return err
		}
		if err := trace.materialize(ctx, traceStageArtifact, input, output, message); err != nil {
			return err
		}
	}
	return nil
}

// Materialization records caller-owned typed input, not an authorization decision.
// Actual label projection follows this step and must still enforce host policy.
func (t *compileTrace) materialize(ctx context.Context, stage string, input, output ContentRef, message Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.profile.Labels.validateLabels(ctx, message.Extensions, true); err != nil {
		return err
	}
	descriptor, declared := t.profile.Stages[stage]
	if !declared {
		return fmt.Errorf("%w: stage %s", ErrInvalidDescriptor, stage)
	}
	if err := descriptor.Validate(); err != nil {
		return err
	}
	invocation := t.nextID(stage)
	output.Occurrence = invocation
	if err := t.appendRecord(LineageRecord{ID: invocation, Transform: descriptor, Inputs: []ContentRef{input},
		Outputs: []ContentRef{output}, DecisionRef: "", Stage: stage}); err != nil {
		return err
	}
	if err := captureTraceMessage(ctx, message, stage); err != nil {
		return err
	}
	t.latest[baseContentRef(output)] = output
	return nil
}
