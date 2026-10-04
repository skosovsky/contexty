package contexty_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Metadata_Isolation(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	msg := contexty.Message{
		Actor:       &contexty.Actor{Kind: "user", ID: "alice", DisplayName: "alice"},
		Role:        contexty.RoleUser,
		Parts:       []contexty.ContentPart{contexty.TextPart{Text: "hello"}},
		Annotations: contexty.Annotations{Timestamp: &ts},
		SourceRefs: []contexty.SourceRef{{
			Namespace: "messages",
			Kind:      "external",
			ID:        "msg-42",
		}},
	}
	assert.Equal(t, "hello", msg.TextContent())
	assert.Equal(t, "alice", msg.Actor.DisplayName)
	assert.NotContains(t, msg.TextContent(), "alice")

	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{msg})
	// Act.
	xml, err := contexty.Render(ctx, snap, contexty.ViewLLMXML)
	// Assert.
	require.NoError(t, err)
	flat, err := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
	require.NoError(t, err)
	assert.NotContains(t, xml, "alice")
	assert.NotContains(t, xml, "msg-42")
	assert.NotContains(t, flat, "alice")
	assert.NotContains(t, flat, "msg-42")

	store := contexty.NewMemoryConversationStateStore()
	s0, _ := loadState(ctx, store, "meta")
	require.NoError(
		t,
		updateSegment(
			ctx,
			store,
			"meta",
			s0.Version(),
			contexty.SegmentHistory,
			[]contexty.Message{msg},
		),
	)
	engine := contexty.NewEngine(contexty.WithConversationID("meta"), contexty.WithStateStore(store))
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	require.NoError(t, err)
	payload := result.Payload
	require.Len(t, payload.History, 1)
	assert.Equal(t, "hello", payload.History[0].TextContent())
	assert.Equal(t, "alice", payload.History[0].Actor.DisplayName)
	require.Len(t, payload.History[0].SourceRefs, 1)
	assert.Equal(t, "msg-42", payload.History[0].SourceRefs[0].ID)
}

func TestAcceptance_Unified_Budgeting(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	msgs := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "aaaaaaaaaaaaaaaa"),
		contexty.TextMessage(contexty.RoleUser, "bbbbbbbbbbbbbbbb"),
	}
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget: contexty.EffectiveInputBudget(15),
		Summarizer: stubSummarizer(
			func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
				return contexty.TextMessage(contexty.RoleSystem, "sum"), nil
			},
		),
	}, &contexty.FixedEstimator{TokensPerMessage: 10})
	// Act.
	outBudget, err := pipe.Apply(ctx, msgs)
	out := outBudget.Messages
	// Assert.
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "sum", out[0].TextContent())
}

func TestAcceptance_Polymorphic_DecodeStrict(t *testing.T) {
	// Arrange.
	_, err := contexty.UnmarshalParts([]byte(`[{"kind":"unknown","body":{}}]`))
	require.Error(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Contains(t, err.Error(), "unknown content part kind")
}

func TestAcceptance_Structural_Sharing(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	base := contexty.EmptySnapshot().WithSegment(contexty.SegmentSystem, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "immutable"),
	})
	derived := base.WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "new"),
	})
	assert.Equal(t, "immutable", base.Segment(contexty.SegmentSystem)[0].TextContent())
	assert.Equal(t, "new", derived.Segment(contexty.SegmentHistory)[0].TextContent())

	orig := base.WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "a@b.com"),
	})
	masked, err := contexty.NewRedactionHook().Transform(ctx, orig)
	require.NoError(t, err)
	assert.Equal(t, "a@b.com", orig.Segment(contexty.SegmentHistory)[0].TextContent())
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, "[REDACTED]", masked.Segment(contexty.SegmentHistory)[0].TextContent())
}

func TestAcceptance_Compile_Determinism(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	s0, _ := loadState(ctx, store, "det")
	require.NoError(
		t,
		updateSegment(ctx, store, "det", s0.Version(), contexty.SegmentHistory, []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "stable"),
		}),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("det"),
		contexty.WithStateStore(store),
	)
	req := contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "stable-1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "stable"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("k", "v"),
		},
	}
	// Act.
	r1, err := engine.Compile(ctx, req)
	// Assert.
	require.NoError(t, err)
	r2, err := engine.Compile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, r1.Payload, r2.Payload)
}

func TestAcceptance_CompileResult_ImmutableContract(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	pending := contexty.Message{
		ID:    "pending-1",
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "current turn"}},
	}
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1000)},
				&contexty.FixedEstimator{TokensPerMessage: 10},
			),
		),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{contexty.TextMessage(contexty.RoleUser, "past")},
		Pending: []contexty.Message{pending},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 2)
	assert.Equal(t, "pending-1", result.Payload.History[1].ID)
	assert.Equal(t, "current turn", result.Payload.History[1].TextContent())
	// Pending is merged into History; host must not patch Payload after compile.
}

func TestAcceptance_Segment_FormatterInjectedByHost(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	// Act.
	engine := contexty.NewEngine(
		contexty.WithSegmentFormatter(
			contexty.SegmentMemory,
			func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				out := make([]contexty.Message, len(msgs))
				for i, m := range msgs {
					m = m.Clone()
					m.Parts = []contexty.ContentPart{
						contexty.TextPart{Text: "<memory>" + m.TextContent() + "</memory>"},
					}
					out[i] = m
				}
				return out, nil
			},
		),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "m1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "fact"}},
		}},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.Memory, 1)
	assert.Contains(t, result.Payload.Memory[0].TextContent(), "<memory>")
}

func TestAcceptance_Pending_NeverEvicted(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:   contexty.EffectiveInputBudget(15),
			DropHead: contexty.DropHeadConfig{MinMessages: 1},
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	pending := contexty.Message{
		ID:    "pending-protected",
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "must stay"}},
	}
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "a"),
			contexty.TextMessage(contexty.RoleUser, "b"),
			contexty.TextMessage(contexty.RoleUser, "c"),
		},
		Pending: []contexty.Message{pending},
	})
	// Assert.
	require.NoError(t, err)
	last := result.Payload.History[len(result.Payload.History)-1]
	assert.Equal(t, "pending-protected", last.ID)
	assert.Equal(t, "must stay", last.TextContent())
	rec := result.Transformations["pending-protected"]
	assert.Equal(t, contexty.ActionPassed, rec.Final().Action)
	assert.Equal(t, contexty.ReasonProtectedPending, rec.Final().Reason)
}

