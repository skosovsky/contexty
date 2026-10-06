package contexty

// MergePolicy controls how deferred block messages merge into an existing segment.
type MergePolicy string

const (
	PolicyAppend             MergePolicy = "append"
	PolicyReplaceByOrigin    MergePolicy = "replace_by_origin"
	PolicyDeduplicateByLayer MergePolicy = "deduplicate_by_layer"
)

func applyMergePolicy(existing, incoming []Message, policy MergePolicy) ([]Message, error) {
	switch policy {
	case PolicyAppend, "":
		combined := make([]Message, len(existing)+len(incoming))
		copy(combined, existing)
		copy(combined[len(existing):], incoming)
		return combined, nil
	case PolicyReplaceByOrigin:
		return mergeReplaceByOrigin(existing, incoming), nil
	case PolicyDeduplicateByLayer:
		return mergeDeduplicateByLayer(existing, incoming), nil
	default:
		return nil, ErrInvalidCompileConfiguration
	}
}

func mergeReplaceByOrigin(existing, incoming []Message) []Message {
	templateIDs := incomingTemplateIDs(incoming)
	if len(templateIDs) == 0 {
		combined := make([]Message, len(existing)+len(incoming))
		copy(combined, existing)
		copy(combined[len(existing):], incoming)
		return combined
	}
	filtered := make([]Message, 0, len(existing))
	for _, m := range existing {
		if m.Origin == nil || m.Origin.TemplateID == "" {
			filtered = append(filtered, m)
			continue
		}
		if templateIDs[m.Origin.TemplateID] {
			continue
		}
		filtered = append(filtered, m)
	}
	combined := make([]Message, len(filtered)+len(incoming))
	copy(combined, filtered)
	copy(combined[len(filtered):], incoming)
	return combined
}

func mergeDeduplicateByLayer(existing, incoming []Message) []Message {
	incomingLayers := layerKeySet(incoming)
	combined := make([]Message, 0, len(existing)+len(incoming))
	for _, message := range existing {
		if message.Origin != nil && message.Origin.LayerID != "" &&
			incomingLayers[layerKey{Template: message.Origin.TemplateID, Layer: message.Origin.LayerID}] {
			continue
		}
		combined = append(combined, message)
	}
	return append(combined, incoming...)
}

func incomingTemplateIDs(incoming []Message) map[string]bool {
	out := make(map[string]bool)
	for _, m := range incoming {
		if m.Origin != nil && m.Origin.TemplateID != "" {
			out[m.Origin.TemplateID] = true
		}
	}
	return out
}

type layerKey struct{ Template, Layer string }

func layerKeySet(messages []Message) map[layerKey]bool {
	keys := make(map[layerKey]bool)
	for _, message := range messages {
		if message.Origin != nil && message.Origin.LayerID != "" {
			keys[layerKey{Template: message.Origin.TemplateID, Layer: message.Origin.LayerID}] = true
		}
	}
	return keys
}
