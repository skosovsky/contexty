package contexty

import "errors"

var (
	ErrEmptyCheckpointCommit       = errors.New("contexty: empty checkpoint commit")
	ErrInvalidCheckpoint           = errors.New("contexty: invalid checkpoint")
	ErrUnsupportedCheckpointSchema = errors.New("contexty: unsupported checkpoint schema")
)

// ConversationSchema identifies the lossless semantic state wire format, not OCC.
const ConversationSchema = "contexty/conversation/1"

// ProjectCheckpoint explicitly filters working artifacts for durable persistence.
func ProjectCheckpoint(state ConversationState) (ConversationState, error) {
	artifacts := state.Artifacts()
	for _, artifact := range artifacts {
		if artifact.ID == "" {
			return ConversationState{}, ErrInvalidCheckpoint
		}
		switch artifact.Lifecycle {
		case "", ArtifactLifecyclePersistent, ArtifactLifecycleTurnBound, ArtifactLifecycleEphemeral:
		default:
			return ConversationState{}, ErrInvalidCheckpoint
		}
		switch artifact.Persistence {
		case ArtifactPersistenceDefault, ArtifactPersistenceStore, ArtifactPersistenceSkip:
		default:
			return ConversationState{}, ErrInvalidCheckpoint
		}
	}
	if err := validateArtifactBlobs(artifacts); err != nil {
		return ConversationState{}, err
	}
	return state.AllSegmentsSnapshot().WithArtifacts(persistentArtifacts(artifacts)), nil
}

// MemoryStateStoreOption configures the reference store's durable codec.
type MemoryStateStoreOption func(*MemoryConversationStateStore)

// WithMemoryStateCodec supplies host codecs for checkpoint parts and extensions.
func WithMemoryStateCodec(codec ConversationCodec) MemoryStateStoreOption {
	return func(store *MemoryConversationStateStore) { store.codec = codec }
}
