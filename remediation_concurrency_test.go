package contexty_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestRemediation_ConcurrentReuse(t *testing.T) {
	// Arrange: reusable configured instances and host callbacks with no mutable state.
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
		&contexty.FixedEstimator{TokensPerMessage: 1, TokensPerContentPart: 1},
	)
	engine := contexty.NewEngine(contexty.WithBudgetPipeline(pipe))
	registry := contexty.DefaultProvenanceRegistry()
	wire, err := contexty.EncodeProvenance(contexty.UserProvenance{Channel: "host", UserID: "user"})
	require.NoError(t, err)
	request := contexty.CompileRequest{
		History: remediationBenchMessages(3),
		Targets: []contexty.CompileTarget{{Name: "target", Segments: []contexty.SegmentName{contexty.SegmentHistory}}},
	}
	// Act: shared requests/configuration are reused; each worker mutates only owned results.
	var wait sync.WaitGroup
	failures := make(chan error, 16)
	for range 16 {
		wait.Go(func() {
			if failure := remediationReuseWorker(engine, pipe, request, registry, wire); failure != nil {
				failures <- failure
			}
		})
	}
	wait.Wait()
	close(failures)
	// Assert: no operation polluted shared input or independent output bindings.
	for failure := range failures {
		require.NoError(t, failure)
	}
	require.Equal(t, "owned benchmark content", request.History[0].TextContent())
}

func remediationReuseWorker(
	engine *contexty.Engine,
	pipe *contexty.BudgetPipeline,
	request contexty.CompileRequest,
	registry *contexty.ProvenanceRegistry,
	wire []byte,
) error {
	for range 10 {
		result, compileErr := engine.CompileSnapshot(context.Background(), request)
		if compileErr != nil {
			return compileErr
		}
		if len(result.Payload.History) != 3 || len(result.Projections["target"].Messages) != 3 {
			return errors.New("branch output lost history")
		}
		result.Payload.History[0].Parts[0] = contexty.TextPart{Text: "owned"}
		output, applyErr := pipe.Apply(context.Background(), request.History)
		if applyErr != nil {
			return applyErr
		}
		output.Messages[0].Parts[0] = contexty.TextPart{Text: "owned"}
		if _, decodeErr := registry.Decode(wire); decodeErr != nil {
			return decodeErr
		}
	}
	return nil
}
