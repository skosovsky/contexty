package contexty

import (
	"encoding/json"
	"errors"
	"fmt"
)

// PartKind discriminates polymorphic content parts in wire JSON.
type PartKind string

const (
	PartKindText       PartKind = "text"
	PartKindImage      PartKind = "image"
	PartKindToolCall   PartKind = "tool_call"
	PartKindToolResult PartKind = "tool_result"
)

// ContentPart is a semantic content unit in a message AST.
type ContentPart interface {
	partKind() PartKind
	clonePart() ContentPart
}

// TextPart is plain text content.
type TextPart struct {
	Text string `json:"text"`
}

func (TextPart) partKind() PartKind { return PartKindText }

func (p TextPart) clonePart() ContentPart { return p }

// ImagePart references image content by URL.
type ImagePart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func (ImagePart) partKind() PartKind { return PartKindImage }

func (p ImagePart) clonePart() ContentPart { return p }

// ToolCallPart is a structured tool invocation (first-class, not Base64 text).
type ToolCallPart struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON object string
}

func (ToolCallPart) partKind() PartKind { return PartKindToolCall }

func (p ToolCallPart) clonePart() ContentPart { return p }

// ToolResultPart is a structured tool result bound to a call ID.
type ToolResultPart struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

func (ToolResultPart) partKind() PartKind { return PartKindToolResult }

func (p ToolResultPart) clonePart() ContentPart { return p }

type partWire struct {
	Kind PartKind        `json:"kind"`
	Body json.RawMessage `json:"body"`
}

// MarshalParts serializes typed content parts to JSON.
func MarshalParts(parts []ContentPart) ([]byte, error) {
	wires := make([]partWire, len(parts))
	for i, part := range parts {
		if part == nil {
			return nil, fmt.Errorf("contexty: marshal parts: nil part at index %d", i)
		}
		body, err := json.Marshal(part)
		if err != nil {
			return nil, fmt.Errorf("contexty: marshal parts: %w", err)
		}
		wires[i] = partWire{Kind: part.partKind(), Body: body}
	}
	return json.Marshal(wires)
}

// UnmarshalParts deserializes JSON into typed content parts.
func UnmarshalParts(data []byte) ([]ContentPart, error) {
	var wires []partWire
	if err := json.Unmarshal(data, &wires); err != nil {
		return nil, fmt.Errorf("contexty: unmarshal parts: %w", err)
	}
	out := make([]ContentPart, len(wires))
	for i, wire := range wires {
		part, err := decodePart(wire)
		if err != nil {
			return nil, fmt.Errorf("contexty: unmarshal parts index %d: %w", i, err)
		}
		out[i] = part
	}
	return out, nil
}

func decodePart(wire partWire) (ContentPart, error) {
	switch wire.Kind {
	case PartKindText:
		var p TextPart
		if err := json.Unmarshal(wire.Body, &p); err != nil {
			return nil, err
		}
		return p, nil
	case PartKindImage:
		var p ImagePart
		if err := json.Unmarshal(wire.Body, &p); err != nil {
			return nil, err
		}
		return p, nil
	case PartKindToolCall:
		var p ToolCallPart
		if err := json.Unmarshal(wire.Body, &p); err != nil {
			return nil, err
		}
		return p, nil
	case PartKindToolResult:
		var p ToolResultPart
		if err := json.Unmarshal(wire.Body, &p); err != nil {
			return nil, err
		}
		return p, nil
	case "":
		return nil, errors.New("missing kind discriminator")
	default:
		return nil, fmt.Errorf("unknown content part kind %q", wire.Kind)
	}
}
