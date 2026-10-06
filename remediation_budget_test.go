package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestRemediation_BudgetExplicitEstimator(t *testing.T) {
	for _, estimator := range []contexty.TokenEstimator{nil, (*contexty.FixedEstimator)(nil), &contexty.FixedEstimator{TokensPerMessage: -1}, &contexty.CharFallbackEstimator{CharsPerToken: 1, TokensPerNonTextPart: -1}} {
		// Arrange: invalid configuration precedes any callback.
		calls := 0
		pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)}, estimator)
		engine := contexty.NewEngine(
			contexty.WithBudgetPipeline(pipe),
			contexty.WithDeferredBlocks(
				contexty.DeferredBlock{
					Resolve: func(context.Context) (contexty.DeferredResult, error) { calls++; return contexty.DeferredResult{}, nil },
				},
			),
		)
		// Act / Assert.
		for _, compile := range []func(context.Context, contexty.CompileRequest) (contexty.CompileResult, error){engine.Compile, engine.CompileSnapshot} {
			result, err := compile(context.Background(), contexty.CompileRequest{})
			require.Error(t, err)
			require.Zero(t, result)
		}
		result, err := pipe.Apply(context.Background(), nil)
		require.Error(t, err)
		require.Zero(t, result)
		require.Zero(t, calls)
	}
}

func TestRemediation_BudgetArithmetic(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	messages := []contexty.Message{fixtureRollingText("a", "ab"), fixtureRollingText("b", "ab")}
	for _, estimator := range []contexty.TokenEstimator{
		&contexty.FixedEstimator{TokensPerMessage: maximum, TokensPerContentPart: 2},
		&contexty.FixedEstimator{TokensPerMessage: maximum/2 + 1},
		&contexty.FixedEstimator{TokensPerContentPart: maximum},
		&contexty.CharFallbackEstimator{CharsPerToken: 4, TokensPerNonTextPart: maximum},
		&contexty.CharFallbackEstimator{CharsPerToken: 1, EstimateTool: func(contexty.ToolCallPart) int { return maximum }},
	} {
		// Arrange: deterministic overflow across components, messages or tool overhead.
		input := messages
		if counter, ok := estimator.(*contexty.CharFallbackEstimator); ok && counter.EstimateTool != nil {
			input = []contexty.Message{
				{
					ID:    "call",
					Role:  contexty.RoleAssistant,
					Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "c", Name: "f"}},
				},
			}
		}
		if counter, ok := estimator.(*contexty.CharFallbackEstimator); ok && counter.TokensPerNonTextPart == maximum {
			input = []contexty.Message{
				{
					ID:   "images",
					Role: contexty.RoleUser,
					Parts: []contexty.ContentPart{
						contexty.ImagePart{URL: "https://example.test/1"},
						contexty.ImagePart{URL: "https://example.test/2"},
					},
				},
			}
		}

		// Act / Assert: invalid arithmetic cannot become a fitting negative/small number.
		_, err := estimator.Estimate(context.Background(), input)
		require.ErrorIs(t, err, contexty.ErrInconsistentEstimate)
		pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(2)}, estimator)
		result, err := pipe.Apply(context.Background(), input)
		require.Error(t, err)
		require.Zero(t, result)
	}
	// Arrange / Act: quotient/remainder ceiling division at MaxInt.
	cost, err := (&contexty.CharFallbackEstimator{CharsPerToken: maximum}).Estimate(context.Background(), messages[:1])
	// Assert.
	require.NoError(t, err)
	require.Equal(t, 1, cost)
}

func TestRemediation_BudgetRequestOverhead(t *testing.T) {
	for _, active := range []bool{false, true} {
		// Arrange: overhead is charged once for the complete request, including a fixed section.
		counter := fixtureEvidenceEstimator{
			total: func(_ context.Context, messages []contexty.Message) (int, error) {
				if len(messages) == 0 {
					return 0, nil
				}
				return 10 + len(messages), nil
			},
			per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
				weights := make([]int, len(messages))
				for index := range weights {
					weights[index] = 1
				}
				if len(weights) > 0 {
					weights[0] += 10
				}
				return weights, nil
			},
		}
		descriptor := contexty.Descriptor{ID: "host/overhead", Revision: "1"}
		reporter, err := contexty.NewEstimateReporter(
			counter,
			contexty.EstimateProfile{
				Model:     descriptor,
				Estimator: descriptor,
				Method:    descriptor,
				Encoding:  descriptor,
				Capabilities: map[contexty.EstimateKind]contexty.EstimateQuality{
					contexty.EstimateText:       contexty.EstimateCounted,
					contexty.EstimateImage:      contexty.EstimateUnknown,
					contexty.EstimateToolCall:   contexty.EstimateUnknown,
					contexty.EstimateToolResult: contexty.EstimateUnknown,
					contexty.EstimateMedia:      contexty.EstimateUnknown,
					contexty.EstimateExtension:  contexty.EstimateUnknown,
				},
			},
			contexty.DefaultJSONSerializer(),
		)
		require.NoError(t, err)
		request := contexty.CompileRequest{
			System:  []contexty.Message{fixtureRollingText("s", "system")},
			History: []contexty.Message{fixtureRollingText("h", "history")},
		}
		limit := 12
		if active {
			request.Pending = []contexty.Message{fixtureRollingText("p", "pending")}
			turn := contexty.NewCurrentTurn(fixtureRollingText("c", "current"))
			request.CurrentTurn = &turn
			limit += 2
		}
		pipe := contexty.NewBudgetPipeline(
			contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(limit)},
			reporter,
		)
		// Act.
		result, err := contexty.NewEngine(contexty.WithBudgetPipeline(pipe)).
			CompileSnapshot(context.Background(), request)
		// Assert: exactly fitting original history survives, with protected active events.
		require.NoError(t, err)
		require.Equal(t, "h", result.Payload.History[0].ID)
		require.Len(t, result.Payload.History, limit-11)
	}
}