func TestAcceptance_Pending_ExceedsBudget(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(50)},
				&contexty.FixedEstimator{TokensPerMessage: 30},
			),
		),
	)
	// Act.
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Pending: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "p1"),
			contexty.TextMessage(contexty.RoleUser, "p2"),
		},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrPendingExceedsBudget)
}

func TestAcceptance_Formatter_AffectsTokenBudget(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	basePipe := contexty.BudgetConfig{
		Budget:   contexty.EffectiveInputBudget(35),
		DropHead: contexty.DropHeadConfig{MinMessages: 1},
	}
	est := &contexty.FixedEstimator{TokensPerMessage: 10}
	memMsg := contexty.Message{
		ID: "mem", Role: contexty.RoleSystem,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "m"}},
	}
	req := contexty.CompileRequest{
		Memory: []contexty.Message{memMsg},
		History: []contexty.Message{
			{
				ID:    "h1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "1"}},
			},
			{
				ID:    "h2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "2"}},
			},
			{
				ID:    "h3",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "3"}},
			},
		},
	}
	// Act.
	without, err := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(basePipe, est),
		),
	).CompileSnapshot(ctx, req)
	// Assert.
	require.NoError(t, err)

	expandFormatter := func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
		extra := contexty.Message{
			ID:    "mem-extra",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "extra reserved tokens"}},
		}
		return append(cloneMsgs(msgs), extra), nil
	}
	with, err := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(basePipe, est),
		),
		contexty.WithSegmentFormatter(contexty.SegmentMemory, expandFormatter),
	).CompileSnapshot(ctx, req)
	require.NoError(t, err)
	assert.Less(t, len(with.Payload.History), len(without.Payload.History))
}

func TestAcceptance_Formatter_SameIDRecordsFormatted(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	// Act.
	engine := contexty.NewEngine(
		contexty.WithSegmentFormatter(
			contexty.SegmentMemory,
			func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				m := msgs[0].Clone()
				m.Parts = []contexty.ContentPart{contexty.TextPart{Text: "rewritten"}}
				return []contexty.Message{m}, nil
			},
		),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-same",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "before"}},
		}},
	})
	// Assert.
	require.NoError(t, err)
	rec, ok := result.Transformations["mem-same"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionFormatted, rec.Final().Action)
	assert.Equal(t, contexty.ReasonSegmentFormatter, rec.Final().Reason)
}

func TestAcceptance_Compile_SnapshotSelfContained(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	_, _ = loadState(ctx, store, "ignored")
	engine := contexty.NewEngine(
		contexty.WithStateStore(store),
		contexty.WithConversationID("ignored"),
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1000)},
				&contexty.FixedEstimator{TokensPerMessage: 10},
			),
		),
	)
	tools := []contexty.Message{{
		ID: "tool-1", Role: contexty.RoleTool,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "result"}},
	}}
	pending := []contexty.Message{{
		ID: "pend-1", Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "now"}},
	}}
	hist := contexty.Message{
		ID: "h1", Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "hist"}},
	}
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{hist},
		Tools:   tools,
		Pending: pending,
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.Tools, 1)
	assert.Equal(t, "tool-1", result.Payload.Tools[0].ID)
	require.Len(t, result.Payload.History, 2)
	assert.Equal(t, "pend-1", result.Payload.History[1].ID)
}

func TestAcceptance_Pending_HistoryIDCollision(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	engine := contexty.NewEngine()
	dup := contexty.Message{
		ID:    "dup-id",
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
	}
	// Act.
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{dup},
		Pending: []contexty.Message{dup.Clone()},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrDuplicateMessageID)
}

func TestAcceptance_Duplicate_MessageIDWithinHistory(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	engine := contexty.NewEngine()
	// Act.
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "dup",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "a"}},
			},
			{
				ID:    "dup",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "b"}},
			},
		},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrDuplicateMessageID)
}

func TestAcceptance_Strict_SystemExceedsBudget(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(15)},
				&contexty.FixedEstimator{TokensPerMessage: 20},
			),
		),
	)
	// Act.
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "large system block"),
		},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
}

func TestAcceptance_Formatter_ReplacedByFormatter(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithSegmentFormatter(
			contexty.SegmentMemory,
			func(context.Context, []contexty.Message) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "mem-new",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "new"}},
				}}, nil
			},
		),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-old",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}},
		}},
	})
	// Assert.
	require.NoError(t, err)
	oldRec, ok := result.Transformations["mem-old"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionFormatted, oldRec.Final().Action)
	assert.Equal(t, contexty.ReasonReplacedByFormatter, oldRec.Final().Reason)
	newRec, ok := result.Transformations["mem-new"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionPassed, newRec.Final().Action)
}

func TestAcceptance_Summarize_TransformationByMessageID(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget: contexty.EffectiveInputBudget(25),
			Summarizer: stubSummarizer(
				func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
					return contexty.Message{
						ID:    "summary-1",
						Role:  contexty.RoleSystem,
						Parts: []contexty.ContentPart{contexty.TextPart{Text: "sum"}},
					}, nil
				},
			),
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "h-a",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "a"}},
			},
			{
				ID:    "h-b",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "b"}},
			},
			{
				ID:    "h-c",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "c"}},
			},
		},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 1)
	assert.Equal(t, "summary-1", result.Payload.History[0].ID)
	for _, id := range []string{"h-a", "h-b", "h-c"} {
		rec, ok := result.Transformations[id]
		require.True(t, ok, "missing %s", id)
		assert.Equal(t, contexty.ActionTruncated, rec.Final().Action)
	}
	sumRec, ok := result.Transformations["summary-1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionPassed, sumRec.Final().Action)
}

func TestAcceptance_Merge_PolicyReplaceByOrigin(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "persona",
			Segment:     contexty.SegmentSystem,
			MergePolicy: contexty.PolicyReplaceByOrigin,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "new-persona",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "updated persona"}},
					Origin: &contexty.MessageOrigin{
						TemplateID: "agents/sales",
						LayerID:    "persona-v2",
					},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{{
			ID:     "old-persona",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "old persona"}},
			Origin: &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona-v1"},
		}},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.System, 1)
	assert.Equal(t, "updated persona", result.Payload.System[0].TextContent())
}

