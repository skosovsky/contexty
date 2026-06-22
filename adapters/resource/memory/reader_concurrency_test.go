package memory_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/resource/memory"
)

func TestReaderConcurrentRead(t *testing.T) {
	// Arrange: immutable backing snapshots, with application-owned concurrent authorization.
	body, request := resourceFixture(t, "selected")
	var calls atomic.Int64
	reader, err := memory.New(memory.Config{MaxBodyBytes: request.MaxBytes,
		Authorize: func(context.Context, contexty.ResourceReadRequest) error {
			calls.Add(1)
			return nil
		}}, body)
	require.NoError(t, err)
	const readers = 16
	var group sync.WaitGroup
	results := make(chan contexty.ResourceBody, readers)
	errors := make(chan error, readers)
	// Act: each authorized read gets its own containers, with no shared mutation.
	for range readers {
		group.Go(func() {
			selected, readErr := reader.ReadResource(context.Background(), request)
			errors <- readErr
			if readErr == nil {
				selected.Artifact.SourceRefs[0].ID = "caller-owned"
				results <- selected
			}
		})
	}
	group.Wait()
	close(errors)
	close(results)
	// Assert: all authorization decisions executed; original metadata stays unchanged.
	for readErr := range errors {
		require.NoError(t, readErr)
	}
	require.Len(t, results, readers)
	require.Equal(t, int64(readers), calls.Load())
	reloaded, err := reader.ReadResource(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, body, reloaded)
}
