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

	"github.com/skosovsky/prompty"

	"github.com/skosovsky/contexty"
)

var ErrUnsupported = errors.New("chat consumer: unsupported mapping")

const metadataType = "host.chat_metadata"
const stateType = "host.chat_state"
const profileRevision = "fixed"
const mediaImage = "image"

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
	identity := contexty.Descriptor{ID: "host/chat-state", Revision: profileRevision}
	registry.RegisterOpaquePayload(stateType, identity, func(data []byte) (contexty.Extension, error) {
		var value StatePayload
		err := json.Unmarshal(data, &value)
		if err == nil && value.Digest != digest(value.Wire) {
			return nil, contexty.ErrInvalidOpaqueState
		}
		return value, err
	})
	profileBytes, _ := json.Marshal(destination) // Plain-data identity has no fallible fields.
	return Mapper{Codec: contexty.JSONSerializer{Extensions: registry, Provenance: nil},
		Profile: contexty.Descriptor{ID: string(profileBytes), Revision: profileRevision}, Destination: destination}
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
	wire, err := prompty.MarshalExecution(
		prompty.NewExecution([]prompty.ChatMessage{record.Message}),
		prompty.WirePolicy{State: prompty.StatePreserve},
	)
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
	block := continuation{States: native.ProviderState, Annotations: nil}
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
		envelope, stateErr := m.importState(record.ID, block, prefix)
		if stateErr != nil {
			return contexty.Message{}, false, stateErr
		}
		message.Extensions = append(message.Extensions, envelope)
	}
	return message, required, nil
}

func (m Mapper) importState(id string, block continuation, prefix []contexty.Message) (contexty.OpaqueState, error) {
	stateWire, marshalErr := json.Marshal(block)
	if marshalErr != nil {
		return contexty.OpaqueState{}, marshalErr
	}
	binding := contexty.OpaqueBinding{Profile: m.Profile, Required: nil, Prefix: nil, Boundary: ""}
	for _, previous := range prefix {
		ref, refErr := contexty.MessageContentRef(previous, m.Codec)
		if refErr != nil {
			return contexty.OpaqueState{}, refErr
		}
		binding.Prefix = append(binding.Prefix, ref)
	}
	if len(prefix) > 0 {
		binding.Boundary = prefix[len(prefix)-1].ID
	}
	return contexty.OpaqueState{
		ID: "state:" + id,
		Codec: contexty.Descriptor{
			ID:       "host/chat-state",
			Revision: profileRevision,
		},
		Payload:   StatePayload{Wire: stateWire, Digest: digest(stateWire)},
		Placement: contexty.OpaquePlacement{AfterPart: -1},
		Binding:   binding,
	}, nil
}