func TestAcceptance_Merge_PolicyDeduplicateByLayer(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "layer-dedup",
			Segment:     contexty.SegmentMemory,
			MergePolicy: contexty.PolicyDeduplicateByLayer,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:     "mem-new",
					Role:   contexty.RoleSystem,
					Parts:  []contexty.ContentPart{contexty.TextPart{Text: "fresh layer"}},
					Origin: &contexty.MessageOrigin{TemplateID: "t2", LayerID: "facts"},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:     "mem-old",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "stale layer"}},
			Origin: &contexty.MessageOrigin{TemplateID: "t1", LayerID: "facts"},
		}},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.Memory, 1)
	assert.Equal(t, "fresh layer", result.Payload.Memory[0].TextContent())
}

func TestAcceptance_Merge_PolicyAppendDefault(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "append",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "mem-2",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "second"}},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "first"}},
		}},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.Memory, 2)
}

func TestAcceptance_MergePolicyReplaceByOrigin_PersistenceProjection(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "persona",
			Segment:     contexty.SegmentSystem,
			MergePolicy: contexty.PolicyReplaceByOrigin,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "new-persona",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "updated persona"}},
					Origin: &contexty.MessageOrigin{
						TemplateID: "agents/sales",
						LayerID:    "persona-v2",
					},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{{
			ID:     "old-persona",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "old persona"}},
			Origin: &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona-v1"},
		}},
	})
	// Assert.
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentSystem)
	require.Len(t, proj, 1)
	assert.Equal(t, "new-persona", proj[0].ID)
	oldRec, ok := result.Transformations["old-persona"]
	require.True(t, ok)
	assert.Equal(t, contexty.ReasonReplacedByDeferred, oldRec.Final().Reason)
}

func TestAcceptance_MergePolicyDeduplicateByLayer_PersistenceProjection(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "layer-dedup",
			Segment:     contexty.SegmentMemory,
			MergePolicy: contexty.PolicyDeduplicateByLayer,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:     "mem-new",
					Role:   contexty.RoleSystem,
					Parts:  []contexty.ContentPart{contexty.TextPart{Text: "fresh layer"}},
					Origin: &contexty.MessageOrigin{TemplateID: "t2", LayerID: "facts"},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:     "mem-old",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "stale layer"}},
			Origin: &contexty.MessageOrigin{TemplateID: "t1", LayerID: "facts"},
		}},
	})
	// Assert.
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "mem-new", proj[0].ID)
	oldRec, ok := result.Transformations["mem-old"]
	require.True(t, ok)
	assert.Equal(t, contexty.ReasonReplacedByDeferred, oldRec.Final().Reason)
}

func TestAcceptance_IntroducedBaseline_DeferredAndHook(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "contact",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:   "mem-deferred",
					Role: contexty.RoleSystem,
					Parts: []contexty.ContentPart{
						contexty.TextPart{Text: "contact me@example.com"},
					},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	require.Contains(t, result.Introduced, "mem-deferred")
	assert.Equal(t, "contact me@example.com", result.Introduced["mem-deferred"].TextContent())
}

func TestAcceptance_MergePolicyReplaceByOrigin_SkipsMessagesWithoutOrigin(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "no-origin",
			Segment:     contexty.SegmentSystem,
			MergePolicy: contexty.PolicyReplaceByOrigin,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "incoming-no-origin",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "incoming"}},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{{
			ID:     "existing-with-origin",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "existing"}},
			Origin: &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona"},
		}},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.System, 2)
	proj := result.DerivePersistenceProjection(contexty.SegmentSystem)
	require.Len(t, proj, 2)
	assert.Equal(t, "existing-with-origin", proj[0].ID)
	assert.Equal(t, "incoming-no-origin", proj[1].ID)
}

func TestAcceptance_Recorder_StructuralFormattedNotOverwrittenByPassed(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "persona",
			Segment:     contexty.SegmentSystem,
			MergePolicy: contexty.PolicyReplaceByOrigin,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "new-persona",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "updated persona"}},
					Origin: &contexty.MessageOrigin{
						TemplateID: "agents/sales",
						LayerID:    "persona-v2",
					},
				}}}, nil
			},
		}),
		contexty.WithTransformHooks(reintroduceSystemMessageHook{
			id:   "old-persona",
			role: contexty.RoleSystem,
			text: "reintroduced by hook",
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{{
			ID:     "old-persona",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "old persona"}},
			Origin: &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona-v1"},
		}},
	})
	// Assert.
	require.NoError(t, err)
	oldRec, ok := result.Transformations["old-persona"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionFormatted, oldRec.Final().Action)
	assert.Equal(t, contexty.ReasonReplacedByDeferred, oldRec.Final().Reason)
	proj := result.DerivePersistenceProjection(contexty.SegmentSystem)
	require.Len(t, proj, 1)
	assert.Equal(t, "new-persona", proj[0].ID)
}

func TestAcceptance_Introduced_BaselineBeforeHooksAndPatches(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "facts",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:   "mem-deferred",
					Role: contexty.RoleSystem,
					Parts: []contexty.ContentPart{
						contexty.TextPart{Text: "contact me@example.com"},
					},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-src",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "source"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentMemory, MessageID: "mem-src", Text: "PATCHED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	require.Contains(t, result.Introduced, "mem-deferred")
	assert.Equal(t, "contact me@example.com", result.Introduced["mem-deferred"].TextContent())
	assert.Equal(t, "PATCHED", result.Payload.Memory[0].TextContent())
	assert.Contains(t, result.Payload.Memory[1].TextContent(), "[REDACTED]")
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 2)
	assert.Equal(t, "source", proj[0].TextContent())
	assert.Equal(t, "contact me@example.com", proj[1].TextContent())
}

func TestAcceptance_Message_CodecRoundTripsTypedExtensions(t *testing.T) {
	// Arrange.
	t.Parallel()
	codec := contexty.MessageCodec{
		Provenance: contexty.DefaultProvenanceRegistry(),
		Extensions: newTestExtensionRegistry(),
	}
	msg := contexty.Message{
		ID:    "with-extension",
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "typed"}},
		Extensions: []contexty.Extension{
			testExtension{Tenant: "acme", Score: 7},
		},
	}

	data, err := contexty.MarshalMessageJSON(msg, codec)
	require.NoError(t, err)
	decoded, err := contexty.UnmarshalMessageJSON(data, codec)
	require.NoError(t, err)

	require.Len(t, decoded.Extensions, 1)
	ext, ok := decoded.Extensions[0].(testExtension)
	require.True(t, ok)
	assert.Equal(t, "acme", ext.Tenant)
	// Act / Assert: exercise the contract and check its result.
	assert.InEpsilon(t, float64(7), ext.Score, 0)
}

