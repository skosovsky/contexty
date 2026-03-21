package contexty

import "slices"

func cloneMessages(msgs []Message) []Message {
	if msgs == nil {
		return nil
	}
	cloned := make([]Message, len(msgs))
	for i, msg := range msgs {
		cloned[i] = msg.Clone()
	}
	return cloned
}

func cloneMessage(msg Message) Message {
	cloned := msg
	cloned.Content = cloneContentParts(msg.Content)
	cloned.ToolCalls = slices.Clone(msg.ToolCalls)
	cloned.Metadata = cloneMap(msg.Metadata)
	return cloned
}

func cloneContentParts(parts []ContentPart) []ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]ContentPart, len(parts))
	for i, part := range parts {
		cloned[i] = part
		if part.ImageURL != nil {
			imageURL := *part.ImageURL
			cloned[i].ImageURL = &imageURL
		}
	}
	return cloned
}

func cloneBlock(block MemoryBlock) MemoryBlock {
	cloned := block
	cloned.Messages = cloneMessages(block.Messages)
	return cloned
}

func cloneNamedBlocks(blocks []NamedBlock) []NamedBlock {
	if blocks == nil {
		return nil
	}
	cloned := make([]NamedBlock, len(blocks))
	for i, block := range blocks {
		cloned[i] = NamedBlock{
			Name:  block.Name,
			Block: cloneBlock(block.Block),
		}
	}
	return cloned
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = cloneValue(value)
	}
	return dst
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case []any:
		return cloneAnySlice(v)
	default:
		return v
	}
}

func cloneAnySlice(values []any) []any {
	if values == nil {
		return nil
	}
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = cloneValue(value)
	}
	return out
}
