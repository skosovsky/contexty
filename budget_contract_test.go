package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_BudgetRetention(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []contexty.Message
		roles    []contexty.Role
		limit    int
	}{
		{name: "protected overflow", messages: []contexty.Message{{ID: "rule", Role: contexty.RoleSystem}}, roles: []contexty.Role{contexty.RoleSystem}, limit: 4},
		{name: "protected result protects round", messages: []contexty.Message{
			{ID: "call", Role: contexty.RoleAssistant, Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "one", Name: "read"}}},
			{ID: "result", Role: contexty.RoleTool, Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "one", Name: "read"}}},
			{ID: "user", Role: contexty.RoleUser},
		}, roles: []contexty.Role{contexty.RoleTool}, limit: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(tc.limit),
				Retention: contexty.RetentionPolicy{Roles: tc.roles}, TruncateStrategy: contexty.NewDropStrategy()},
				&contexty.FixedEstimator{TokensPerMessage: 5})
			// Act.
			result, err := pipe.Apply(context.Background(), tc.messages)
			// Assert.
			require.ErrorIs(t, err, contexty.ErrRetentionExceedsBudget)
			require.Empty(t, result.Messages)
		})
	}
}

func TestAcceptance_BudgetSummaryCapacity(t *testing.T) {
	for _, tailCount := range []int{1, 2} {
		t.Run(string(rune('0'+tailCount)), func(t *testing.T) {
			// Arrange: the same hard capacity leaves different summary capacity.
			messages := []contexty.Message{{ID: "a", Role: contexty.RoleUser}, {ID: "b", Role: contexty.RoleUser},
				{ID: "c", Role: contexty.RoleUser}, {ID: "d", Role: contexty.RoleUser}}
			calls := 0
			pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
				Budget: contexty.EffectiveInputBudget(15),
				Summarizer: summaryRequestFunc(
					func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
						calls++
						require.Equal(t, 15-tailCount*5, request.MaxTokens)
						require.Equal(t, request.MaxTokens, request.TargetTokens)
						request.Messages[0].ID = "mutated"
						return contexty.Message{ID: "summary", Role: contexty.RoleSystem}, nil
					},
				),
			}, &contexty.FixedEstimator{TokensPerMessage: 5}, contexty.WithRollingSummary(fixtureRollingPolicy(tailCount)))
			// Act.
			result, err := pipe.Apply(context.Background(), messages)
			// Assert.
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Equal(t, "a", messages[0].ID)
			require.True(t, result.Decision.Compacted)
			require.True(t, result.Decision.TargetReached)
			require.Len(t, result.Messages, tailCount+1)
		})
	}
}

func TestAcceptance_BudgetSoftTarget(t *testing.T) {
	// Arrange: fitting input triggers early compaction; the summary misses only the soft target.
	calls := 0
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget: contexty.EffectiveInputBudget(20),
		Compaction: &contexty.CompactionPolicy{
			Descriptor:     contexty.Descriptor{ID: "host/compress", Revision: "1"},
			TriggerPercent: 50,
			TargetPercent:  20,
		},
		Summarizer: summaryRequestFunc(
			func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
				calls++
				require.Equal(t, 20, request.MaxTokens)
				require.Equal(t, 4, request.TargetTokens)
				return contexty.Message{ID: "summary", Role: contexty.RoleSystem}, nil
			},
		),
	}, &contexty.FixedEstimator{TokensPerMessage: 5})
	messages := []contexty.Message{
		{ID: "a", Role: contexty.RoleUser},
		{ID: "b", Role: contexty.RoleUser},
		{ID: "c", Role: contexty.RoleUser},
	}
	// Act.
	result, err := pipe.Apply(context.Background(), messages)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.False(t, result.Decision.TargetReached)
	require.Equal(t, 5, result.Decision.AfterTokens)
}

type summaryRequestFunc func(context.Context, contexty.SummaryRequest) (contexty.Message, error)

func (f summaryRequestFunc) Summarize(ctx context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
	return f(ctx, request)
}