func TestAcceptance_Actor_ProjectionAndSourceRefsRoundTrip(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	msg := contexty.Message{
		ID:   "domain-speaker",
		Role: contexty.RoleUser,
		Actor: &contexty.Actor{
			Kind:        "alert",
			ID:          "actor-1",
			DisplayName: "System alert",
			SourceRefs: []contexty.SourceRef{{
				Namespace:    "source",
				Kind:         "event",
				ID:           "evt-1",
				CheckpointID: "chk-1",
			}},
		},
		SourceRefs: []contexty.SourceRef{{
			Namespace:    "messages",
			Kind:         "external",
			ID:           "msg-1",
			CheckpointID: "stable-1",
			URI:          "urn:message:msg-1",
		}},
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "alert text"}},
	}
	codec := contexty.ConversationCodec{Provenance: contexty.DefaultProvenanceRegistry()}
	// Act.
	data, err := codec.Encode(
		contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{msg}),
	)
	// Assert.
	require.NoError(t, err)
	decoded, err := codec.Decode(data)
	require.NoError(t, err)
	got := decoded.Segment(contexty.SegmentHistory)[0]
	require.NotNil(t, got.Actor)
	assert.Equal(t, "alert", got.Actor.Kind)
	require.Len(t, got.SourceRefs, 1)
	assert.Equal(t, "stable-1", got.SourceRefs[0].CheckpointID)

	engine := contexty.NewEngine(
		contexty.WithRoleProjectionPolicy(
			contexty.RoleProjectionFunc(func(msg contexty.Message) (contexty.Role, error) {
				if msg.Actor != nil && msg.Actor.Kind == "alert" {
					return contexty.RoleSystem, nil
				}
				return msg.Role, nil
			}),
		),
	)
	result, err := engine.CompileSnapshot(
		ctx,
		contexty.CompileRequest{History: []contexty.Message{msg}},
	)
	require.NoError(t, err)
	assert.Equal(t, contexty.RoleSystem, result.Payload.History[0].Role)
	assert.Equal(t, contexty.RoleUser, result.Source.History[0].Role)
	assert.Equal(t, contexty.RoleUser, msg.Role)
}

func TestAcceptance_Context_AwareFormatterReceivesContextAndPropagatesError(t *testing.T) {
	// Arrange.
	t.Parallel()
	expectedErr := errors.New("formatter failed")
	ctx := context.WithValue(context.Background(), formatterContextKey{}, "trace-1")
	engine := contexty.NewEngine(
		contexty.WithSegmentFormatter(
			contexty.SegmentMemory,
			func(ctx context.Context, _ []contexty.Message) ([]contexty.Message, error) {
				assert.Equal(t, "trace-1", ctx.Value(formatterContextKey{}))
				return nil, expectedErr
			},
		),
	)

	// Act.
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{contexty.TextMessage(contexty.RoleSystem, "memory")},
	})
	// Assert.
	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
}

func TestAcceptance_Context_ArtifactsLifecycleAndBudgetPreflight(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	visible := contexty.NewRetrievalDocument(
		"visible",
		contexty.TextPayload("visible artifact"),
	).ContextArtifact
	visible.BoundTurnID = "turn-1"
	hidden := contexty.NewRetrievalDocument(
		"hidden",
		contexty.TextPayload("hidden artifact"),
	).ContextArtifact
	hidden.BoundTurnID = "turn-2"
	unbound := contexty.NewRetrievalDocument(
		"unbound",
		contexty.TextPayload("unbound retrieval"),
	).ContextArtifact
	persistent := contexty.NewMemoryBlock(
		"persistent",
		contexty.TextPayload("persistent artifact"),
	).ContextArtifact
	oversized := contexty.NewMemoryBlock(
		"oversized",
		contexty.TextPayload("this artifact is too large"),
	).ContextArtifact.WithBudget(contexty.ArtifactBudgetPolicy{TokenLimit: 3})

	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{
					Budget:   contexty.EffectiveInputBudget(30),
					DropHead: contexty.DropHeadConfig{MinMessages: 1},
				},
				&contexty.FixedEstimator{TokensPerMessage: 10},
			),
		),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		TurnID:    "turn-1",
		Artifacts: []contexty.ContextArtifact{visible, hidden, unbound, persistent, oversized},
		History: []contexty.Message{
			{
				ID:    "old",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}},
			},
			{
				ID:    "new",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "new"}},
			},
		},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 2)
	assert.ElementsMatch(t, []string{"visible", "persistent"}, artifactIDs(result.Artifacts))
	memoryText := flattenText(result.Payload.Memory)
	assert.Contains(t, memoryText, "visible artifact")
	assert.Contains(t, memoryText, "persistent artifact")
	assert.NotContains(t, memoryText, "hidden artifact")
	assert.NotContains(t, memoryText, "unbound retrieval")
	assert.NotContains(t, memoryText, "this artifact is too large")
	assert.Len(t, result.Payload.History, 1)
	assert.Equal(t, "new", result.Payload.History[0].ID)
}

