package contexty

import (
	"context"
	"maps"
)

type transformRecorderKey struct{}

type transformRecorder struct {
	records map[string]TransformRecord
}

func withTransformRecorder(ctx context.Context, rec *transformRecorder) context.Context {
	if rec == nil {
		return ctx
	}
	return context.WithValue(ctx, transformRecorderKey{}, rec)
}

func transformRecorderFrom(ctx context.Context) *transformRecorder {
	rec, _ := ctx.Value(transformRecorderKey{}).(*transformRecorder)
	return rec
}

func newTransformRecorder(msgs []Message) *transformRecorder {
	rec := &transformRecorder{records: make(map[string]TransformRecord, len(msgs))}
	for _, m := range msgs {
		if m.ID == "" {
			continue
		}
		rec.records[m.ID] = TransformRecord{Action: ActionPassed, Reason: ""}
	}
	return rec
}

func (r *transformRecorder) snapshot() map[string]TransformRecord {
	if r == nil {
		return nil
	}
	out := make(map[string]TransformRecord, len(r.records))
	maps.Copy(out, r.records)
	return out
}

func (r *transformRecorder) set(id string, action TransformAction, reason string) {
	if r == nil || id == "" {
		return
	}
	r.records[id] = TransformRecord{Action: action, Reason: reason}
}

func (r *transformRecorder) markProtectedPending(ids []string) {
	for _, id := range ids {
		if id == "" {
			continue
		}
		rec, ok := r.records[id]
		if !ok {
			r.set(id, ActionPassed, ReasonProtectedPending)
			continue
		}
		switch rec.Action {
		case ActionEvicted, ActionTruncated, ActionFormatted:
			// preserve budget/hook/formatter outcomes (e.g. history copy evicted before pending merge)
		case ActionPassed:
			r.set(id, ActionPassed, ReasonProtectedPending)
		}
	}
}

func (r *transformRecorder) registerDeferredMessageIDs(before, after ConversationSnapshot) {
	beforeIDs := messageIDSet(snapshotAllMessages(before))
	for _, m := range snapshotAllMessages(after) {
		if m.ID == "" {
			continue
		}
		if _, existed := beforeIDs[m.ID]; existed {
			continue
		}
		if _, ok := r.records[m.ID]; !ok {
			r.records[m.ID] = TransformRecord{Action: ActionPassed, Reason: ""}
		}
	}
}

func snapshotAllMessages(snap ConversationSnapshot) []Message {
	var out []Message
	for _, seg := range snapshotSegmentOrder() {
		out = append(out, snap.Segment(seg)...)
	}
	return out
}

func recordFormatterTransformCtx(ctx context.Context, before, after []Message) {
	recordContentTransformCtx(ctx, before, after, ReasonSegmentFormatter, ReasonReplacedByFormatter)
}

func recordHookTransformCtx(ctx context.Context, before, after []Message) {
	recordContentTransformCtx(ctx, before, after, ReasonTransformHook, ReasonReplacedByHook)
}

func recordSnapshotHookTransforms(ctx context.Context, before, after ConversationSnapshot) {
	seen := make(map[SegmentName]struct{})
	for _, name := range before.SegmentNames() {
		seen[name] = struct{}{}
		recordHookTransformCtx(ctx, before.Segment(name), after.Segment(name))
	}
	for _, name := range after.SegmentNames() {
		if _, ok := seen[name]; ok {
			continue
		}
		recordHookTransformCtx(ctx, nil, after.Segment(name))
	}
}

func recordContentTransformCtx(
	ctx context.Context,
	before, after []Message,
	sameIDReason, replacedReason string,
) {
	rec := transformRecorderFrom(ctx)
	if rec == nil {
		return
	}
	beforeSet := messageIDSet(before)
	afterSet := messageIDSet(after)
	for id := range beforeSet {
		if _, ok := afterSet[id]; !ok {
			rec.set(id, ActionFormatted, replacedReason)
			continue
		}
		bm := findMessageByID(before, id)
		am := findMessageByID(after, id)
		if !MessageEqual(bm, am) {
			rec.set(id, ActionFormatted, sameIDReason)
		}
	}
	for id := range afterSet {
		if _, ok := beforeSet[id]; !ok {
			rec.set(id, ActionPassed, "")
		}
	}
}

func recordEvictionsCtx(ctx context.Context, before, after []Message, reason EvictionReason) {
	rec := transformRecorderFrom(ctx)
	if rec == nil {
		return
	}
	action := ActionEvicted
	trReason := ReasonTokenBudgetExceeded
	switch reason {
	case EvictionReasonTruncate:
		action = ActionTruncated
	case EvictionReasonBudget, EvictionReasonOrphanRepair:
		action = ActionEvicted
	}
	beforeSet := messageIDSet(before)
	afterSet := messageIDSet(after)
	for id := range beforeSet {
		if _, ok := afterSet[id]; !ok {
			rec.set(id, action, trReason)
		}
	}
}

func recordSummarizeReplaceCtx(ctx context.Context, before []Message, summary Message) {
	rec := transformRecorderFrom(ctx)
	if rec == nil {
		return
	}
	for _, m := range before {
		if m.ID != "" {
			rec.set(m.ID, ActionTruncated, ReasonTokenBudgetExceeded)
		}
	}
	if summary.ID != "" {
		rec.set(summary.ID, ActionPassed, "")
	}
}

func messageIDSet(msgs []Message) map[string]struct{} {
	set := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			set[m.ID] = struct{}{}
		}
	}
	return set
}

func findMessageByID(msgs []Message, id string) Message {
	for _, m := range msgs {
		if m.ID == id {
			return m
		}
	}
	return Message{}
}
