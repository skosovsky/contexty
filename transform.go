package contexty

import (
	"context"
	"fmt"
)

// TransformHook mutates a snapshot copy-on-write; original store state is untouched.
type TransformHook interface {
	Transform(ctx context.Context, snap ConversationSnapshot) (ConversationSnapshot, error)
}

// TransformPipeline runs hooks in order, each receiving the previous output.
func TransformPipeline(
	ctx context.Context,
	snap ConversationSnapshot,
	hooks ...TransformHook,
) (ConversationSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ConversationSnapshot{}, err
	}
	cur := snap
	for i, hook := range hooks {
		if hook == nil {
			continue
		}
		stageCtx := withRecordingComponent(ctx, recordingKey(RecordingHook, "", "", i), "hook")
		if err := ctx.Err(); err != nil {
			return ConversationSnapshot{}, err
		}
		next, err := hook.Transform(stageCtx, cur)
		if canceled := ctx.Err(); canceled != nil {
			return ConversationSnapshot{}, canceled
		}
		if err != nil {
			return ConversationSnapshot{}, fmt.Errorf("contexty: transform hook %d: %w", i, err)
		}
		if _, compiling := compileIdentityFromContext(ctx); compiling {
			next, err = normalizeSnapshotMessageIDs(ctx, next)
			if err != nil {
				return ConversationSnapshot{}, err
			}
		}
		if traceFromContext(ctx) != nil {
			next, err = traceSnapshot(stageCtx, "hook", cur, next)
			if err != nil {
				return ConversationSnapshot{}, err
			}
		}
		recordSnapshotHookTransforms(ctx, cur, next)
		cur = next
	}
	return cur, nil
}