func TestAcceptance_Context_ArtifactPersistenceAndMergePolicies(t *testing.T) {
	// Arrange.
	t.Parallel()
	left := contexty.NewMemoryBlock(
		"merge-1",
		contexty.TextPayload("left"),
	).ContextArtifact
	right := contexty.NewMemoryBlock(
		"merge-1",
		contexty.TextPayload("right"),
	).ContextArtifact
	right.MergePolicy = contexty.PolicyAppend

	state, err := contexty.ApplyDeltas(
		contexty.EmptyState(),
		contexty.ConversationDelta{Operation: contexty.DeltaUpsertArtifact, Artifact: &left},
		contexty.ConversationDelta{Operation: contexty.DeltaUpsertArtifact, Artifact: &right},
	)
	require.NoError(t, err)
	require.Len(t, state.Artifacts(), 1)
	assert.Equal(t, "left\nright", state.Artifacts()[0].Payload.PlainText())

	storeArtifact := contexty.NewMemoryBlock(
		"store",
		contexty.TextPayload("store"),
	).ContextArtifact.WithPersistence(contexty.ArtifactPersistenceStore).WithOwner(contexty.SourceRef{
		Namespace: "tenant",
		Kind:      "workspace",
		ID:        "workspace-1",
	})
	skipArtifact := contexty.NewMemoryBlock(
		"skip",
		contexty.TextPayload("skip"),
	).ContextArtifact.WithPersistence(contexty.ArtifactPersistenceSkip)
	turnDefault := contexty.NewRetrievalDocument(
		"turn-default",
		contexty.TextPayload("turn default"),
	).ContextArtifact.WithTurn("turn-1")
	turnStored := contexty.NewRetrievalDocument(
		"turn-stored",
		contexty.TextPayload("turn stored"),
	).ContextArtifact.WithTurn("turn-1").WithPersistence(contexty.ArtifactPersistenceStore)
	ephemeral := contexty.NewMemoryBlock(
		"ephemeral",
		contexty.TextPayload("ephemeral"),
	).ContextArtifact
	ephemeral.Lifecycle = contexty.ArtifactLifecycleEphemeral

	codec := contexty.ConversationStateCodec{Provenance: contexty.DefaultProvenanceRegistry()}
	projected, err := contexty.ProjectCheckpoint(
		contexty.EmptyState().WithArtifacts([]contexty.ContextArtifact{
			storeArtifact,
			skipArtifact,
			turnDefault,
			turnStored,
			ephemeral,
		}),
	)
	require.NoError(t, err)
	data, err := codec.EncodeState(projected)
	require.NoError(t, err)
	decoded, err := codec.DecodeState(data)
	require.NoError(t, err)
	assert.Equal(t, []string{"store", "turn-stored"}, artifactIDs(decoded.Artifacts()))
	require.NotNil(t, decoded.Artifacts()[0].OwnerRef)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, "workspace-1", decoded.Artifacts()[0].OwnerRef.ID)
}

func TestAcceptance_CurrentTurn_PromptProjectionAndPersistence(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	raw := contexty.Message{
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "raw secret text"}},
	}
	promptSafe := contexty.Message{
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "safe text"}},
	}
	turn := contexty.NewCurrentTurn(raw).WithPromptSafe(promptSafe)

	// Act.
	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		TurnID: "turn-1",
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		CurrentTurn:            &turn,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	// Assert.
	require.NoError(t, err)

	require.Len(t, result.Payload.History, 2)
	assert.Equal(t, "safe text", result.Payload.History[1].TextContent())
	require.NotNil(t, result.Source.CurrentTurn)
	assert.Equal(t, "raw secret text", result.Source.CurrentTurn.Raw.TextContent())
	assert.Equal(t, result.Source.CurrentTurn.Raw.ID, result.Payload.History[1].ID)
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionFormatted,
		Reason: contexty.ReasonCurrentTurnProjection,
	}, result.Transformations[result.Source.CurrentTurn.Raw.ID].Final())

	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 2)
	assert.Equal(t, "h1", proj[0].ID)
	assert.Equal(t, "raw secret text", proj[1].TextContent())
	assert.NotContains(t, proj[1].TextContent(), "safe text")
	writebackHistory := result.Writeback.Snapshot.Segment(contexty.SegmentHistory)
	require.Len(t, writebackHistory, 2)
	assert.Equal(t, "raw secret text", writebackHistory[1].TextContent())
}

func TestAcceptance_CurrentTurn_CanPersistPromptSafeText(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	raw := contexty.TextMessage(contexty.RoleUser, "raw text")
	promptSafe := contexty.TextMessage(contexty.RoleUser, "redacted text")
	turn := contexty.NewCurrentTurn(raw).
		WithPromptSafe(promptSafe).
		WithPersistence(contexty.CurrentTurnPersistPromptSafe)

	// Act.
	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		CurrentTurn:            &turn,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	// Assert.
	require.NoError(t, err)

	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "redacted text", proj[0].TextContent())
	writebackHistory := result.Writeback.Snapshot.Segment(contexty.SegmentHistory)
	require.Len(t, writebackHistory, 1)
	assert.Equal(t, "redacted text", writebackHistory[0].TextContent())
}

func TestAcceptance_CurrentTurn_CanSkipPersistenceAndRejectInvalidPolicy(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	turn := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "raw text")).
		WithPersistence(contexty.CurrentTurnPersistNone)

	// Act.
	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		CurrentTurn:            &turn,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	// Assert.
	require.NoError(t, err)
	assert.Empty(t, result.DerivePersistenceProjection(contexty.SegmentHistory))
	assert.Empty(t, result.Writeback.Snapshot.Segment(contexty.SegmentHistory))

	invalid := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "raw text")).
		WithPersistence(contexty.CurrentTurnPersistencePolicy("persist_promt_safe"))
	_, err = contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		CurrentTurn:            &invalid,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.ErrorIs(t, err, contexty.ErrInvalidCurrentTurnPersistencePolicy)
}

func TestAcceptance_DurableIdentity_PolicyAndWritebackIntent(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	msg := contexty.TextMessage(contexty.RoleUser, "needs durable id")

	// Act.
	_, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History:                []contexty.Message{msg},
		RequireDurableIdentity: true,
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingIdentityPolicy)

	policy := contexty.NewStableMessageIdentityPolicy("stable")
	first, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History:                []contexty.Message{msg},
		IdentityPolicy:         policy,
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)
	second, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History:                []contexty.Message{msg},
		IdentityPolicy:         policy,
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)

	require.Len(t, first.Writeback.Messages, 1)
	assert.Equal(t, contexty.SegmentHistory, first.Writeback.Messages[0].Segment)
	assert.Equal(t, first.Payload.History[0].ID, first.Writeback.Messages[0].ID)
	assert.Equal(t, first.Payload.History[0].ID, first.NormalizedSnapshot.Segment(contexty.SegmentHistory)[0].ID)
	assert.Equal(t, first.Payload.History[0].ID, second.Payload.History[0].ID)
}

