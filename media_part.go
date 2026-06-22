package contexty

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"slices"
	"strings"
)

var (
	ErrInvalidMediaPart          = errors.New("contexty: invalid media part")
	ErrUnsupportedMediaRendering = errors.New("contexty: text view cannot render media")
)

const mimeApplicationJSON = "application/json"

// MediaPart preserves inline host-provided media without interpreting its format.
// MIMEType is explicit; Data is copied by message cloning and codec round-trips.
type MediaPart struct {
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"-"`
}

type mediaPartWire struct {
	MIMEType string `json:"mime_type"`
	DataHex  string `json:"data_hex"`
}

func (MediaPart) partKind() PartKind { return PartKindMedia }
func (p MediaPart) clonePart() ContentPart {
	p.Data = slices.Clone(p.Data)
	return p
}

func (p MediaPart) Validate() error {
	mediaType, _, err := mime.ParseMediaType(p.MIMEType)
	major, minor, ok := strings.Cut(mediaType, "/")
	if err != nil || !ok || major == "" || minor == "" || major == "*" || minor == "*" {
		return ErrInvalidMediaPart
	}
	return nil
}

func (p MediaPart) MarshalJSON() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(mediaPartWire{MIMEType: p.MIMEType, DataHex: hex.EncodeToString(p.Data)})
}

func (p *MediaPart) UnmarshalJSON(data []byte) error {
	if p == nil {
		return ErrInvalidMediaPart
	}
	*p = MediaPart{MIMEType: "", Data: nil}
	var wire struct {
		MIMEType string  `json:"mime_type"`
		DataHex  *string `json:"data_hex"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return ErrInvalidMediaPart
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrInvalidMediaPart
	}
	if wire.DataHex == nil {
		return ErrInvalidMediaPart
	}
	body, err := hex.DecodeString(*wire.DataHex)
	if err != nil {
		return ErrInvalidMediaPart
	}
	next := MediaPart{MIMEType: wire.MIMEType, Data: body}
	if err := next.Validate(); err != nil {
		return err
	}
	*p = next
	return nil
}
