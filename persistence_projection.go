package contexty

// DerivePersistenceProjection returns messages ready to persist for seg,
// using immutable Source and compile transformations (excludes evicted/truncated;
// in-place formatted/ephemeral changes revert to Source or Introduced originals;
// structural replacements and deferred merge removals use payload adds; Pending excluded from history).
func (r CompileResult) DerivePersistenceProjection(seg SegmentName) []Message {
	sourceMsgs := r.sourceSegment(seg)
	pendingIDs := r.pendingIDSet()
	var out []Message
	for _, m := range sourceMsgs {
		if m.ID == "" {
			continue
		}
		if seg == SegmentHistory {
			if _, pending := pendingIDs[m.ID]; pending {
				continue
			}
		}
		rec, ok := r.Transformations[m.ID]
		if !ok {
			out = append(out, m.Clone())
			continue
		}
		if shouldPersistSourceMessage(rec) {
			out = append(out, m.Clone())
		}
	}
	out = append(out, r.payloadAddsForSegment(seg, sourceMsgs)...)
	if len(out) == 0 {
		return nil
	}
	return out
}

func shouldPersistSourceMessage(rec TransformRecord) bool {
	switch rec.Action {
	case ActionEvicted, ActionTruncated:
		return false
	case ActionPassed:
		return true
	case ActionFormatted:
		return isInPlaceFormatReason(rec.Reason)
	default:
		return false
	}
}

func shouldPersistPayloadAdd(rec TransformRecord) bool {
	switch rec.Action {
	case ActionEvicted, ActionTruncated:
		return false
	case ActionPassed:
		return true
	case ActionFormatted:
		return isInPlaceFormatReason(rec.Reason)
	default:
		return false
	}
}

func isInPlaceFormatReason(reason string) bool {
	switch reason {
	case ReasonReplacedByFormatter, ReasonReplacedByHook, ReasonReplacedByDeferred:
		return false
	default:
		return true
	}
}

func (r CompileResult) sourceSegment(seg SegmentName) []Message {
	switch seg {
	case SegmentSystem:
		return r.Source.System
	case SegmentHistory:
		return r.Source.History
	case SegmentMemory:
		return r.Source.Memory
	case SegmentTools:
		return r.Source.Tools
	default:
		return nil
	}
}

func (r CompileResult) payloadSegment(seg SegmentName) []Message {
	switch seg {
	case SegmentSystem:
		return r.Payload.System
	case SegmentHistory:
		return r.Payload.History
	case SegmentMemory:
		return r.Payload.Memory
	case SegmentTools:
		return r.Payload.Tools
	default:
		return nil
	}
}

func (r CompileResult) pendingIDSet() map[string]struct{} {
	out := make(map[string]struct{}, len(r.Source.Pending))
	for _, m := range r.Source.Pending {
		if m.ID != "" {
			out[m.ID] = struct{}{}
		}
	}
	return out
}

func (r CompileResult) payloadAddsForSegment(seg SegmentName, sourceMsgs []Message) []Message {
	sourceIDs := messageIDSet(sourceMsgs)
	pendingIDs := r.pendingIDSet()
	var out []Message
	for _, m := range r.payloadSegment(seg) {
		if m.ID == "" {
			continue
		}
		if _, pending := pendingIDs[m.ID]; pending {
			continue
		}
		if _, inSource := sourceIDs[m.ID]; inSource {
			continue
		}
		rec, ok := r.Transformations[m.ID]
		if !ok || !shouldPersistPayloadAdd(rec) {
			continue
		}
		if intro, hasIntro := r.Introduced[m.ID]; hasIntro {
			out = append(out, intro.Clone())
			continue
		}
		if rec.Action == ActionFormatted && isInPlaceFormatReason(rec.Reason) {
			continue
		}
		out = append(out, m.Clone())
	}
	return out
}
