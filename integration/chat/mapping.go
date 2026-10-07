// Package chat is an optional host-owned mapping and execution recipe.
package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/prompty"
)

var ErrUnsupported = errors.New("chat consumer: unsupported mapping")

const metadataType = "host.chat_metadata"
const stateType = "host.chat_state"

// Record keeps host identity separate from the native API's message type.
type Record struct {
	ID         string
	SourceRefs []contexty.SourceRef
	Message    prompty.ChatMessage
}

// Metadata carries native field hints, not continuation bytes.
type Metadata struct {
	Wire []byte `json:"wire"`
}

func (Metadata) ExtensionType() string                { return metadataType }
func (m Metadata) CloneExtension() contexty.Extension { return Metadata{Wire: bytes.Clone(m.Wire)} }

// StatePayload carries exact provider bytes in a host-owned encoding.
type StatePayload struct {
	Wire   []byte `json:"wire"`
	Digest string `json:"digest"`
}

func (StatePayload) ExtensionType() string { return stateType }
func (s StatePayload) CloneExtension() contexty.Extension {
	return StatePayload{Wire: bytes.Clone(s.Wire), Digest: s.Digest}
}

type continuation struct {
	States      []prompty.PartState         `json:"states"`
	Annotations []prompty.MessageAnnotation `json:"annotations"`
}

// Mapper pins codecs and the destination. No provider network calls are made.
type Mapper struct {
	Codec       contexty.JSONSerializer
	Profile     contexty.Descriptor
	Destination prompty.ProfileIdentity
}

func NewMapper(destination prompty.ProfileIdentity) Mapper {
	registry := contexty.NewExtensionRegistry()
	registry.Register(metadataType, func(data []byte) (contexty.Extension, error) {
		var value Metadata
		err := json.Unmarshal(data, &value)
		return value, err
	})
	identity := contexty.Descriptor{ID: "host/chat-state", Revision: "fixed"}
	registry.RegisterOpaquePayload(stateType, identity, func(data []byte) (contexty.Extension, error) {
		var value StatePayload
		err := json.Unmarshal(data, &value)
		if err == nil && value.Digest != digest(value.Wire) {
			return nil, contexty.ErrInvalidOpaqueState
		}
		return value, err
	})
	profileBytes, _ := json.Marshal(destination) // Plain-data identity has no fallible fields.
	return Mapper{Codec: contexty.JSONSerializer{Extensions: registry},
		Profile: contexty.Descriptor{ID: string(profileBytes), Revision: "fixed"}, Destination: destination}
}

// Import maps records in the caller's order and returns mandatory state IDs.
func (m Mapper) Import(records []Record, now time.Time) ([]contexty.Message, []string, error) {
	if err := m.validateIdentity(); err != nil {
		return nil, nil, err
	}
	var messages []contexty.Message
	var mandatory []string
	ids := make(map[string]bool)
	for _, record := range records {
		if record.ID == "" || ids[record.ID] {
			return nil, nil, ErrUnsupported
		}
		ids[record.ID] = true
		message, required, err := m.importRecord(record, messages, now)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, message)
		if required {
			mandatory = append(mandatory, "state:"+record.ID)
		}
	}
	return messages, mandatory, nil
}

func (m Mapper) importRecord(record Record, prefix []contexty.Message, now time.Time) (contexty.Message, bool, error) {
	role := contexty.Role(record.Message.Role)
	if err := role.Validate(); err != nil {
		return contexty.Message{}, false, err
	}
	if err := prompty.ValidateContinuation([]prompty.ChatMessage{record.Message}, m.Destination, now); err != nil {
		return contexty.Message{}, false, err
	}
	wire, err := prompty.MarshalExecution(prompty.NewExecution([]prompty.ChatMessage{record.Message}), prompty.WirePolicy{State: prompty.StatePreserve})
	if err != nil {
		return contexty.Message{}, false, err
	}
	native := wire.Messages[0]
	if native.CachePolicy != nil || native.ContinuationUnavailable {
		return contexty.Message{}, false, ErrUnsupported
	}
	var parts []contexty.ContentPart
	for _, part := range native.Content {
		semantic, partErr := importPart(part)
		if partErr != nil {
			return contexty.Message{}, false, partErr
		}
		parts = append(parts, semantic)
	}
	block := continuation{States: native.ProviderState}
	native.ProviderState = nil
	required := false
	for _, state := range block.States {
		required = required || state.Envelope.Required
	}
	if prompty.HasScopedMessageAnnotations(native.MessageAnnotations) {
		block.Annotations = native.MessageAnnotations
		native.MessageAnnotations = nil
		for _, annotation := range block.Annotations {
			required = required || annotation.Required
		}
	}
	metadata, err := json.Marshal(native)
	if err != nil {
		return contexty.Message{}, false, err
	}
	message := contexty.Message{ID: record.ID, Role: role, Parts: parts,
		SourceRefs: slices.Clone(record.SourceRefs), Extensions: []contexty.Extension{Metadata{Wire: metadata}}}
	if len(block.States)+len(block.Annotations) > 0 {
		stateWire, marshalErr := json.Marshal(block)
		if marshalErr != nil {
			return contexty.Message{}, false, marshalErr
		}
		binding := contexty.OpaqueBinding{Profile: m.Profile}
		for _, previous := range prefix {
			ref, refErr := contexty.MessageContentRef(previous, m.Codec)
			if refErr != nil {
				return contexty.Message{}, false, refErr
			}
			binding.Prefix = append(binding.Prefix, ref)
		}
		if len(prefix) > 0 {
			binding.Boundary = prefix[len(prefix)-1].ID
		}
		message.Extensions = append(message.Extensions, contexty.OpaqueState{ID: "state:" + record.ID,
			Codec: contexty.Descriptor{ID: "host/chat-state", Revision: "fixed"}, Payload: StatePayload{Wire: stateWire, Digest: digest(stateWire)},
			Placement: contexty.OpaquePlacement{AfterPart: -1}, Binding: binding})
	}
	return message, required, nil
}

