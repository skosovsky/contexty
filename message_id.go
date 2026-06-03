package contexty

import (
	"crypto/rand"
	"encoding/hex"
)

// EnsureMessageID assigns a unique ID when empty.
func EnsureMessageID(msg Message) Message {
	if msg.ID != "" {
		return msg
	}
	msg.ID = newMessageID(msg)
	return msg
}

// EnsureMessageIDs assigns IDs to all messages missing one.
func EnsureMessageIDs(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = EnsureMessageID(m)
	}
	return out
}

func newMessageID(msg Message) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "msg:" + messageFingerprint(msg)
	}
	return hex.EncodeToString(b[:])
}