func TestAcceptance_Public_NormalizeFailsClosedAndReturnsWritebacks(t *testing.T) {
	// Arrange.
	t.Parallel()
	msg := contexty.TextMessage(contexty.RoleUser, "needs durable id")

	// Act.
	_, _, err := (contexty.CompileRequest{
		History:                []contexty.Message{msg},
		RequireDurableIdentity: true,
	}).Normalize()
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingIdentityPolicy)

	normalized, writebacks, err := (contexty.CompileRequest{
		TurnID:                 "turn-1",
		History:                []contexty.Message{msg},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	}).Normalize()
	require.NoError(t, err)
	require.Len(t, normalized.History, 1)
	require.NotEmpty(t, normalized.History[0].ID)
	require.Len(t, writebacks, 1)
	assert.Equal(t, normalized.History[0].ID, writebacks[0].ID)
	assert.Equal(t, contexty.SegmentHistory, writebacks[0].Segment)
}

func TestAcceptance_DurableIdentity_UsesHistoryOffsetForPending(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	msg := contexty.TextMessage(contexty.RoleUser, "same content")

	// Act.
	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History:                []contexty.Message{msg},
		Pending:                []contexty.Message{msg},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 2)
	assert.NotEqual(t, result.Payload.History[0].ID, result.Payload.History[1].ID)
	require.Len(t, result.Writeback.Messages, 2)
	assert.Equal(t, 0, result.Writeback.Messages[0].Index)
	assert.Equal(t, 1, result.Writeback.Messages[1].Index)
}

func TestAcceptance_Hook_IntroducedIDsRespectDurableIdentity(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	hook := fixtureTransformHook{
		fn: func(
			_ context.Context,
			snap contexty.ConversationSnapshot,
		) (contexty.ConversationSnapshot, error) {
			history := snap.Segment(contexty.SegmentHistory)
			history = append(history, contexty.TextMessage(contexty.RoleUser, "hook introduced"))
			return snap.WithSegment(contexty.SegmentHistory, history), nil
		},
	}

	// Act.
	_, err := contexty.NewEngine(contexty.WithTransformHooks(hook)).CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		RequireDurableIdentity: true,
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingIdentityPolicy)

	first, err := contexty.NewEngine(contexty.WithTransformHooks(hook)).CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)
	second, err := contexty.NewEngine(contexty.WithTransformHooks(hook)).CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)
	require.Len(t, first.Payload.History, 2)
	require.NotEmpty(t, first.Payload.History[1].ID)
	assert.Equal(t, first.Payload.History[1].ID, second.Payload.History[1].ID)
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionFormatted,
		Reason: contexty.ReasonTransformHook,
	}, first.Transformations[first.Payload.History[1].ID].Final())
}

func TestAcceptance_Typed_ArtifactCodecRoundTrip(t *testing.T) {
	// Arrange.
	t.Parallel()
	type retrievedFact struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	desc := contexty.ArtifactCodecDescriptor[retrievedFact]{
		TypeID:      "test.retrieved_fact",
		Kind:        contexty.ArtifactKindRetrievalDocument,
		Lifecycle:   contexty.ArtifactLifecyclePersistent,
		BoundTurnID: "",
		OwnerRef: &contexty.SourceRef{
			Namespace:    "owner",
			Kind:         "workspace",
			ID:           "workspace-1",
			CheckpointID: "",
			URI:          "",
		},
		SourceRefs: []contexty.SourceRef{{
			Namespace:    "source",
			Kind:         "document",
			ID:           "doc-1",
			CheckpointID: "doc-1:v1",
			URI:          "",
		}},
		MergePolicy: contexty.PolicyReplaceByOrigin,
		Budget: &contexty.ArtifactBudgetPolicy{
			TokenLimit: 200,
		},
		Persistence: contexty.ArtifactPersistenceStore,
		Render: func(v retrievedFact) string {
			return v.Title + ": " + v.Body
		},
	}
	artifact, err := contexty.NewTypedArtifact(
		"fact-1",
		desc,
		retrievedFact{Title: "Contract", Body: "typed codec"},
	)
	require.NoError(t, err)
	require.NotNil(t, artifact.OwnerRef)
	assert.Equal(t, "workspace-1", artifact.OwnerRef.ID)
	require.Len(t, artifact.SourceRefs, 1)
	assert.Equal(t, "doc-1", artifact.SourceRefs[0].ID)
	assert.Equal(t, contexty.ArtifactLifecyclePersistent, artifact.Lifecycle)
	assert.Equal(t, contexty.PolicyReplaceByOrigin, artifact.MergePolicy)
	require.NotNil(t, artifact.Budget)
	assert.Equal(t, 200, artifact.Budget.TokenLimit)
	assert.Equal(t, contexty.ArtifactPersistenceStore, artifact.Persistence)

	codec := contexty.ConversationCodec{}
	// Act.
	data, err := codec.Encode(contexty.EmptySnapshot().WithArtifacts([]contexty.ContextArtifact{artifact}))
	// Assert.
	require.NoError(t, err)
	decodedSnapshot, err := codec.Decode(data)
	require.NoError(t, err)
	require.Len(t, decodedSnapshot.Artifacts(), 1)
	decodedArtifact := decodedSnapshot.Artifacts()[0]
	assert.Equal(t, "test.retrieved_fact", decodedArtifact.ArtifactType)
	require.NotNil(t, decodedArtifact.OwnerRef)
	assert.Equal(t, "workspace-1", decodedArtifact.OwnerRef.ID)
	require.Len(t, decodedArtifact.SourceRefs, 1)
	assert.Equal(t, "doc-1", decodedArtifact.SourceRefs[0].ID)
	assert.Equal(t, contexty.PolicyReplaceByOrigin, decodedArtifact.MergePolicy)
	require.NotNil(t, decodedArtifact.Budget)

	value, err := contexty.DecodeTypedArtifact[retrievedFact](decodedArtifact, desc)
	require.NoError(t, err)
	assert.Equal(t, "Contract", value.Title)

	registry := contexty.NewArtifactCodecRegistry()
	registry.Register(contexty.NewTypedArtifactCodec(desc))
	decodedAny, err := registry.Decode(decodedArtifact)
	require.NoError(t, err)
	assert.Equal(t, retrievedFact{Title: "Contract", Body: "typed codec"}, decodedAny)
}