func importPart(part prompty.ContentPartWire) (contexty.ContentPart, error) {
	if part.CachePolicy != nil {
		return nil, ErrUnsupported
	}
	switch part.Type {
	case prompty.ContentPartWireText:
		return contexty.TextPart{Text: part.Text}, nil
	case prompty.ContentPartWireMedia:
		if part.MediaType != mediaImage && part.MediaType != "audio" && part.MediaType != "video" &&
			part.MediaType != "document" {
			return nil, ErrUnsupported
		}
		if part.URL != "" {
			if part.MediaType != mediaImage || len(part.Data) != 0 {
				return nil, ErrUnsupported
			}
			return contexty.ImagePart{URL: part.URL, Detail: ""}, nil
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
		return contexty.ToolCallPart{
			ID:            part.ToolCallID,
			Name:          part.Name,
			Arguments:     contexty.JSONPayload(part.Args),
			ArgumentsBlob: nil,
		}, nil
	case prompty.ContentPartWireToolResult:
		if len(part.Content) != 1 || part.Content[0].Type != prompty.ContentPartWireText ||
			part.Content[0].CachePolicy != nil {
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
	if message.Actor != nil || message.Origin != nil || message.LLMCache != nil || message.Provenance != nil ||
		message.Annotations.Timestamp != nil {
		return Record{}, nil, ErrUnsupported
	}
	native := prompty.MessageWire{Role: prompty.Role(message.Role), ProviderState: nil, Annotations: nil,
		MessageAnnotations: nil, ContinuationUnavailable: false, Content: nil, CachePolicy: nil,
		Provenance: nil, Metadata: nil, LayerKind: ""}
	decoded, err := decodeExtensions(message, native)
	if err != nil {
		return Record{}, nil, err
	}
	native = decoded.native
	states, hints := decoded.states, decoded.hints
	originalRole, metadataSeen := decoded.originalRole, decoded.metadataSeen

	native.Role = prompty.Role(message.Role)
	native.Content = nil
	if metadataSeen && len(hints) != len(message.Parts) {
		return Record{}, nil, ErrUnsupported
	}
	for i, part := range message.Parts {
		var hint prompty.ContentPartWire
		if metadataSeen {
			hint = hints[i]
		}
		projected, partErr := exportPart(part, hint)
		if partErr != nil {
			return Record{}, nil, partErr
		}
		native.Content = append(native.Content, projected)
	}
	if len(states) > 0 && (originalRole != message.Role || !reflect.DeepEqual(hints, native.Content)) {
		return Record{}, nil, contexty.ErrOpaqueStateInvalidated
	}
	// Only the message wire is populated in this host mapping profile.
	var executionWire prompty.PromptExecutionWire
	executionWire.Format, executionWire.Messages = prompty.ExecutionWireFormat, []prompty.MessageWire{native}
	execution, err := prompty.UnmarshalExecution(executionWire)
	if err != nil {
		return Record{}, nil, err
	}
	return Record{
		ID:         message.ID,
		SourceRefs: slices.Clone(message.SourceRefs),
		Message:    execution.Messages[0],
	}, states, nil
}

type decodedMessage struct {
	native       prompty.MessageWire
	states       []string
	hints        []prompty.ContentPartWire
	originalRole contexty.Role
	metadataSeen bool
}

func decodeExtensions(message contexty.Message, native prompty.MessageWire) (decodedMessage, error) {
	decoded := decodedMessage{native: native, states: nil, hints: nil, originalRole: message.Role, metadataSeen: false}
	for index, extension := range message.Extensions {
		switch value := extension.(type) {
		case Metadata:
			if decoded.metadataSeen || index != 0 {
				return decodedMessage{}, ErrUnsupported
			}
			decoded.metadataSeen = true
			if err := json.Unmarshal(value.Wire, &decoded.native); err != nil {
				return decodedMessage{}, err
			}
			if decoded.native.ProviderState != nil ||
				prompty.HasScopedMessageAnnotations(decoded.native.MessageAnnotations) ||
				decoded.native.CachePolicy != nil ||
				decoded.native.ContinuationUnavailable {
				return decodedMessage{}, ErrUnsupported
			}
			decoded.hints = decoded.native.Content
			decoded.originalRole = contexty.Role(decoded.native.Role)
		case contexty.OpaqueState:
			block, stateErr := decodeState(value)
			if stateErr != nil {
				return decodedMessage{}, stateErr
			}
			decoded.native.ProviderState = append(decoded.native.ProviderState, block.States...)
			decoded.native.MessageAnnotations = append(decoded.native.MessageAnnotations, block.Annotations...)
			decoded.states = append(decoded.states, value.ID)
		default:
			return decodedMessage{}, ErrUnsupported
		}
	}
	return decoded, nil
}

func decodeState(value contexty.OpaqueState) (continuation, error) {
	payload, ok := value.Payload.(StatePayload)
	if !ok || value.Placement.AfterPart != -1 || payload.Digest != digest(payload.Wire) {
		return continuation{}, ErrUnsupported
	}
	var block continuation
	if err := json.Unmarshal(payload.Wire, &block); err != nil {
		return continuation{}, err
	}
	if len(block.States)+len(block.Annotations) == 0 {
		return continuation{}, contexty.ErrInvalidOpaqueState
	}
	return block, nil
}

func exportPart(part contexty.ContentPart, hint prompty.ContentPartWire) (prompty.ContentPartWire, error) {
	var result prompty.ContentPartWire
	if hint.CachePolicy != nil {
		return result, ErrUnsupported
	}
	switch value := part.(type) {
	case contexty.TextPart:
		result.Type, result.Text = prompty.ContentPartWireText, value.Text
	case contexty.ImagePart:
		if value.Detail != "" || hint.MediaType != mediaImage {
			return result, ErrUnsupported
		}
		result.Type, result.MediaType, result.MIMEType, result.URL = prompty.ContentPartWireMedia, mediaImage, hint.MIMEType, value.URL
	case contexty.MediaPart:
		if hint.MediaType == "" || value.Validate() != nil {
			return result, ErrUnsupported
		}
		result.Type, result.MediaType, result.MIMEType, result.Data = prompty.ContentPartWireMedia, hint.MediaType, value.MIMEType, bytes.Clone(
			value.Data,
		)
	case contexty.ToolCallPart:
		if unsupportedArguments(value.Arguments) {
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
		if unsupportedToolResult(payload) {
			return result, ErrUnsupported
		}
		result.Type, result.ToolCallID, result.Name, result.IsError = prompty.ContentPartWireToolResult, value.ToolCallID, value.Name, value.IsError
		var text prompty.ContentPartWire
		text.Type, text.Text = prompty.ContentPartWireText, payload.Text
		result.Content = []prompty.ContentPartWire{text}
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
	if strings.TrimSpace(m.Destination.Provider) == "" || strings.TrimSpace(m.Destination.Model) == "" ||
		prompty.ValidateEndpointIdentity(m.Destination.Endpoint) != nil {
		return ErrUnsupported
	}
	identity, _ := json.Marshal(m.Destination)
	if m.Profile != (contexty.Descriptor{ID: string(identity), Revision: profileRevision}) {
		return ErrStaleExecution
	}
	return nil
}

func unsupportedArguments(payload contexty.ToolPayload) bool {
	return payload.Text != "" || len(payload.Binary) != 0 || payload.Error != nil || payload.Progress != nil ||
		payload.Control != nil ||
		payload.MIMEType != "" ||
		!json.Valid(payload.Data)
}
func unsupportedToolResult(payload contexty.ToolPayload) bool {
	return len(payload.Data)+len(payload.Binary) != 0 || payload.MIMEType != "" || payload.Error != nil ||
		payload.Progress != nil ||
		payload.Control != nil
}
