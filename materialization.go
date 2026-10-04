package contexty

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

var (
	ErrMissingArtifactMaterialization = errors.New("contexty: artifact materialization policy required")
	ErrInvalidArtifactMaterialization = errors.New("contexty: invalid artifact materialization")
)

// ArtifactRepresentation is the host's explicit provider role and typed content.
// Identity, source ancestry and extensions remain owned by the artifact.
type ArtifactRepresentation struct {
	Role  Role
	Parts []ContentPart
}

// ArtifactMaterializationPolicy decides representation, never tool authorization.
// Materialize receives an owned artifact and must return an explicit role.
type ArtifactMaterializationPolicy struct {
	Identity    Descriptor
	Materialize func(context.Context, ContextArtifact) (ArtifactRepresentation, error)
}

// ArtifactMaterializationDecision binds the host policy to exact input/output.
type ArtifactMaterializationDecision struct {
	Policy   Descriptor `json:"policy"`
	Artifact ContentRef `json:"artifact"`
	Message  ContentRef `json:"message"`
}

type materializationKey struct{}
type materializationState struct {
	policy    *ArtifactMaterializationPolicy
	codec     JSONSerializer
	messages  map[ContentRef]Message
	decisions map[ContentRef]ArtifactMaterializationDecision
}

func withArtifactMaterialization(
	ctx context.Context,
	policy *ArtifactMaterializationPolicy,
	codec JSONSerializer,
) context.Context {
	if policy != nil {
		copyPolicy := *policy
		policy = &copyPolicy
	}
	return context.WithValue(ctx, materializationKey{}, &materializationState{
		policy: policy, codec: snapshotJSONSerializer(codec), messages: make(map[ContentRef]Message),
		decisions: make(map[ContentRef]ArtifactMaterializationDecision),
	})
}

func validateMaterializationPolicy(policy *ArtifactMaterializationPolicy) error {
	if policy == nil || policy.Materialize == nil {
		return ErrMissingArtifactMaterialization
	}
	if policy.Identity.Validate() != nil {
		return ErrInvalidArtifactMaterialization
	}
	return nil
}

func artifactMessage(ctx context.Context, artifact ContextArtifact) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	state, _ := ctx.Value(materializationKey{}).(*materializationState)
	if state == nil {
		return Message{}, ErrMissingArtifactMaterialization
	}
	if err := validateMaterializationPolicy(state.policy); err != nil {
		return Message{}, err
	}
	ref, err := ArtifactContentRef(artifact)
	if err != nil {
		return Message{}, err
	}
	if cached, found := state.messages[ref]; found {
		return cached.Clone(), nil
	}
	if err = validateArtifactBlob(artifact); err != nil {
		return Message{}, err
	}
	representation, err := state.policy.Materialize(ctx, artifact.Clone())
	if canceled := ctx.Err(); canceled != nil {
		return Message{}, canceled
	}
	if err != nil {
		return Message{}, err
	}
	message, err := materializationMessage(artifact, representation)
	if err != nil {
		return Message{}, err
	}
	output, err := MessageContentRef(message, state.codec)
	if err != nil {
		return Message{}, err
	}
	state.messages[ref] = message.Clone()
	state.decisions[ref] = ArtifactMaterializationDecision{
		Policy:   state.policy.Identity,
		Artifact: ref,
		Message:  output,
	}
	return message.Clone(), nil
}

func materializationMessage(artifact ContextArtifact, representation ArtifactRepresentation) (Message, error) {
	if !outputPolicyKnownRole(representation.Role) || len(representation.Parts) == 0 {
		return Message{}, ErrInvalidArtifactMaterialization
	}
	for _, part := range representation.Parts {
		if nilInterfaceValue(part) {
			return Message{}, ErrInvalidArtifactMaterialization
		}
		if _, call := part.(ToolCallPart); call {
			return Message{}, ErrInvalidArtifactMaterialization
		}
		if _, result := part.(ToolResultPart); result {
			return Message{}, ErrInvalidArtifactMaterialization
		}
		if media, ok := part.(MediaPart); ok {
			if err := media.Validate(); err != nil {
				return Message{}, fmt.Errorf("%w: %w", ErrInvalidArtifactMaterialization, err)
			}
		}
	}
	message := Message{
		ID:          "artifact:" + artifact.ID,
		Role:        representation.Role,
		Parts:       representation.Parts,
		Actor:       nil,
		Annotations: Annotations{Timestamp: nil},
		SourceRefs:  cloneSourceRefs(artifact.SourceRefs),
		Extensions:  cloneExtensions(artifact.Extensions),
		Origin:      nil,
		LLMCache:    nil,
		Provenance:  nil,
	}
	return message.Clone(), nil
}

