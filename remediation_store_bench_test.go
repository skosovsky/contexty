package contexty

import (
	"context"
	"math/bits"
	"strconv"
	"sync/atomic"
	"testing"
)

func BenchmarkRemediation_MemoryStoreParallel(b *testing.B) {
	for _, work := range []int{0, 10000} {
		b.Run(strconv.Itoa(work), func(b *testing.B) {
			// Arrange: each worker owns its ID/token; all codec work shares the store mutex.
			var memoryCodecWorkSink atomic.Uint64
			registry := NewProvenanceRegistry()
			registry.Register("host/lock-probe", func([]byte) (Provenance, error) {
				value := uint64(1)
				for index := range work {
					value = bits.RotateLeft64(value, 1) ^ uint64(index)
				}
				memoryCodecWorkSink.Store(value)
				return lockProbeProvenance{Value: "v"}, nil
			})
			store := NewMemoryConversationStateStore(WithMemoryStateCodec(ConversationCodec{Provenance: registry}))
			message := TextMessage(RoleUser, "payload")
			message.ID = "message"
			message.Provenance = lockProbeProvenance{Value: "v"}
			delta := ConversationDelta{
				Operation: DeltaReplaceSegment,
				Segment:   SegmentHistory,
				Messages:  []Message{message},
			}
			var sequence atomic.Int64
			b.ReportAllocs()
			b.ResetTimer()
			// Act: measure reference-store reuse; this is not a throughput promise.
			b.RunParallel(func(pb *testing.PB) {
				id := strconv.FormatInt(sequence.Add(1), 10)
				version := int64(0)
				for pb.Next() {
					if err := store.CommitState(context.Background(), id, version, delta); err != nil {
						b.Error(err)
						return
					}
					version++
				}
			})
		})
	}
}