func TestAcceptance_BudgetRequiredContent(t *testing.T) {
	for _, summarize := range []bool{false, true} {
		t.Run(map[bool]string{false: "eviction", true: "summary"}[summarize], func(t *testing.T) {
			// Arrange: a required message lies between two evictable messages.
			messages := []contexty.Message{
				fixtureRollingText("a", "aaaa"),
				fixtureRollingText("rule", "keep"),
				fixtureRollingText("b", "bbbb"),
			}
			ref := fixtureRefForMessage(t, messages[1])
			cfg := contexty.BudgetConfig{
				Budget:    contexty.EffectiveInputBudget(8),
				Retention: contexty.RetentionPolicy{ContentRefs: []contexty.ContentRef{ref}},
				DropHead:  contexty.DropHeadConfig{MinMessages: 10},
			}
			if summarize {
				cfg.Summarizer = summaryRequestFunc(
					func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
						require.Equal(t, []string{"a", "b"}, []string{request.Messages[0].ID, request.Messages[1].ID})
						require.Equal(t, 4, request.MaxTokens)
						return fixtureRollingText("summary", "ok"), nil
					},
				)
			}
			pipe := contexty.NewBudgetPipeline(cfg, contexty.CharTokenEstimator{})
			// Act.
			result, err := pipe.Apply(context.Background(), messages)
			// Assert: MinMessages may drop the optional remainder, never the rule.
			require.NoError(t, err)
			require.Equal(t, []contexty.ContentRef{ref}, result.Decision.Required)
			require.Equal(t, messages[1], result.Messages[len(result.Messages)-1])
		})
	}
}

func TestAcceptance_BudgetRetainedIDCannotBeForged(t *testing.T) {
	for _, summarize := range []bool{false, true} {
		t.Run(map[bool]string{false: "eviction", true: "summary"}[summarize], func(t *testing.T) {
			// Arrange: the host callback tries to overwrite an excluded mandatory ID.
			messages := []contexty.Message{fixtureRollingText("a", "aaaa"), fixtureRollingText("rule", "ok")}
			forged := fixtureRollingText("rule", "x")
			cfg := contexty.BudgetConfig{
				Budget:           contexty.EffectiveInputBudget(4),
				Retention:        contexty.RetentionPolicy{MessageIDs: []string{"rule"}},
				TruncateStrategy: budgetCallbackEviction{output: []contexty.Message{forged}},
			}
			if summarize {
				cfg.Summarizer = summaryRequestFunc(
					func(context.Context, contexty.SummaryRequest) (contexty.Message, error) { return forged, nil },
				)
			}
			// Act.
			result, err := contexty.NewBudgetPipeline(cfg, contexty.CharTokenEstimator{}).
				Apply(context.Background(), messages)
			// Assert.
			require.ErrorIs(t, err, contexty.ErrInvalidRetention)
			require.Empty(t, result.Messages)
			require.Equal(t, "ok", messages[1].TextContent())
		})
	}
}

func TestAcceptance_BudgetSelectorsAndThresholds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retention contexty.RetentionPolicy
		policy    *contexty.CompactionPolicy
		want      error
	}{
		{name: "missing id", retention: contexty.RetentionPolicy{MessageIDs: []string{"missing"}}, want: contexty.ErrInvalidRetention},
		{name: "invalid ref", retention: contexty.RetentionPolicy{ContentRefs: []contexty.ContentRef{{ID: "a"}}}, want: contexty.ErrInvalidRetention},
		{name: "invalid trigger", policy: &contexty.CompactionPolicy{Descriptor: contexty.Descriptor{ID: "host", Revision: "1"}, TriggerPercent: 101, TargetPercent: 20}, want: contexty.ErrInvalidCompactionPolicy},
		{name: "target above trigger", policy: &contexty.CompactionPolicy{Descriptor: contexty.Descriptor{ID: "host", Revision: "1"}, TriggerPercent: 50, TargetPercent: 70}, want: contexty.ErrInvalidCompactionPolicy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			calls := 0
			cfg := contexty.BudgetConfig{
				Budget:     contexty.EffectiveInputBudget(20),
				Retention:  tc.retention,
				Compaction: tc.policy,
				Summarizer: summaryRequestFunc(
					func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
						calls++
						return fixtureRollingText("summary", "ok"), nil
					},
				),
			}
			// Act.
			result, err := contexty.NewBudgetPipeline(cfg, contexty.CharTokenEstimator{}).
				Apply(context.Background(), []contexty.Message{fixtureRollingText("a", "aaaa")})
			// Assert.
			require.ErrorIs(t, err, tc.want)
			require.Empty(t, result.Messages)
			require.Zero(t, calls)
		})
	}
}