func TestRemediation_BudgetMutatingWeights(t *testing.T) {
	// Arrange: per-message estimator mutates parts and exposes a host-owned weight slice.
	weights := []int{5, 5}
	counter := fixtureEvidenceEstimator{
		total: func(_ context.Context, messages []contexty.Message) (int, error) { return len(messages) * 5, nil },
		per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
			for index := range messages {
				messages[index].Parts[0] = contexty.TextPart{Text: "mutated"}
			}
			return weights, nil
		},
	}
	input := []contexty.Message{fixtureRollingText("a", "original-a"), fixtureRollingText("b", "original-b")}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(5)}, counter)
	// Act.
	result, err := pipe.Apply(context.Background(), input)
	weights[1] = 999
	// Assert: caller and retained context stay original; accepted evidence is owned.
	require.NoError(t, err)
	require.Equal(t, "original-b", input[1].TextContent())
	require.Equal(t, "original-b", result.Messages[0].TextContent())
	require.Equal(t, 5, result.Decision.AfterTokens)
}

func TestRemediation_BudgetInvalidWeights(t *testing.T) {
	for _, weights := range [][]int{{-1, 11}, {int(^uint(0) >> 1), 1}, {1}, {1, 1}} {
		// Arrange: invalid sums/shape cannot feed suffix/subtraction algorithms.
		counter := fixtureEvidenceEstimator{
			total: func(context.Context, []contexty.Message) (int, error) { return 10, nil },
			per:   func(context.Context, []contexty.Message) ([]int, error) { return weights, nil },
		}
		input := []contexty.Message{fixtureRollingText("a", "a"), fixtureRollingText("b", "b")}
		// Act.
		result, err := contexty.NewDropHeadStrategy(contexty.DropHeadConfig{}).
			Apply(context.Background(), input, 10, 5, counter)
		// Assert.
		require.Error(t, err)
		require.Nil(t, result)
	}
}

func TestRemediation_BudgetInvalidOptionalConfiguration(t *testing.T) {
	for _, config := range []contexty.BudgetConfig{
		{DropHead: contexty.DropHeadConfig{MinMessages: -1}},
		{TruncateStrategy: contexty.NewDropHeadStrategy(contexty.DropHeadConfig{MinMessages: -1})},
		{Summarizer: (*fixtureSummarizer)(nil)},
	} {
		// Arrange: even fitting input must reject an invalid configured optional port.
		config.Budget = contexty.EffectiveInputBudget(100)
		pipe := contexty.NewBudgetPipeline(config, contexty.CharTokenEstimator{})
		// Act.
		result, err := pipe.Apply(context.Background(), []contexty.Message{fixtureRollingText("a", "a")})
		// Assert.
		require.ErrorIs(t, err, contexty.ErrInvalidBudgetRequest)
		require.Zero(t, result)
	}
}

func TestRemediation_BudgetNonmonotonicCandidates(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		// Arrange: individually expensive fixed/pending subsets have cheaper complete candidates.
		counter := fixtureEvidenceEstimator{
			total: func(_ context.Context, messages []contexty.Message) (int, error) {
				switch len(messages) {
				case 0:
					return 0, nil
				case 1:
					return 100, nil
				case 2:
					return 8, nil
				default:
					return 30, nil
				}
			},
			per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
				weights := make([]int, len(messages))
				if len(weights) > 0 {
					switch len(weights) {
					case 1:
						weights[0] = 100
					case 2:
						weights[0] = 8
					default:
						weights[0] = 30
					}
				}
				return weights, nil
			},
		}
		request := contexty.CompileRequest{
			System:  []contexty.Message{fixtureRollingText("s", "system")},
			Pending: []contexty.Message{fixtureRollingText("p", "pending")},
		}
		if overflow {
			request.Pending = nil
			request.History = []contexty.Message{
				fixtureRollingText("h1", "history1"),
				fixtureRollingText("h2", "history2"),
			}
		}
		pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)}, counter)
		// Act.
		result, err := contexty.NewEngine(contexty.WithBudgetPipeline(pipe)).
			CompileSnapshot(context.Background(), request)
		// Assert: admission uses complete request/candidates, never the separately costly subset.
		require.NoError(t, err)
		require.Len(t, result.Payload.History, 1)
		require.Equal(t, 8, result.BudgetDecisions[0].Decision.AfterTokens)
	}
}