func importPart(part prompty.ContentPartWire) (contexty.ContentPart, error) {
	if part.CachePolicy != nil {
		return nil, ErrUnsupported
	}
	switch part.Type {
	case prompty.ContentPartWireText:
		return contexty.TextPart{Text: part.Text}, nil
	case prompty.ContentPartWireMedia:
		if part.MediaType != "image" && part.MediaType != "audio" && part.MediaType != "video" && part.MediaType != "document" {
			return nil, ErrUnsupported
		}
		if part.URL != "" {
			if part.MediaType != "image" || len(part.Data) != 0 {
				return nil, ErrUnsupported
			}
			return contexty.ImagePart{URL: part.URL}, nil
		}
		media := contexty.MediaPart{MIMEType: part.MIMEType, Data: bytes.Clone(part.Data)}
		if err := media.Validate(); err != nil {
			return nil, err
		}
		return media, nil
	case prompty.ContentPartWireToolCall:
		if part.ArgsChunk != "" || !json.Valid([]byte(part.Args)) {
			return nil, ErrUnsupported
		}
		return contexty.ToolCallPart{ID: part.ToolCallID, Name: part.Name, Arguments: contexty.JSONPayload(part.Args)}, nil
	case prompty.ContentPartWireToolResult:
		if len(part.Content) != 1 || part.Content[0].Type != prompty.ContentPartWireText || part.Content[0].CachePolicy != nil {
			return nil, fmt.Errorf("%w: composite tool result", ErrUnsupported)
		}
		return contexty.ToolResultPart{ToolCallID: part.ToolCallID, Name: part.Name,
			IsError: part.IsError, Payload: contexty.TextPayload(part.Content[0].Text)}, nil
	default:
		return nil, fmt.Errorf("%w: content kind %q", ErrUnsupported, part.Type)
	}
}

// Export checks state, mandatory envelopes, scope/expiry and unsupported metadata.
// Output records preserve semantic IDs and SourceRefs; no external common DTO is introduced.
func (m Mapper) Export(messages []contexty.Message, mandatory []string, now time.Time) ([]Record, error) {
	if err := m.validateIdentity(); err != nil {
		return nil, err
	}
	if err := contexty.ValidateOpaqueState(messages, m.Codec, m.Profile); err != nil {
		return nil, err
	}
	found := make(map[string]bool)
	var records []Record
	for _, message := range messages {
		record, states, err := m.exportMessage(message)
		if err != nil {
			return nil, err
		}
		for _, id := range states {
			if found[id] {
				return nil, ErrUnsupported
			}
			found[id] = true
		}
		records = append(records, record)
	}
	for _, id := range mandatory {
		if !found[id] {
			return nil, prompty.ErrStateUnavailable
		}
	}
	native := make([]prompty.ChatMessage, len(records))
	for i, record := range records {
		native[i] = record.Message
	}
	if err := prompty.ValidateContinuation(native, m.Destination, now); err != nil {
		return nil, err
	}
	return records, nil
}

