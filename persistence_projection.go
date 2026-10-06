package contexty

// derivePersistenceSegment returns the source-aware persistence segment,
// using the owned Source baseline and compile transformations (excludes evicted/truncated;
// in-place formatted/ephemeral changes revert to Source or Introduced originals;
// structural replacements and deferred merge removals use payload adds; Pending excluded from history).
func (r CompileResult) derivePersistenceSegment(seg SegmentName) []Message {
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
		if shouldPersistSourceMessage(rec.Final()) {
			out = append(out, m.Clone())
		}
	}
	out = append(out, r.payloadAddsForSegment(seg, sourceMsgs)...)
	out = orderPersistenceMessages(out, r.payloadSegment(seg))
	out = append(out, r.currentTurnPersistenceMessages(seg)...)
	if len(out) == 0 {
		return nil
	}
	return out
}

func orderPersistenceMessages(selected, payload []Message) []Message {
	byID := make(map[string]Message, len(selected))
	for _, message := range selected {
		byID[message.ID] = message
	}
	out := make([]Message, 0, len(selected))
	for _, message := range payload {
		if original, exists := byID[message.ID]; exists {
			out = append(out, original)
			delete(byID, message.ID)
		}
	}
	for _, message := range selected {
		if _, exists := byID[message.ID]; exists {
			out = append(out, message)
			delete(byID, message.ID)
		}
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

func (r CompileResult) currentTurnPersistenceMessages(seg SegmentName) []Message {
	if seg != SegmentHistory || r.Source.CurrentTurn == nil {
		return nil
	}
	msg, ok := r.Source.CurrentTurn.persistedMessage()
	if !ok || msg.ID == "" {
		return nil
	}
	return []Message{msg}
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
	if r.Source.CurrentTurn != nil && r.Source.CurrentTurn.Raw.ID != "" {
		out[r.Source.CurrentTurn.Raw.ID] = struct{}{}
	}
	return out
}

func (r CompileResult) payloadAddsForSegment(seg SegmentName, sourceMsgs []Message) []Message {
	sourceIDs := messageIDSet(sourceMsgs)
	pendingIDs := r.pendingIDSet()
	var out []Message
	for _, m := range r.payloadSegment(seg) {
		if r.isArtifactMessage(m.ID) {
			continue
		}
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
		if !ok || !shouldPersistPayloadAdd(rec.Final()) {
			continue
		}
		if intro, hasIntro := r.Introduced[m.ID]; hasIntro {
			out = append(out, intro.Clone())
			continue
		}
		if rec.Final().Action == ActionFormatted && isInPlaceFormatReason(rec.Final().Reason) {
			continue
		}
		out = append(out, m.Clone())
	}
	return out
}

func (r CompileResult) isArtifactMessage(id string) bool {
	for _, artifact := range r.Artifacts {
		if id == "artifact:"+artifact.ID {
			return true
		}
	}
	return false
}

// DerivePersistenceState restores compile-only changes and validates all declared
// opaque dependencies jointly before returning any durable state. The host must
// explicitly supply the codec and integration profile used for that state.
func (r CompileResult) DerivePersistenceState(codec JSONSerializer, profile Descriptor) (ConversationState, error) {
	state := EmptyState()
	for _, segment := range viewSegmentOrder() {
		state = state.WithSegment(segment, r.derivePersistenceSegment(segment))
	}
	state = state.WithArtifacts(r.Artifacts)
	if err := ValidateOpaqueState(snapshotPayload(state).FlattenMessages(), codec, profile); err != nil {
		return ConversationState{}, err
	}
	return state, nil
}