func TestRemediation_BudgetEmptyEstimate(t *testing.T) {
	for _, cost := range []int{20, -1} {
		// Arrange: empty requests can still have host cost or an invalid estimate.
		counter := fixtureEvidenceEstimator{
			total: func(context.Context, []contexty.Message) (int, error) { return cost, nil },
		}
		pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1)}, counter)
		// Act.
		result, err := pipe.Apply(context.Background(), nil)
		// Assert: empty data cannot bypass the supplied estimator.
		require.Error(t, err)
		require.Zero(t, result)
		if cost < 0 {
			require.ErrorIs(t, err, contexty.ErrInconsistentEstimate)
		} else {
			require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
		}
	}
}

func TestRemediation_BudgetCandidateFinalEstimate(t *testing.T) {
	// Arrange: after accepting a candidate, the next full estimate changes above the limit.
	candidateCalls := 0
	counter := fixtureEvidenceEstimator{total: func(_ context.Context, messages []contexty.Message) (int, error) {
		if len(messages) == 3 {
			return 30, nil
		}
		candidateCalls++
		if candidateCalls == 1 {
			return 8, nil
		}
		return 20, nil
	}}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:    contexty.EffectiveInputBudget(10),
			Retention: contexty.RetentionPolicy{MessageIDs: []string{"required"}},
		},
		counter,
	)
	input := []contexty.Message{
		fixtureRollingText("required", "required"),
		fixtureRollingText("a", "a"),
		fixtureRollingText("b", "b"),
	}
	// Act.
	result, err := pipe.Apply(context.Background(), input)
	// Assert: the final observed count cannot exceed the reported hard allowance.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Zero(t, result)
}

func TestRemediation_BudgetAtomicityPolicyParity(t *testing.T) {
	for _, counter := range []contexty.TokenEstimator{&contexty.FixedEstimator{TokensPerMessage: 5}, fixtureEvidenceEstimator{total: func(_ context.Context, messages []contexty.Message) (int, error) { return len(messages) * 5, nil }, per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
		weights := make([]int, len(messages))
		for index := range weights {
			weights[index] = 5
		}
		return weights, nil
	}}} {
		// Arrange: explicit opt-out may create an invalid round, which must still be rejected.
		input := fixtureRoundMessages()
		input[1].Parts = append(
			input[1].Parts,
			contexty.ToolResultPart{ToolCallID: "second", Payload: contexty.TextPayload("done")},
		)
		input = append([]contexty.Message{fixtureRollingText("old", "old")}, input...)
		input = append(input, fixtureRollingText("new", "new"))
		pipe := contexty.NewBudgetPipeline(
			contexty.BudgetConfig{
				Budget:   contexty.EffectiveInputBudget(17),
				DropHead: contexty.DropHeadConfig{KeepTurnAtomicity: contexty.BoolPtr(false)},
			},
			counter,
		)
		request := contexty.CompileRequest{
			System:  []contexty.Message{fixtureRollingText("s", "system")},
			History: input,
		}
		// Act.
		result, err := contexty.NewEngine(contexty.WithBudgetPipeline(pipe)).
			CompileSnapshot(context.Background(), request)
		// Assert: intrinsic and host estimator paths honor the same policy and round validation.
		require.ErrorIs(t, err, contexty.ErrInvalidToolRound)
		require.Zero(t, result)
	}
}

func TestRemediation_BudgetUnsupportedBinary(t *testing.T) {
	for _, counter := range []contexty.TokenEstimator{contexty.CharTokenEstimator{}, &contexty.CharFallbackEstimator{CharsPerToken: 4}} {
		// Arrange: binary bytes have no raw text token approximation.
		message := contexty.Message{
			ID:   "binary",
			Role: contexty.RoleTool,
			Parts: []contexty.ContentPart{
				contexty.ToolResultPart{
					ToolCallID: "c",
					Payload:    contexty.BinaryPayload([]byte{1, 2, 3}, "application/octet-stream"),
				},
			},
		}
		// Act.
		_, err := counter.Estimate(context.Background(), []contexty.Message{message})
		// Assert.
		require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
	}
}