func materializationDecisions(ctx context.Context) []ArtifactMaterializationDecision {
	state, _ := ctx.Value(materializationKey{}).(*materializationState)
	if state == nil {
		return nil
	}
	var decisions []ArtifactMaterializationDecision
	for _, decision := range state.decisions {
		decisions = append(decisions, decision)
	}
	slices.SortFunc(decisions, func(a, b ArtifactMaterializationDecision) int {
		if order := cmp.Compare(a.Artifact.ID, b.Artifact.ID); order != 0 {
			return order
		}
		return cmp.Compare(a.Artifact.Digest, b.Artifact.Digest)
	})
	return decisions
}

func registerMaterializedArtifact(
	ctx context.Context,
	artifact ContextArtifact,
	message Message,
	policy Descriptor,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, _ := ctx.Value(materializationKey{}).(*materializationState)
	if state == nil || state.policy == nil {
		return ErrMissingArtifactMaterialization
	}
	if state.policy.Identity != policy {
		return ErrInvalidArtifactMaterialization
	}
	if err := validateMaterializedArtifactMessage(artifact, message); err != nil {
		return err
	}
	ref, err := ArtifactContentRef(artifact)
	if err != nil {
		return err
	}
	output, err := MessageContentRef(message, state.codec)
	if err != nil {
		return err
	}
	if cached, found := state.decisions[ref]; found && cached.Message != output {
		return ErrInvalidArtifactMaterialization
	}
	state.messages[ref] = message.Clone()
	state.decisions[ref] = ArtifactMaterializationDecision{Policy: policy, Artifact: ref, Message: output}
	return nil
}

func initializeArtifactMaterializationContext(
	ctx context.Context,
	policy *ArtifactMaterializationPolicy,
	codec JSONSerializer,
) (context.Context, error) {
	if policy != nil {
		if err := validateMaterializationPolicy(policy); err != nil {
			return ctx, err
		}
	}
	return withArtifactMaterialization(ctx, policy, codec), nil
}

func artifactSourceMessage(artifact ContextArtifact) (Message, error) {
	parts, err := artifactParts(artifact.Payload)
	if err != nil {
		return Message{}, err
	}
	return Message{
		ID:          "artifact:" + artifact.ID,
		Role:        "",
		Parts:       parts,
		Actor:       nil,
		Annotations: Annotations{Timestamp: nil},
		SourceRefs:  cloneSourceRefs(artifact.SourceRefs),
		Extensions:  cloneExtensions(artifact.Extensions),
		Origin:      nil,
		LLMCache:    nil,
		Provenance:  nil,
	}, nil
}

func validateMaterializedArtifactMessage(artifact ContextArtifact, message Message) error {
	if message.ID != "artifact:"+artifact.ID || !outputPolicyKnownRole(message.Role) || len(message.Parts) == 0 ||
		message.Actor != nil || message.Origin != nil || message.LLMCache != nil || message.Provenance != nil || message.Annotations.Timestamp != nil ||
		!slices.Equal(
			message.SourceRefs,
			artifact.SourceRefs,
		) || !reflect.DeepEqual(message.Extensions, artifact.Extensions) {
		return ErrInvalidArtifactMaterialization
	}
	_, err := materializationMessage(artifact, ArtifactRepresentation{Role: message.Role, Parts: message.Parts})
	return err
}

func materializationPolicyIdentity(ctx context.Context) (Descriptor, error) {
	state, _ := ctx.Value(materializationKey{}).(*materializationState)
	if state == nil {
		return Descriptor{}, ErrMissingArtifactMaterialization
	}
	if err := validateMaterializationPolicy(state.policy); err != nil {
		return Descriptor{}, err
	}
	return state.policy.Identity, nil
}

// ArtifactContentParts provides the artifact's typed text/media payload without
// assigning a role. Hosts may use it inside an explicit materialization policy.
func ArtifactContentParts(artifact ContextArtifact) ([]ContentPart, error) {
	if err := validateArtifactBlob(artifact); err != nil {
		return nil, err
	}
	return artifactParts(artifact.Payload)
}

// Validate checks exact evidence binding without executing the host policy.
func (d ArtifactMaterializationDecision) Validate() error {
	if d.Policy.Validate() != nil || d.Artifact.Validate() != nil || d.Message.Validate() != nil ||
		d.Artifact.Occurrence != "" || d.Message.Occurrence != "" || d.Message.ID != "artifact:"+d.Artifact.ID {
		return ErrInvalidArtifactMaterialization
	}
	return nil
}