func TestAcceptance_Typed_ArtifactReplaceByOriginUsesSourceMetadata(t *testing.T) {
	// Arrange.
	t.Parallel()
	type retrievedFact struct {
		Body string `json:"body"`
	}
	desc := contexty.ArtifactCodecDescriptor[retrievedFact]{
		TypeID:    "test.retrieved_fact",
		Kind:      contexty.ArtifactKindRetrievalDocument,
		Lifecycle: contexty.ArtifactLifecyclePersistent,
		OwnerRef: &contexty.SourceRef{
			Namespace: "owner",
			Kind:      "workspace",
			ID:        "workspace-1",
		},
		SourceRefs: []contexty.SourceRef{{
			Namespace: "source",
			Kind:      "document",
			ID:        "doc-1",
		}},
		MergePolicy: contexty.PolicyReplaceByOrigin,
		Render: func(v retrievedFact) string {
			return v.Body
		},
	}
	oldArtifact, err := contexty.NewTypedArtifact("fact-old", desc, retrievedFact{Body: "old"})
	require.NoError(t, err)
	newArtifact, err := contexty.NewTypedArtifact("fact-new", desc, retrievedFact{Body: "new"})
	require.NoError(t, err)

	state := contexty.EmptySnapshot().WithArtifact(oldArtifact).WithArtifact(newArtifact)
	artifacts := state.Artifacts()
	require.Len(t, artifacts, 1)
	assert.Equal(t, "fact-new", artifacts[0].ID)
	assert.Equal(t, "new", artifacts[0].Payload.PlainText())

	// Act.
	result, err := contexty.NewEngine().CompileSnapshot(context.Background(), contexty.CompileRequest{
		Artifacts: []contexty.ContextArtifact{oldArtifact, newArtifact},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 1)
	assert.Equal(t, "fact-new", result.Artifacts[0].ID)
	writebackArtifacts := result.Writeback.Snapshot.Artifacts()
	require.Len(t, writebackArtifacts, 1)
	assert.Equal(t, "fact-new", writebackArtifacts[0].ID)
	normalizedArtifacts := result.NormalizedSnapshot.Artifacts()
	require.Len(t, normalizedArtifacts, 1)
	assert.Equal(t, "fact-new", normalizedArtifacts[0].ID)
}

func TestAcceptance_Compile_MergesStoreAndRequestArtifactsBeforeValidation(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	type retrievedFact struct {
		Body string `json:"body"`
	}
	desc := contexty.ArtifactCodecDescriptor[retrievedFact]{
		TypeID:    "test.retrieved_fact",
		Kind:      contexty.ArtifactKindRetrievalDocument,
		Lifecycle: contexty.ArtifactLifecyclePersistent,
		SourceRefs: []contexty.SourceRef{{
			Namespace: "source",
			Kind:      "document",
			ID:        "doc-1",
		}},
		MergePolicy: contexty.PolicyReplaceByOrigin,
		Render: func(v retrievedFact) string {
			return v.Body
		},
	}
	stored, err := contexty.NewTypedArtifact("fact-1", desc, retrievedFact{Body: "old"})
	require.NoError(t, err)
	incoming, err := contexty.NewTypedArtifact("fact-1", desc, retrievedFact{Body: "new"})
	require.NoError(t, err)
	store := contexty.NewMemoryConversationStateStore()
	require.NoError(t, store.CommitState(ctx, "chat-1", 0, contexty.ConversationDelta{
		Operation: contexty.DeltaUpsertArtifact,
		Artifact:  &stored,
	}))

	// Act.
	result, err := contexty.NewEngine(
		contexty.WithStateStore(store),
		contexty.WithConversationID("chat-1"),
	).Compile(ctx, contexty.CompileRequest{
		Artifacts: []contexty.ContextArtifact{incoming},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 1)
	assert.Equal(t, "fact-1", result.Artifacts[0].ID)
	assert.Equal(t, "new", result.Artifacts[0].Payload.PlainText())
	writebackArtifacts := result.Writeback.Snapshot.Artifacts()
	require.Len(t, writebackArtifacts, 1)
	assert.Equal(t, "new", writebackArtifacts[0].Payload.PlainText())
}

func TestAcceptance_Multi_TargetCompileOutput(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	// Act.
	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history message"}},
		}},
		Memory: []contexty.Message{{
			ID:    "m1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "memory message"}},
		}},
		Targets: []contexty.CompileTarget{
			{
				Name:     "classifier_history",
				View:     "",
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Budget:   nil,
				Formatter: func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
					out := make([]contexty.Message, len(msgs))
					for i, msg := range msgs {
						out[i] = msg.Clone()
						out[i].Parts = []contexty.ContentPart{
							contexty.TextPart{Text: strings.ToUpper(msg.TextContent())},
						}
					}
					return out, nil
				},
			},
			{
				Name:     "memory_plain",
				View:     "",
				Segments: []contexty.SegmentName{contexty.SegmentMemory}, IncludeArtifacts: true,
				Budget:    nil,
				Formatter: nil,
			},
		},
	})
	// Assert.
	require.NoError(t, err)

	classifier := result.Projections["classifier_history"]
	assert.Equal(t, "HISTORY MESSAGE", classifier.Text)
	require.Len(t, classifier.Messages, 1)
	assert.Equal(t, "h1", classifier.Messages[0].ID)
	assert.Equal(t, "h1", classifier.Source.History[0].ID)
	assert.Contains(t, classifier.Transformations, "h1")

	memory := result.Projections["memory_plain"]
	assert.Equal(t, "memory message", memory.Text)
	require.Len(t, memory.Messages, 1)
	assert.Equal(t, "m1", memory.Messages[0].ID)
}

func TestAcceptance_Projection_InputSnapshotUsesPreparedState(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	hook := fixtureTransformHook{
		fn: func(
			_ context.Context,
			snap contexty.ConversationSnapshot,
		) (contexty.ConversationSnapshot, error) {
			history := snap.Segment(contexty.SegmentHistory)
			history = append(history, contexty.Message{
				ID:    "hooked",
				Role:  contexty.RoleAssistant,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "hooked"}},
			})
			return snap.WithSegment(contexty.SegmentHistory, history), nil
		},
	}

	// Act.
	result, err := contexty.NewEngine(contexty.WithTransformHooks(hook)).CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		Targets: []contexty.CompileTarget{{
			Name:      "history_target",
			View:      "",
			Segments:  []contexty.SegmentName{contexty.SegmentHistory},
			Budget:    nil,
			Formatter: nil,
		}},
	})
	// Assert.
	require.NoError(t, err)

	projection := result.Projections["history_target"]
	assert.Equal(t, []string{"h1"}, fixtureMessageIDs(projection.Source.History))
	assert.Equal(
		t,
		[]string{"h1"},
		fixtureMessageIDs(projection.InputSnapshot.Segment(contexty.SegmentHistory)),
	)
	assert.Equal(t, []string{"h1", "hooked"}, fixtureMessageIDs(projection.Messages))
}

