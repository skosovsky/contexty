package contexty

import (
	"context"
	"encoding/json"
)

// HistorySnapshot is the result of Load: message history plus a monotonic version
// used for optimistic concurrency control. Version starts at 0 for an empty thread.
type HistorySnapshot struct {
	Messages []Message
	Version  int64
}

// HistoryStore persists message history for a single conversation thread with
// optimistic concurrency: every mutating operation must specify the version
// observed from the last successful Load (or 0 for a never-written thread).
type HistoryStore interface {
	// Load returns the full stored history for threadID in append order and its
	// current version. Empty history returns an empty slice and version 0 if the
	// thread has never been written.
	Load(ctx context.Context, threadID string) (HistorySnapshot, error)

	// Append appends msgs when the stored version equals expectedVersion, then
	// bumps the version. Empty msgs is a no-op (version unchanged).
	Append(ctx context.Context, threadID string, expectedVersion int64, msgs ...Message) error

	// Save replaces the full stored history when the stored version equals
	// expectedVersion, then bumps the version. Passing nil or an empty slice clears history.
	Save(ctx context.Context, threadID string, expectedVersion int64, msgs []Message) error

	// Clear removes all stored history when the stored version equals expectedVersion,
	// then bumps the version.
	Clear(ctx context.Context, threadID string, expectedVersion int64) error
}

// MessageSerializer converts messages to bytes for storage adapters.
type MessageSerializer interface {
	Marshal(msg Message) ([]byte, error)
	Unmarshal(data []byte, msg *Message) error
}

// DefaultJSONSerializer marshals Message values using encoding/json.
//
// JSON numbers in Metadata deserialize as float64 when using map[string]any; callers
// who need stable integer types should use [json.RawMessage] metadata or normalize
// after load. See migration notes in README.
type DefaultJSONSerializer struct{}

// Marshal serializes msg into JSON.
func (DefaultJSONSerializer) Marshal(msg Message) ([]byte, error) {
	return json.Marshal(msg)
}

// Unmarshal deserializes a JSON-encoded message into msg.
func (DefaultJSONSerializer) Unmarshal(data []byte, msg *Message) error {
	return json.Unmarshal(data, msg)
}

var _ MessageSerializer = DefaultJSONSerializer{}