func (m Mapper) exportMessage(message contexty.Message) (Record, []string, error) {
	if err := message.Role.Validate(); err != nil {
		return Record{}, nil, err
	}
	if message.Actor != nil || message.Origin != nil || message.LLMCache != nil || message.Provenance != nil || message.Annotations.Timestamp != nil {
		return Record{}, nil, ErrUnsupported
	}
	native := prompty.MessageWire{Role: prompty.Role(message.Role)}
	var states []string
	originalRole := message.Role
	var hints []prompty.ContentPartWire
	metadataSeen := false
	for index, extension := range message.Extensions {
		switch value := extension.(type) {
		case Metadata:
			if metadataSeen || index != 0 {
				return Record{}, nil, ErrUnsupported
			}
			metadataSeen = true
			if err := json.Unmarshal(value.Wire, &native); err != nil {
				return Record{}, nil, err
			}
			if native.ProviderState != nil || prompty.HasScopedMessageAnnotations(native.MessageAnnotations) || native.CachePolicy != nil || native.ContinuationUnavailable {
				return Record{}, nil, ErrUnsupported
			}
			hints = native.Content
			originalRole = contexty.Role(native.Role)
		case contexty.OpaqueState:
			payload, ok := value.Payload.(StatePayload)
			if !ok || value.Placement.AfterPart != -1 || payload.Digest != digest(payload.Wire) {
				return Record{}, nil, ErrUnsupported
			}
			var block continuation
			if err := json.Unmarshal(payload.Wire, &block); err != nil {
				return Record{}, nil, err
			}
			if len(block.States)+len(block.Annotations) == 0 {
				return Record{}, nil, contexty.ErrInvalidOpaqueState
			}
			native.ProviderState = append(native.ProviderState, block.States...)
			native.MessageAnnotations = append(native.MessageAnnotations, block.Annotations...)
			states = append(states, value.ID)
		default:
			return Record{}, nil, ErrUnsupported
		}
	}
	native.Role = prompty.Role(message.Role)
	native.Content = nil
	if metadataSeen && len(hints) != len(message.Parts) {
		return Record{}, nil, ErrUnsupported
	}
	for i, part := range message.Parts {
		hint := prompty.ContentPartWire{}
		if metadataSeen {
			hint = hints[i]
		}
		projected, err := exportPart(part, hint)
		if err != nil {
			return Record{}, nil, err
		}
		native.Content = append(native.Content, projected)
	}
	if len(states) > 0 && (originalRole != message.Role || !reflect.DeepEqual(hints, native.Content)) {
		return Record{}, nil, contexty.ErrOpaqueStateInvalidated
	}
	execution, err := prompty.UnmarshalExecution(prompty.PromptExecutionWire{Format: prompty.ExecutionWireFormat, Messages: []prompty.MessageWire{native}})
	if err != nil {
		return Record{}, nil, err
	}
	return Record{ID: message.ID, SourceRefs: slices.Clone(message.SourceRefs), Message: execution.Messages[0]}, states, nil
}

func exportPart(part contexty.ContentPart, hint prompty.ContentPartWire) (prompty.ContentPartWire, error) {
	result := prompty.ContentPartWire{}
	if hint.CachePolicy != nil {
		return result, ErrUnsupported
	}
	switch value := part.(type) {
	case contexty.TextPart:
		result.Type, result.Text = prompty.ContentPartWireText, value.Text
	case contexty.ImagePart:
		if value.Detail != "" || hint.MediaType != "image" {
			return result, ErrUnsupported
		}
		result.Type, result.MediaType, result.MIMEType, result.URL = prompty.ContentPartWireMedia, "image", hint.MIMEType, value.URL
	case contexty.MediaPart:
		if hint.MediaType == "" || value.Validate() != nil {
			return result, ErrUnsupported
		}
		result.Type, result.MediaType, result.MIMEType, result.Data = prompty.ContentPartWireMedia, hint.MediaType, value.MIMEType, bytes.Clone(value.Data)
	case contexty.ToolCallPart:
		if value.Arguments.Text != "" || len(value.Arguments.Binary) != 0 || value.Arguments.Error != nil || value.Arguments.Progress != nil || value.Arguments.Control != nil || value.Arguments.MIMEType != "" || !json.Valid(value.Arguments.Data) {
			return result, ErrUnsupported
		}
		args := string(value.Arguments.Data)
		if hint.Type != "" {
			if !jsonEqual(value.Arguments.Data, []byte(hint.Args)) {
				return result, ErrUnsupported
			}
			args = hint.Args
		}
		result.Type, result.ToolCallID, result.Name, result.Args = prompty.ContentPartWireToolCall, value.ID, value.Name, args
	case contexty.ToolResultPart:
		payload := value.Payload
		if len(payload.Data)+len(payload.Binary) != 0 || payload.MIMEType != "" || payload.Error != nil || payload.Progress != nil || payload.Control != nil {
			return result, ErrUnsupported
		}
		result.Type, result.ToolCallID, result.Name, result.IsError = prompty.ContentPartWireToolResult, value.ToolCallID, value.Name, value.IsError
		result.Content = []prompty.ContentPartWire{{Type: prompty.ContentPartWireText, Text: payload.Text}}
	default:
		return result, ErrUnsupported
	}
	if hint.Type != "" && hint.Type != result.Type {
		return prompty.ContentPartWire{}, ErrUnsupported
	}
	return result, nil
}

func jsonEqual(a, b []byte) bool {
	var left, right any
	first, second := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	first.UseNumber()
	second.UseNumber()
	return first.Decode(&left) == nil && second.Decode(&right) == nil && reflect.DeepEqual(left, right)
}

func (m Mapper) validateIdentity() error {
	if strings.TrimSpace(m.Destination.Provider) == "" || strings.TrimSpace(m.Destination.Model) == "" || prompty.ValidateEndpointIdentity(m.Destination.Endpoint) != nil {
		return ErrUnsupported
	}
	identity, _ := json.Marshal(m.Destination)
	if m.Profile != (contexty.Descriptor{ID: string(identity), Revision: "fixed"}) {
		return ErrStaleExecution
	}
	return nil
}
