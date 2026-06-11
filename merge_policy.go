package contexty

// MergePolicy controls how deferred block messages merge into an existing segment.
type MergePolicy string

const (
	PolicyAppend             MergePolicy = "append"
	PolicyReplaceByOrigin    MergePolicy = "replace_by_origin"
	PolicyDeduplicateByLayer MergePolicy = "deduplicate_by_layer"
)

func applyMergePolicy(existing, incoming []Message, policy MergePolicy) []Message {
	switch policy {
	case PolicyAppend, "":
		combined := make([]Message, len(existing)+len(incoming))
		copy(combined, existing)
		copy(combined[len(existing):], incoming)
		return combined
	case PolicyReplaceByOrigin:
		return mergeReplaceByOrigin(existing, incoming)
	case PolicyDeduplicateByLayer:
		return mergeDeduplicateByLayer(existing, incoming)
	default:
		combined := make([]Message, len(existing)+len(incoming))
		copy(combined, existing)
		copy(combined[len(existing):], incoming)
		return combined
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
	incomingLayers := layerIDSet(incoming)
	kept := make([]Message, 0, len(existing))
	for _, m := range existing {
		if m.Origin != nil && m.Origin.LayerID != "" && incomingLayers[m.Origin.LayerID] {
			continue
		}
		kept = append(kept, m)
	}
	seenLayers := layerIDSet(kept)
	filteredIncoming := make([]Message, 0, len(incoming))
	for _, m := range incoming {
		if m.Origin != nil && m.Origin.LayerID != "" {
			if seenLayers[m.Origin.LayerID] {
				continue
			}
			seenLayers[m.Origin.LayerID] = true
		}
		filteredIncoming = append(filteredIncoming, m)
	}
	combined := make([]Message, len(kept)+len(filteredIncoming))
	copy(combined, kept)
	copy(combined[len(kept):], filteredIncoming)
	return combined
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

func layerIDSet(msgs []Message) map[string]bool {
	out := make(map[string]bool)
	for _, m := range msgs {
		if m.Origin != nil && m.Origin.LayerID != "" {
			out[m.Origin.LayerID] = true
		}
	}
	return out
}
