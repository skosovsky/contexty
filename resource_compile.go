package contexty

import (
	"context"
	"slices"
)

type resourceCompileKey struct{}

// Records remain operation-local until host privacy capture approves bytes.
type resourceCompileState struct {
	turnID    string
	resources []ResolvedResource
	active    []ContextArtifact
	ids       map[string]bool
	removed   map[string]bool
	appends   map[string]resourceAppendProjection
}

func startResourceCompile(ctx context.Context, request CompileRequest) context.Context {
	state := &resourceCompileState{
		turnID:    request.TurnID,
		resources: nil,
		active:    nil,
		ids:       make(map[string]bool),
		removed:   make(map[string]bool),
		appends:   make(map[string]resourceAppendProjection),
	}
	for _, artifact := range request.Artifacts {
		state.ids[artifact.ID] = true
	}
	return context.WithValue(ctx, resourceCompileKey{}, state)
}

func resourceStateFrom(ctx context.Context) *resourceCompileState {
	state, _ := ctx.Value(resourceCompileKey{}).(*resourceCompileState)
	return state
}

func (e *Engine) admitDeferredResource(ctx context.Context, resource ResolvedResource) (bool, error) {
	state := resourceStateFrom(ctx)
	if state == nil || (state.ids[resource.Artifact.ID] && !resourceSupportsSameID(resource.Artifact)) {
		return false, ErrInvalidResource
	}
	active, err := e.selectArtifacts(ctx, state.turnID, []ContextArtifact{resource.Artifact})
	if err != nil {
		return false, err
	}
	state.ids[resource.Artifact.ID] = true
	state.resources = append(state.resources, resource)
	if len(active) == 0 || resourceRetainsExisting(state.active, active[0]) {
		return false, ctx.Err()
	}
	if active[0].MergePolicy == PolicyAppend && active[0].Lifecycle != ArtifactLifecycleEphemeral {
		for index, previous := range state.active {
			if previous.ID == active[0].ID {
				return e.admitResourceAppend(ctx, state, resource, index)
			}
		}
	}
	if err = commitResourceArtifact(state, active[0]); err != nil {
		return false, err
	}
	return true, ctx.Err()
}

func commitResourceArtifact(state *resourceCompileState, artifact ContextArtifact) error {
	before := cloneArtifacts(state.active)
	state.active = upsertArtifact(state.active, artifact)
	for _, previous := range before {
		unchanged, err := artifactRevisionPresent(state.active, previous)
		if err != nil {
			return err
		}
		if !unchanged || previous.ID == artifact.ID {
			state.removed["artifact:"+previous.ID] = true
		}
	}
	return nil
}

func resourceSupportsSameID(artifact ContextArtifact) bool {
	return artifact.Lifecycle == ArtifactLifecycleEphemeral || artifact.MergePolicy == "" ||
		artifact.MergePolicy == PolicyReplaceByOrigin || artifact.MergePolicy == PolicyDeduplicateByLayer || artifact.MergePolicy == PolicyAppend
}

func resourceRetainsExisting(artifacts []ContextArtifact, incoming ContextArtifact) bool {
	if incoming.Lifecycle == ArtifactLifecycleEphemeral || incoming.MergePolicy != PolicyDeduplicateByLayer {
		return false
	}
	for _, existing := range artifacts {
		if existing.ID == incoming.ID && artifactsShareSourceLayer(existing, incoming) {
			return true
		}
	}
	return false
}

func artifactRevisionPresent(artifacts []ContextArtifact, expected ContextArtifact) (bool, error) {
	ref, err := ArtifactContentRef(expected)
	if err != nil {
		return false, err
	}
	for _, artifact := range artifacts {
		if artifact.ID != expected.ID {
			continue
		}
		actual, err := ArtifactContentRef(artifact)
		return ref == actual, err
	}
	return false, nil
}

func setResourceActiveArtifacts(ctx context.Context, artifacts []ContextArtifact) {
	if state := resourceStateFrom(ctx); state != nil {
		state.active = cloneArtifacts(artifacts)
	}
}

func removeReplacedResourceContent(ctx context.Context, messages []Message) []Message {
	if state := resourceStateFrom(ctx); state != nil {
		seen := make(map[string]bool)
		var kept []Message
		for _, message := range slices.Backward(messages) {
			if !state.removed[message.ID] {
				kept = append(kept, message)
				continue
			}
			active := slices.ContainsFunc(
				state.active,
				func(artifact ContextArtifact) bool { return message.ID == "artifact:"+artifact.ID },
			)
			if active && !seen[message.ID] {
				kept = append(kept, message)
				seen[message.ID] = true
			}
		}
		slices.Reverse(kept)
		return kept
	}
	return messages
}

func removeReplacedResourceMessages(ctx context.Context, snapshot ConversationSnapshot) (ConversationSnapshot, error) {
	state := resourceStateFrom(ctx)
	if state == nil || len(state.removed) == 0 {
		return snapshot, nil
	}
	for _, segment := range []SegmentName{SegmentSystem, SegmentHistory, SegmentTools, SegmentMemory} {
		before := snapshot.Segment(segment)
		after := slices.DeleteFunc(
			cloneMessageSlice(before),
			func(message Message) bool { return state.removed[message.ID] },
		)
		if len(before) == len(after) {
			continue
		}
		projected, err := traceStage(ctx, "merge", before, after, false)
		if err != nil {
			return ConversationSnapshot{}, err
		}
		recordMergeRemovalsCtx(ctx, before, projected)
		snapshot = snapshot.WithSegment(segment, projected)
	}
	clear(state.removed)
	return snapshot, ctx.Err()
}

func resolvedResourceArtifacts(ctx context.Context) []ContextArtifact {
	state := resourceStateFrom(ctx)
	var artifacts []ContextArtifact
	if state != nil {
		for _, resource := range state.resources {
			artifacts = append(artifacts, resource.Artifact.Clone())
		}
	}
	return artifacts
}

func activeResourceArtifacts(ctx context.Context) []ContextArtifact {
	if state := resourceStateFrom(ctx); state != nil {
		return cloneArtifacts(state.active)
	}
	return nil
}

func compileArtifactInputs(ctx context.Context, source []ContextArtifact) []ContextArtifact {
	return append(cloneArtifacts(source), resolvedResourceArtifacts(ctx)...)
}

func appendResourceArtifactInputs(ctx context.Context, inputs []ManifestSegment) ([]ManifestSegment, error) {
	refs, err := manifestArtifactRefs(resolvedResourceArtifacts(ctx))
	if err != nil {
		return nil, err
	}
	for index := range inputs {
		if inputs[index].Name == manifestArtifactsSegment {
			inputs[index].Messages = uniqueContentRefs(append(inputs[index].Messages, refs...))
			return inputs, nil
		}
	}
	return nil, ErrInvalidManifest
}
