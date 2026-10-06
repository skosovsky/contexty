package contexty

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type lockWaitContext struct {
	context.Context

	once    sync.Once
	checked chan struct{}
}

func (c *lockWaitContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestRemediation_MemoryCanceledLockWait(t *testing.T) {
	for _, operation := range []string{"load", "commit", "clear"} {
		t.Run(operation, func(t *testing.T) {
			// Arrange: the operation sees an initially live context then waits for the mutex.
			store := NewMemoryConversationStateStore()
			message := TextMessage(RoleUser, "original")
			message.ID = "original"
			require.NoError(
				t,
				store.CommitState(
					context.Background(),
					"conversation",
					0,
					ConversationDelta{
						Operation: DeltaAppendMessages,
						Segment:   SegmentHistory,
						Messages:  []Message{message},
					},
				),
			)
			before, err := store.LoadState(context.Background(), "conversation")
			require.NoError(t, err)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &lockWaitContext{Context: base, checked: make(chan struct{})}
			store.mu.Lock()
			done := make(chan error, 1)
			go func() {
				switch operation {
				case "load":
					_, err = store.LoadState(ctx, "conversation")
				case "commit":
					err = store.CommitState(ctx, "conversation", before.Version(), ConversationDelta{})
				case "clear":
					err = store.ClearState(ctx, "conversation", before.Version())
				}
				done <- err
			}()
			<-ctx.checked
			cancel()
			// Act: cancellation precedes lock acquisition, with no timing/sleep assumption.
			store.mu.Unlock()
			err = <-done
			// Assert: canceled work publishes no changed payload or version.
			require.ErrorIs(t, err, context.Canceled)
			after, err := store.LoadState(context.Background(), "conversation")
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

type lockProbeProvenance struct{ Value string }

func (lockProbeProvenance) ProvenanceType() string        { return "host/lock-probe" }
func (p lockProbeProvenance) CloneProvenance() Provenance { return p }

func TestRemediation_MemoryCodecSerializesOtherIDs(t *testing.T) {
	// Arrange: one decoder holds the single reference-store mutex.
	registry := NewProvenanceRegistry()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	registry.Register("host/lock-probe", func([]byte) (Provenance, error) {
		once.Do(func() { close(entered) })
		<-release
		return lockProbeProvenance{Value: "v"}, nil
	})
	store := NewMemoryConversationStateStore(WithMemoryStateCodec(ConversationCodec{Provenance: registry}))
	message := TextMessage(RoleUser, "original")
	message.ID = "original"
	message.Provenance = lockProbeProvenance{Value: "v"}
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- store.CommitState(context.Background(), "a", 0, ConversationDelta{Operation: DeltaAppendMessages, Segment: SegmentHistory, Messages: []Message{message}})
	}()
	<-entered
	ctx := &lockWaitContext{Context: context.Background(), checked: make(chan struct{})}
	loadDone := make(chan error, 1)
	go func() { _, err := store.LoadState(ctx, "b"); loadDone <- err }()
	<-ctx.checked
	// Act: the independent ID cannot finish while the decoder runs under the shared lock.
	select {
	case <-loadDone:
		t.Fatal("load bypassed codec lock")
	default:
	}
	close(release)
	// Assert: releasing the codec completes both operations safely.
	require.NoError(t, <-commitDone)
	require.NoError(t, <-loadDone)
}
