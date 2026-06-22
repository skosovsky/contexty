package contexty_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestFinal_EstimatorCancellation(t *testing.T) {
	for _, target := range []bool{false, true} {
		for _, hostError := range []bool{false, true} {
			t.Run(fmt.Sprintf("target=%v/hostError=%v", target, hostError), func(t *testing.T) {
				// Arrange: only the final representation triggers cancellation, not initial budgeting.
				ctx, cancel := context.WithCancel(context.Background())
				finalCalls := 0
				counter := fixtureEvidenceEstimator{
					per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
						weights := make([]int, len(messages))
						for i := range weights {
							weights[i] = 1
						}
						return weights, nil
					},
					total: func(_ context.Context, messages []contexty.Message) (int, error) {
						if len(messages) == 1 && messages[0].TextContent() == "final" {
							finalCalls++
							cancel()
							if hostError {
								return 0, contexty.ErrInvalidDescriptor
							}
						}
						return len(messages), nil
					},
				}
				engine, request := fixtureFinalCancellationRequest(target, counter)
				// Act.
				result, err := engine.CompileSnapshot(ctx, request)
				cancel()
				// Assert: no success/partial result, cancellation rather than host count error.
				require.ErrorIs(t, err, context.Canceled, "target=%v, hostError=%v", target, hostError)
				require.Zero(t, result)
				require.Equal(t, 1, finalCalls)
				require.Equal(t, "input", request.History[0].TextContent())
			})
		}
	}
}