func TestAcceptance_Target_TraceabilityAndIdentityPolicy(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	req := contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "drop",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "drop me"}},
			},
			{
				ID:    "keep",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "keep me"}},
			},
		},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
		Targets: []contexty.CompileTarget{
			{
				Name:     "budgeted",
				View:     "",
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Budget: contexty.NewBudgetPipeline(
					contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(1)},
					&contexty.FixedEstimator{TokensPerMessage: 1},
				),
				Formatter: nil,
			},
			{
				Name:     "derived",
				View:     "",
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Budget:   nil,
				Formatter: func(_ context.Context, _ []contexty.Message) ([]contexty.Message, error) {
					return []contexty.Message{contexty.TextMessage(contexty.RoleUser, "derived")}, nil
				},
			},
		},
	}
	// Act.
	first, err := contexty.NewEngine().CompileSnapshot(ctx, req)
	// Assert.
	require.NoError(t, err)
	second, err := contexty.NewEngine().CompileSnapshot(ctx, req)
	require.NoError(t, err)

	budgeted := first.Projections["budgeted"]
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionTruncated,
		Reason: contexty.ReasonTokenBudgetExceeded,
	}, budgeted.Transformations["drop"].Final())

	derived := first.Projections["derived"]
	require.Len(t, derived.Messages, 1)
	require.NotEmpty(t, derived.Messages[0].ID)
	assert.Equal(t, derived.Messages[0].ID, second.Projections["derived"].Messages[0].ID)
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionFormatted,
		Reason: contexty.ReasonReplacedByFormatter,
	}, derived.Transformations["drop"].Final())
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionFormatted,
		Reason: contexty.ReasonSegmentFormatter,
	}, derived.Transformations[derived.Messages[0].ID].Final())
}

func TestAcceptance_Target_BudgetSummaryTraceability(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	// Act.
	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "h1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "one"}},
			},
			{
				ID:    "h2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "two"}},
			},
		},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
		Targets: []contexty.CompileTarget{
			{
				Name:     "summary_target",
				View:     "",
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Budget: contexty.NewBudgetPipeline(
					contexty.BudgetConfig{
						Budget:     contexty.EffectiveInputBudget(1),
						Summarizer: fixtureSummarizer{summary: contexty.TextMessage(contexty.RoleAssistant, "summary")},
					},
					&contexty.FixedEstimator{TokensPerMessage: 1},
				),
				Formatter: nil,
			},
		},
	})
	// Assert.
	require.NoError(t, err)

	projection := result.Projections["summary_target"]
	require.Len(t, projection.Messages, 1)
	summaryID := projection.Messages[0].ID
	require.NotEmpty(t, summaryID)
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionTruncated,
		Reason: contexty.ReasonTokenBudgetExceeded,
	}, projection.Transformations["h1"].Final())
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionTruncated,
		Reason: contexty.ReasonTokenBudgetExceeded,
	}, projection.Transformations["h2"].Final())
	assert.Equal(
		t,
		contexty.TransformRecord{Action: contexty.ActionPassed, Reason: ""},
		projection.Transformations[summaryID].Final(),
	)
}

func TestAcceptance_Target_GeneratedIDsFailClosedWithoutPolicy(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	// Act.
	_, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		RequireDurableIdentity: true,
		Targets: []contexty.CompileTarget{
			{
				Name:     "derived",
				View:     "",
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Budget:   nil,
				Formatter: func(_ context.Context, _ []contexty.Message) ([]contexty.Message, error) {
					return []contexty.Message{contexty.TextMessage(contexty.RoleUser, "derived")}, nil
				},
			},
		},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingIdentityPolicy)
}

func TestAcceptance_Target_FormatterDuplicateIDsFailClosed(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	// Act.
	_, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		Targets: []contexty.CompileTarget{
			{
				Name:     "duplicate_target",
				View:     "",
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Budget:   nil,
				Formatter: func(_ context.Context, _ []contexty.Message) ([]contexty.Message, error) {
					return []contexty.Message{
						{
							ID:    "dup",
							Role:  contexty.RoleUser,
							Parts: []contexty.ContentPart{contexty.TextPart{Text: "one"}},
						},
						{
							ID:    "dup",
							Role:  contexty.RoleAssistant,
							Parts: []contexty.ContentPart{contexty.TextPart{Text: "two"}},
						},
					}, nil
				},
			},
		},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrDuplicateMessageID)
}

func TestAcceptance_CompileTarget_Validation(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	// Act.
	_, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{
				Name:      "dup",
				View:      "",
				Segments:  nil,
				Budget:    nil,
				Formatter: nil,
			},
			{
				Name:      "dup",
				View:      "",
				Segments:  nil,
				Budget:    nil,
				Formatter: nil,
			},
		},
	})
	// Assert.
	require.Error(t, err)
	require.ErrorIs(t, err, contexty.ErrDuplicateCompileTarget)

	_, err = contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{
				Name:      "unknown_view",
				View:      "clasifier",
				Segments:  nil,
				Budget:    nil,
				Formatter: nil,
			},
		},
	})
	require.ErrorIs(t, err, contexty.ErrUnknownCompileTargetView)

	_, err = contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{
				Name:      "bad_segment",
				View:      "",
				Segments:  []contexty.SegmentName{contexty.SegmentName("scratch")},
				Budget:    nil,
				Formatter: nil,
			},
		},
	})
	require.ErrorIs(t, err, contexty.ErrInvalidCompileTargetSegment)

	_, err = contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{
				Name:      "view_conflict",
				View:      string(contexty.ViewLLMXML),
				Segments:  []contexty.SegmentName{contexty.SegmentHistory},
				Budget:    nil,
				Formatter: nil,
			},
		},
	})
	require.ErrorIs(t, err, contexty.ErrConflictingCompileTargetFields)
}
