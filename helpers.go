package contexty

// TextMessage builds a single-text-part message.
func TextMessage(role Role, text string) Message {
	return Message{
		Role:  role,
		Parts: []ContentPart{TextPart{Text: text}},
	}
}

// MultipartMessage builds a message with multiple content parts.
func MultipartMessage(role Role, parts ...ContentPart) Message {
	return Message{Role: role, Parts: parts}
}