func TestAcceptance_BudgetThresholdBoundaryAndZeroTarget(t *testing.T) {
	// Arrange: protected content alone exceeds the soft target, but not hard capacity.
	calls := 0
	policy := &contexty.CompactionPolicy{
		Descriptor:     contexty.Descriptor{ID: "host/compress", Revision: "1"},
		TriggerPercent: 50,
		TargetPercent:  20,
	}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:     contexty.EffectiveInputBudget(20),
			Compaction: policy,
			Retention:  contexty.RetentionPolicy{MessageIDs: []string{"keep"}},
			Summarizer: summaryRequestFunc(
				func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
					calls++
					require.Equal(t, 15, request.MaxTokens)
					require.Zero(t, request.TargetTokens)
					return fixtureRollingText("summary", "x"), nil
				},
			),
		},
		&contexty.FixedEstimator{TokensPerMessage: 5},
	)
	messages := []contexty.Message{fixtureRollingText("keep", "rule"), fixtureRollingText("a", "aaaa")}
	// Act.
	atBoundary, err := pipe.Apply(context.Background(), messages)
	// Assert: equality at the trigger does not compact.
	require.NoError(t, err)
	require.Zero(t, calls)
	require.False(t, atBoundary.Decision.Compacted)
	// Act: one additional message crosses the trigger while still fitting the hard limit.
	compressed, err := pipe.Apply(context.Background(), append(messages, fixtureRollingText("b", "bbbb")))
	// Assert: the unmet target is explicit and the mandatory message is intact.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.False(t, compressed.Decision.TargetReached)
	require.Equal(t, messages[0], compressed.Messages[0])
}

func TestAcceptance_BudgetSummaryHardOverflow(t *testing.T) {
	// Arrange: a summarizer ignores the hard capacity; no eviction fallback is allowed.
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:           contexty.EffectiveInputBudget(2),
			TruncateStrategy: contexty.NewDropStrategy(),
			Summarizer: summaryRequestFunc(
				func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
					require.Equal(t, 2, request.MaxTokens)
					return fixtureRollingText("summary", "oversized"), nil
				},
			),
		},
		contexty.CharTokenEstimator{},
	)
	// Act.
	result, err := pipe.Apply(context.Background(), []contexty.Message{fixtureRollingText("input", "long")})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Empty(t, result.Messages)
}

func TestAcceptance_BudgetRetentionReplayIdentity(t *testing.T) {
	// Arrange: recording does not require a summarization to pin retention configuration.
	roles := []contexty.Role{contexty.RoleSystem}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:    contexty.EffectiveInputBudget(100),
			Retention: contexty.RetentionPolicy{Roles: roles},
		},
		contexty.CharTokenEstimator{},
	)
	roles[0] = contexty.RoleUser
	compiled, err := fixtureTruncationEngine(
		pipe,
	).CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "retention"})
	require.NoError(t, err)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	expected.Budgets[0].Retention.Roles[0] = contexty.RoleUser
	// Act.
	_, err = contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	// Assert: caller mutation neither changes the configured policy nor accepted record.
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Equal(t, contexty.RoleSystem, compiled.Manifest.Budgets[0].Retention.Roles[0])
	require.NoError(t, accepted.Validate())
}

func TestAcceptance_BudgetSummaryReservations(t *testing.T) {
	for _, turnText := range []string{"turn", "longer"} {
		t.Run(turnText, func(t *testing.T) {
			// Arrange: output/wire, system, current turn, and rolling tail all reserve space.
			calls := 0
			capacity := 17 - 3 - len(turnText)
			pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
				Budget: contexty.WindowInputBudget(25, 5, 3),
				Compaction: &contexty.CompactionPolicy{
					Descriptor:     contexty.Descriptor{ID: "host/compress", Revision: "1"},
					TriggerPercent: 80,
					TargetPercent:  50,
				},
				Summarizer: summaryRequestFunc(
					func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
						calls++
						require.Equal(t, capacity-3, request.MaxTokens)
						require.Equal(t, max(0, 17/2-(3+len(turnText)+3)), request.TargetTokens)
						return fixtureRollingText("summary", "ok"), nil
					},
				),
			}, contexty.CharTokenEstimator{}, contexty.WithRollingSummary(fixtureRollingPolicy(1)))
			turn := contexty.NewCurrentTurn(fixtureRollingText("turn", turnText))
			engine := fixtureEngine(contexty.WithBudgetPipeline(pipe))
			request := contexty.CompileRequest{
				System: []contexty.Message{fixtureRollingText("sys", "sys")},
				History: []contexty.Message{
					fixtureRollingText("a", "aaaaaaaa"),
					fixtureRollingText("b", "bbbbbbbb"),
					fixtureRollingText("tail", "end"),
				},
				CurrentTurn: &turn,
			}
			// Act.
			compiled, err := engine.CompileSnapshot(context.Background(), request)
			// Assert: reservations are deducted once, and chronology is preserved.
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Equal(
				t,
				[]string{"summary", "tail", "turn"},
				[]string{
					compiled.Payload.History[0].ID,
					compiled.Payload.History[1].ID,
					compiled.Payload.History[2].ID,
				},
			)
			require.Equal(t, 17, compiled.BudgetDecisions[0].Decision.HardLimit)
		})
	}
}

