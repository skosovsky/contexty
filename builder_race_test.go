package contexty

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestBuilder_ConcurrentCloneBuild exercises the recommended pattern: one template Builder,
// Clone per goroutine, then Build — no concurrent mutation on a shared Builder instance.
func TestBuilder_ConcurrentCloneBuild(t *testing.T) {
	t.Parallel()

	counter := &FixedCounter{TokensPerMessage: 1}
	base := NewBuilder(100, counter).
		AddBlock("a", MemoryBlock{
			Strategy: NewStrictStrategy(),
			Messages: []Message{TextMessage(RoleUser, "x")},
		})

	const workers = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			b := base.Clone()
			msgs, err := b.Build(context.Background())
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if len(msgs) != 1 {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("got %d messages, want 1", len(msgs))
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
}
