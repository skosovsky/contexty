package contexty

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// ToolPayload carries typed tool input/output data without tunneling metadata
// through TextPart.
//
//nolint:recvcheck // UnmarshalJSON must use a pointer; value methods keep payloads immutable.
type ToolPayload struct {
	Text     string          `json:"text,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Binary   []byte          `json:"-"`
	MIMEType string          `json:"mime_type,omitempty"`
	Error    *ToolError      `json:"error,omitempty"`
	Progress *ToolProgress   `json:"progress,omitempty"`
	Control  *ToolControl    `json:"control,omitempty"`
}

// ToolError describes a failed tool result payload.
type ToolError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// ToolProgress describes a progress update emitted by a tool.
type ToolProgress struct {
	Current int    `json:"current,omitempty"`
	Total   int    `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}

// ToolControl describes control-plane tool output.
type ToolControl struct {
	Action string `json:"action,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type toolPayloadWire struct {
	Text      string          `json:"text,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	BinaryHex string          `json:"binary_hex,omitempty"`
	MIMEType  string          `json:"mime_type,omitempty"`
	Error     *ToolError      `json:"error,omitempty"`
	Progress  *ToolProgress   `json:"progress,omitempty"`
	Control   *ToolControl    `json:"control,omitempty"`
}

// TextPayload returns a text tool payload.
func TextPayload(text string) ToolPayload {
	return ToolPayload{
		Text:     text,
		Data:     nil,
		Binary:   nil,
		MIMEType: "",
		Error:    nil,
		Progress: nil,
		Control:  nil,
	}
}

// JSONPayload returns a structured JSON tool payload.
func JSONPayload(raw string) ToolPayload {
	return ToolPayload{
		Text:     "",
		Data:     json.RawMessage(raw),
		Binary:   nil,
		MIMEType: "",
		Error:    nil,
		Progress: nil,
		Control:  nil,
	}
}

// StructuredPayload marshals a typed value into a structured JSON tool payload.
func StructuredPayload(v any) (ToolPayload, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return ToolPayload{}, fmt.Errorf("contexty: structured tool payload: %w", err)
	}
	return ToolPayload{
		Text:     "",
		Data:     append(json.RawMessage(nil), data...),
		Binary:   nil,
		MIMEType: "",
		Error:    nil,
		Progress: nil,
		Control:  nil,
	}, nil
}

// BinaryPayload returns a binary tool payload with an explicit MIME type.
func BinaryPayload(data []byte, mimeType string) ToolPayload {
	return ToolPayload{
		Text:     "",
		Data:     nil,
		Binary:   append([]byte(nil), data...),
		MIMEType: mimeType,
		Error:    nil,
		Progress: nil,
		Control:  nil,
	}
}

// Clone returns a deep copy of the payload.
func (p ToolPayload) Clone() ToolPayload {
	cp := ToolPayload{
		Text:     p.Text,
		Data:     nil,
		Binary:   nil,
		MIMEType: p.MIMEType,
		Error:    nil,
		Progress: nil,
		Control:  nil,
	}
	if len(p.Data) > 0 {
		cp.Data = append(json.RawMessage(nil), p.Data...)
	}
	if len(p.Binary) > 0 {
		cp.Binary = append([]byte(nil), p.Binary...)
	}
	if p.Error != nil {
		errCopy := *p.Error
		cp.Error = &errCopy
	}
	if p.Progress != nil {
		progressCopy := *p.Progress
		cp.Progress = &progressCopy
	}
	if p.Control != nil {
		controlCopy := *p.Control
		cp.Control = &controlCopy
	}
	return cp
}

// PlainText returns text suitable for deterministic plain renderers.
func (p ToolPayload) PlainText() string {
	if p.Text != "" {
		return p.Text
	}
	if len(p.Data) > 0 {
		return string(p.Data)
	}
	if p.Error != nil {
		return p.Error.Message
	}
	if p.Progress != nil {
		return p.Progress.Message
	}
	if p.Control != nil {
		return p.Control.Action
	}
	if len(p.Binary) > 0 {
		return fmt.Sprintf("[%d bytes]", len(p.Binary))
	}
	return ""
}

// MarshalJSON serializes binary data through an explicit hex field.
func (p ToolPayload) MarshalJSON() ([]byte, error) {
	wire := toolPayloadWire{
		Text:      p.Text,
		Data:      nil,
		BinaryHex: "",
		MIMEType:  p.MIMEType,
		Error:     p.Error,
		Progress:  p.Progress,
		Control:   p.Control,
	}
	if len(p.Data) > 0 {
		wire.Data = append(json.RawMessage(nil), p.Data...)
	}
	if len(p.Binary) > 0 {
		wire.BinaryHex = bytesToHex(p.Binary)
	}
	return json.Marshal(wire)
}

// UnmarshalJSON restores a typed tool payload.
func (p *ToolPayload) UnmarshalJSON(data []byte) error {
	var wire toolPayloadWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	binary, err := hexToBytes(wire.BinaryHex)
	if err != nil {
		return err
	}
	*p = ToolPayload{
		Text:     wire.Text,
		Data:     nil,
		MIMEType: wire.MIMEType,
		Binary:   binary,
		Error:    wire.Error,
		Progress: wire.Progress,
		Control:  wire.Control,
	}
	if len(wire.Data) > 0 {
		p.Data = append(json.RawMessage(nil), wire.Data...)
	}
	return nil
}

func bytesToHex(data []byte) string {
	const alphabet = "0123456789abcdef"
	out := make([]byte, len(data)*2)
	for i, b := range data {
		out[i*2] = alphabet[b>>4]
		out[i*2+1] = alphabet[b&0x0f]
	}
	return string(out)
}

func hexToBytes(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if len(s)%2 != 0 {
		return nil, errors.New("contexty: tool payload binary_hex has odd length")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		v, err := strconv.ParseUint(s[i:i+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("contexty: tool payload binary_hex: %w", err)
		}
		out[i/2] = byte(v)
	}
	return out, nil
}

// ToolCallPart is a structured tool invocation.
type ToolCallPart struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Arguments ToolPayload `json:"arguments"`
}

func (ToolCallPart) partKind() PartKind { return PartKindToolCall }

func (p ToolCallPart) clonePart() ContentPart {
	p.Arguments = p.Arguments.Clone()
	return p
}

// ToolResultPart is a structured tool result bound to a call ID.
type ToolResultPart struct {
	ToolCallID string      `json:"tool_call_id"`
	Name       string      `json:"name,omitempty"`
	Payload    ToolPayload `json:"payload"`
	IsError    bool        `json:"is_error,omitempty"`
}

func (ToolResultPart) partKind() PartKind { return PartKindToolResult }

func (p ToolResultPart) clonePart() ContentPart {
	p.Payload = p.Payload.Clone()
	return p
}

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