func TestAcceptance_BudgetCompactionReplayIdentity(t *testing.T) {
	// Arrange: the policy is recorded even when history fits below its trigger.
	calls := 0
	policy := &contexty.CompactionPolicy{
		Descriptor:     contexty.Descriptor{ID: "host/compress", Revision: "1"},
		TriggerPercent: 80,
		TargetPercent:  50,
	}
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100), Compaction: policy,
			Summarizer: summaryRequestFunc(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
				calls++
				return fixtureRollingText("summary", "ok"), nil
			})},
		contexty.CharTokenEstimator{},
	)
	contexty.WithSummarizerDescriptor(contexty.Descriptor{ID: "host/summary", Revision: "1"})(pipe)
	policy.TargetPercent = 30
	compiled, err := fixtureTruncationEngine(
		pipe,
	).CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "policy"})
	require.NoError(t, err)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
	require.NoError(t, err)
	// Act: unchanged replay succeeds; changed thresholds invalidate that expectation.
	_, err = contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	expected.Budgets[0].CompactionPolicy.TargetPercent = 30
	_, err = contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
	// Assert: configuration is owned and replay calls no summarizer.
	require.ErrorIs(t, err, contexty.ErrReplayMismatch)
	require.Equal(t, 50, compiled.Manifest.Budgets[0].CompactionPolicy.TargetPercent)
	require.Zero(t, calls)
}

func TestAcceptance_BudgetAnonymousOptionalRetention(t *testing.T) {
	// Arrange: optional messages need no IDs; required content has a stable identity.
	messages := []contexty.Message{
		fixtureRollingText("keep", "rule"),
		contexty.TextMessage(
			contexty.RoleUser,
			"a",
		),
		contexty.TextMessage(contexty.RoleUser, "b"),
		contexty.TextMessage(contexty.RoleUser, "b"),
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget: contexty.EffectiveInputBudget(15),
		Retention: contexty.RetentionPolicy{
			MessageIDs: []string{"keep"},
		},
	}, &contexty.FixedEstimator{TokensPerMessage: 5})
	// Act.
	result, err := pipe.Apply(context.Background(), messages)
	// Assert: equal anonymous occurrences survive independently after the oldest is evicted.
	require.NoError(t, err)
	require.Equal(t, []contexty.Message{messages[0], messages[2], messages[3]}, result.Messages)
}

func TestAcceptance_BudgetRoundRetentionRequiresIdentity(t *testing.T) {
	// Arrange: explicit retention expands to an anonymous participant of an atomic round.
	messages := []contexty.Message{
		{ID: "keep", Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.ToolCallPart{ID: "call", Name: "read"}}},
		{
			Role:  contexty.RoleTool,
			Parts: []contexty.ContentPart{contexty.ToolResultPart{ToolCallID: "call", Name: "read"}},
		},
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget: contexty.EffectiveInputBudget(20),
		Retention: contexty.RetentionPolicy{
			MessageIDs: []string{"keep"},
		},
	}, &contexty.FixedEstimator{TokensPerMessage: 5})
	// Act.
	result, err := pipe.Apply(context.Background(), messages)
	// Assert: evidence cannot silently omit a required round participant.
	require.ErrorIs(t, err, contexty.ErrInvalidRetention)
	require.Empty(t, result.Messages)
}

func TestAcceptance_BudgetZeroSummaryCapacity(t *testing.T) {
	// Arrange: retained content consumes the entire hard capacity.
	calls := 0
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget:    contexty.EffectiveInputBudget(3),
		Retention: contexty.RetentionPolicy{MessageIDs: []string{"keep"}},
		Summarizer: summaryRequestFunc(
			func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
				calls++
				require.Zero(t, request.MaxTokens)
				require.Zero(t, request.TargetTokens)
				return fixtureRollingText("summary", ""), nil
			},
		),
	}, contexty.CharTokenEstimator{})
	messages := []contexty.Message{fixtureRollingText("old", "history"), fixtureRollingText("keep", "pin")}
	// Act.
	result, err := pipe.Apply(context.Background(), messages)
	// Assert: one empty result fits under this estimator and required content survives.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 3, result.Decision.AfterTokens)
	require.Equal(t, messages[1], result.Messages[1])
}
