package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestDoD_MergePolicyReplaceByOrigin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "persona",
			Segment:     contexty.SegmentSystem,
			MergePolicy: contexty.PolicyReplaceByOrigin,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "new-persona",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "updated persona"}},
					Origin: &contexty.MessageOrigin{
						TemplateID: "agents/sales",
						LayerID:    "persona-v2",
					},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{{
			ID:     "old-persona",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "old persona"}},
			Origin: &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona-v1"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.System, 1)
	assert.Equal(t, "updated persona", result.Payload.System[0].TextContent())
}

func TestDoD_MergePolicyDeduplicateByLayer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "layer-dedup",
			Segment:     contexty.SegmentMemory,
			MergePolicy: contexty.PolicyDeduplicateByLayer,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:     "mem-new",
					Role:   contexty.RoleSystem,
					Parts:  []contexty.ContentPart{contexty.TextPart{Text: "fresh layer"}},
					Origin: &contexty.MessageOrigin{TemplateID: "t2", LayerID: "facts"},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:     "mem-old",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "stale layer"}},
			Origin: &contexty.MessageOrigin{TemplateID: "t1", LayerID: "facts"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.Memory, 1)
	assert.Equal(t, "fresh layer", result.Payload.Memory[0].TextContent())
}

func TestDoD_MergePolicyAppendDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "append",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "mem-2",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "second"}},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "first"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.Memory, 2)
}

func TestDoD_EphemeralPatchLastUserMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "u1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "hello"}},
			},
			{
				ID:    "a1",
				Role:  contexty.RoleAssistant,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "hi"}},
			},
			{
				ID:    "u2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret@mail.com"}},
			},
		},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	require.NoError(t, err)
	last := result.Payload.History[len(result.Payload.History)-1]
	assert.Equal(t, "REDACTED", last.TextContent())
}

func TestDoD_EphemeralPatchNotMutatesInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "u1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "original"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	}
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
	assert.Equal(t, "original", result.Source.History[0].TextContent())
	assert.Equal(t, "original", req.History[0].TextContent())
}

func TestDoD_EphemeralPatch_EmptySegmentNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Payload.History)
}

func TestDoD_EphemeralPatch_NoMatchingRoleNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "a1",
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "only assistant"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "only assistant", result.Payload.History[0].TextContent())
}

func TestDoD_ResolveVarInDeferred(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "locale",
			Segment: contexty.SegmentMemory,
			Resolve: func(ctx context.Context) ([]contexty.Message, error) {
				vars := contexty.CompileResolveVarFromContext(ctx)
				return []contexty.Message{
					contexty.TextMessage(contexty.RoleSystem, "locale="+vars["locale"]),
				}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("locale", "ru-RU"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "locale=ru-RU", result.Payload.Memory[0].TextContent())
}

func TestDoD_NamedViewWithBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 30, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 15},
	)
	engine := contexty.NewEngine(
		contexty.WithNamedView("classifier", contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Budget:        pipe,
		}),
	)
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "one"),
		contexty.TextMessage(contexty.RoleUser, "two"),
		contexty.TextMessage(contexty.RoleUser, "three"),
	})
	out, err := engine.RenderView(ctx, snap, "classifier")
	require.NoError(t, err)
	assert.NotContains(t, out, "one")
	assert.Contains(t, out, "three")
}

func TestDoD_NamedViewFormatterInjected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithNamedView("wrapped", contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Formatter: func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				out := make([]contexty.Message, len(msgs))
				for i, m := range msgs {
					cloned := m.Clone()
					cloned.Parts = []contexty.ContentPart{
						contexty.TextPart{Text: "WRAP:" + m.TextContent()},
					}
					out[i] = cloned
				}
				return out, nil
			},
		}),
	)
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "x"),
	})
	out, err := engine.RenderView(ctx, snap, "wrapped")
	require.NoError(t, err)
	assert.Equal(t, "WRAP:x", out)
}

func TestDoD_RenderViewNonMutating(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 20, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithNamedView("trim", contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Budget:        pipe,
		}),
	)
	before := []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "keep-me-1"),
		contexty.TextMessage(contexty.RoleUser, "keep-me-2"),
	}
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, cloneTestMsgs(before))
	_, err := engine.RenderView(ctx, snap, "trim")
	require.NoError(t, err)
	after := snap.Segment(contexty.SegmentHistory)
	require.True(t, contexty.MessagesEqual(before, after))
}

func TestDoD_PersistenceProjectionDropsTruncatedByDropHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 35, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 15},
	)
	engine := contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe))
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "h1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}},
			},
			{
				ID:    "h2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "mid"}},
			},
			{
				ID:    "h3",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "new"}},
			},
		},
	})
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	ids := messageIDsFromSlice(proj)
	assert.NotContains(t, ids, "h1")
	assert.Contains(t, ids, "h3")
}

func TestDoD_PersistenceProjectionKeepsOriginalOnRedaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "u1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "contact me@example.com"}},
		}},
	})
	require.NoError(t, err)
	assert.Contains(t, result.Payload.History[0].TextContent(), "[REDACTED]")
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "contact me@example.com", proj[0].TextContent())
}

func TestDoD_PersistenceProjectionExcludesPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "stored"}},
		}},
		Pending: []contexty.Message{{
			ID:    "p1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "current turn"}},
		}},
	})
	require.NoError(t, err)
	assert.Len(t, result.Payload.History, 2)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "h1", proj[0].ID)
}

func TestDoD_PersistenceProjection_ReplacedByFormatter(t *testing.T) {
	t.Parallel()
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
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-old",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}},
		}},
	})
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "mem-new", proj[0].ID)
	assert.Equal(t, "new", proj[0].TextContent())
}

func TestDoD_PersistenceProjection_IncludesSummary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			TokenLimit: 25,
			Summarizer: stubSummarizer(
				func(context.Context, []contexty.Message) (contexty.Message, error) {
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
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "summary-1", proj[0].ID)
}

func TestDoD_PersistenceProjection_EphemeralPatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "u1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret@mail.com"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "secret@mail.com", proj[0].TextContent())
}

func TestDoD_PersistenceProjection_MemorySegment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "facts",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "mem-deferred",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "from deferred"}},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-stored",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "stored"}},
		}},
	})
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	ids := messageIDsFromSlice(proj)
	assert.Contains(t, ids, "mem-stored")
	assert.Contains(t, ids, "mem-deferred")
}

func TestDoD_MergePolicyReplaceByOrigin_PersistenceProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "persona",
			Segment:     contexty.SegmentSystem,
			MergePolicy: contexty.PolicyReplaceByOrigin,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "new-persona",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "updated persona"}},
					Origin: &contexty.MessageOrigin{
						TemplateID: "agents/sales",
						LayerID:    "persona-v2",
					},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{{
			ID:     "old-persona",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "old persona"}},
			Origin: &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona-v1"},
		}},
	})
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentSystem)
	require.Len(t, proj, 1)
	assert.Equal(t, "new-persona", proj[0].ID)
	oldRec, ok := result.Transformations["old-persona"]
	require.True(t, ok)
	assert.Equal(t, contexty.ReasonReplacedByDeferred, oldRec.Reason)
}

func TestDoD_MergePolicyDeduplicateByLayer_PersistenceProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "layer-dedup",
			Segment:     contexty.SegmentMemory,
			MergePolicy: contexty.PolicyDeduplicateByLayer,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:     "mem-new",
					Role:   contexty.RoleSystem,
					Parts:  []contexty.ContentPart{contexty.TextPart{Text: "fresh layer"}},
					Origin: &contexty.MessageOrigin{TemplateID: "t2", LayerID: "facts"},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:     "mem-old",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "stale layer"}},
			Origin: &contexty.MessageOrigin{TemplateID: "t1", LayerID: "facts"},
		}},
	})
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "mem-new", proj[0].ID)
	oldRec, ok := result.Transformations["mem-old"]
	require.True(t, ok)
	assert.Equal(t, contexty.ReasonReplacedByDeferred, oldRec.Reason)
}

func TestDoD_PersistenceProjection_DeferredPlusHook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "contact",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:   "mem-deferred",
					Role: contexty.RoleSystem,
					Parts: []contexty.ContentPart{
						contexty.TextPart{Text: "contact me@example.com"},
					},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{})
	require.NoError(t, err)
	assert.Contains(t, result.Payload.Memory[0].TextContent(), "[REDACTED]")
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "contact me@example.com", proj[0].TextContent())
}

func TestDoD_PersistenceProjection_DeferredPlusPreBudgetPatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "hint",
			Segment: contexty.SegmentSystem,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "sys-deferred",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret locale"}},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentSystem,
				Role:     contexty.RoleSystem,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.System[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentSystem)
	require.Len(t, proj, 1)
	assert.Equal(t, "secret locale", proj[0].TextContent())
}

func TestDoD_PersistenceProjection_SummaryPlusEphemeralPatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			TokenLimit: 25,
			Summarizer: stubSummarizer(
				func(context.Context, []contexty.Message) (contexty.Message, error) {
					return contexty.Message{
						ID:    "summary-1",
						Role:  contexty.RoleSystem,
						Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret@mail.com"}},
					}, nil
				},
			),
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
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
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleSystem,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "secret@mail.com", proj[0].TextContent())
}

func TestDoD_PersistenceProjectionDropsTruncated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			TokenLimit: 25,
			Summarizer: stubSummarizer(
				func(context.Context, []contexty.Message) (contexty.Message, error) {
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
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "summary-1", proj[0].ID)
	for _, id := range []string{"h-a", "h-b", "h-c"} {
		rec, ok := result.Transformations[id]
		require.True(t, ok, "missing %s", id)
		assert.Equal(t, contexty.ActionTruncated, rec.Action)
	}
}

func TestDoD_PersistenceProjection_InPlaceFormatter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithSegmentFormatter(
			contexty.SegmentMemory,
			func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				out := make([]contexty.Message, len(msgs))
				for i, m := range msgs {
					cloned := m.Clone()
					cloned.Parts = []contexty.ContentPart{
						contexty.TextPart{Text: "fmt:" + m.TextContent()},
					}
					out[i] = cloned
				}
				return out, nil
			},
		),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "raw"}},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "fmt:raw", result.Payload.Memory[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "raw", proj[0].TextContent())
}

func TestDoD_PersistenceProjection_ReplacedByHook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(replaceHistoryHook{}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h-old",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "original"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 1)
	assert.Equal(t, "hook-new", result.Payload.History[0].ID)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "hook-new", proj[0].ID)
}

func TestDoD_EphemeralPatch_PendingTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	req := contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "prior"}},
		}},
		Pending: []contexty.Message{{
			ID:    "p1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret@mail.com"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	}
	result, err := engine.CompileSnapshot(ctx, req)
	require.NoError(t, err)
	last := result.Payload.History[len(result.Payload.History)-1]
	assert.Equal(t, "p1", last.ID)
	assert.Equal(t, "REDACTED", last.TextContent())
	assert.Equal(t, "secret@mail.com", result.Source.Pending[0].TextContent())
	assert.Equal(t, "secret@mail.com", req.Pending[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "h1", proj[0].ID)
}

func TestDoD_RenderView_BuiltinParity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	snap := contexty.EmptySnapshot().
		WithSegment(contexty.SegmentSystem, []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "sys"),
		}).
		WithSegment(contexty.SegmentHistory, []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "hello"),
		}).
		WithSegment(contexty.SegmentMemory, []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "mem"),
		}).
		WithSegment(contexty.SegmentTools, []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "tool-a"),
			contexty.TextMessage(contexty.RoleUser, "tool-b"),
		})
	engine := contexty.NewEngine()
	for _, name := range []string{string(contexty.ViewLLMXML), string(contexty.ViewFlatClassifier)} {
		viewName := name
		t.Run(viewName, func(t *testing.T) {
			t.Parallel()
			viaRender, err := contexty.Render(ctx, snap, contexty.ViewType(viewName))
			require.NoError(t, err)
			viaRenderView, err := engine.RenderView(ctx, snap, viewName)
			require.NoError(t, err)
			assert.Equal(t, viaRender, viaRenderView)
		})
	}
}

func messageIDsFromSlice(msgs []contexty.Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

func findTestMessageByID(msgs []contexty.Message, id string) *contexty.Message {
	for i := range msgs {
		if msgs[i].ID == id {
			return &msgs[i]
		}
	}
	return nil
}

func cloneTestMsgs(in []contexty.Message) []contexty.Message {
	out := make([]contexty.Message, len(in))
	for i, m := range in {
		out[i] = m.Clone()
	}
	return out
}

type replaceHistoryHook struct{}

func (replaceHistoryHook) Transform(
	_ context.Context,
	snap contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	return snap.WithSegment(contexty.SegmentHistory, []contexty.Message{{
		ID:    "hook-new",
		Role:  contexty.RoleAssistant,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "replacement"}},
	}}), nil
}

type partialReplaceHistoryHook struct{}

func (partialReplaceHistoryHook) Transform(
	_ context.Context,
	snap contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	history := snap.Segment(contexty.SegmentHistory)
	if len(history) == 0 {
		return snap, nil
	}
	return snap.WithSegment(contexty.SegmentHistory, []contexty.Message{
		history[0],
		{
			ID:    "hook-new",
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "inserted"}},
		},
	}), nil
}

func TestDoD_EphemeralPatch_PreBudgetSkipsHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret@mail.com"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	require.NoError(t, err)
	// post-budget pass applies patch
	assert.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
}

func TestDoD_PersistenceProjection_PreBudgetPatchNonHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "m1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentMemory,
				Role:     contexty.RoleSystem,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.Memory[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "secret", proj[0].TextContent())
}

func TestDoD_ResolveVarMapClone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var captured, fresh map[string]string
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "vars",
			Segment: contexty.SegmentMemory,
			Resolve: func(ctx context.Context) ([]contexty.Message, error) {
				captured = contexty.CompileResolveVarFromContext(ctx)
				captured["mutated"] = "yes"
				fresh = contexty.CompileResolveVarFromContext(ctx)
				return []contexty.Message{
					contexty.TextMessage(contexty.RoleSystem, "locale="+fresh["locale"]),
				}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("locale", "ru-RU"),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.Equal(t, "yes", captured["mutated"])
	require.NotNil(t, fresh)
	assert.Equal(t, "ru-RU", fresh["locale"])
	_, hasMutated := fresh["mutated"]
	assert.False(t, hasMutated)
	assert.Equal(t, "locale=ru-RU", result.Payload.Memory[0].TextContent())
}

func TestDoD_PersistenceProjection_SummarizeReusesTruncatedID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			TokenLimit: 25,
			Summarizer: stubSummarizer(
				func(_ context.Context, msgs []contexty.Message) (contexty.Message, error) {
					return contexty.Message{
						ID:    msgs[0].ID,
						Role:  contexty.RoleSystem,
						Parts: []contexty.ContentPart{contexty.TextPart{Text: "summary-reused-id"}},
					}, nil
				},
			),
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
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
	require.NoError(t, err)
	rec, ok := result.Transformations["h-a"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionTruncated, rec.Action)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	assert.Empty(t, proj)
}

func TestDoD_PersistenceProjection_SummaryEvictedByTruncate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			TokenLimit: 5,
			Summarizer: stubSummarizer(
				func(context.Context, []contexty.Message) (contexty.Message, error) {
					return contexty.Message{
						ID:    "summary-1",
						Role:  contexty.RoleSystem,
						Parts: []contexty.ContentPart{contexty.TextPart{Text: "still-too-large"}},
					}, nil
				},
			),
			DropHead: contexty.DropHeadConfig{MinMessages: 0},
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
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
		},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Payload.History)
	sumRec, ok := result.Transformations["summary-1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionTruncated, sumRec.Action)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	assert.Empty(t, proj)
}

func TestDoD_PersistenceProjection_DeferredPlusInPlaceFormatter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithSegmentFormatter(
			contexty.SegmentMemory,
			func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				out := make([]contexty.Message, len(msgs))
				for i, m := range msgs {
					cloned := m.Clone()
					cloned.Parts = []contexty.ContentPart{
						contexty.TextPart{Text: "fmt:" + m.TextContent()},
					}
					out[i] = cloned
				}
				return out, nil
			},
		),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "facts",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "mem-deferred",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "deferred raw"}},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{})
	require.NoError(t, err)
	assert.Equal(t, "fmt:deferred raw", result.Payload.Memory[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "deferred raw", proj[0].TextContent())
}

func TestDoD_IntroducedBaseline_DeferredAndHook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "contact",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:   "mem-deferred",
					Role: contexty.RoleSystem,
					Parts: []contexty.ContentPart{
						contexty.TextPart{Text: "contact me@example.com"},
					},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{})
	require.NoError(t, err)
	require.Contains(t, result.Introduced, "mem-deferred")
	assert.Equal(t, "contact me@example.com", result.Introduced["mem-deferred"].TextContent())
}

func TestDoD_PersistenceProjection_PartialHookReplaceOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(partialReplaceHistoryHook{}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "h-keep",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "keep"}},
			},
			{
				ID:    "h-drop",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "drop"}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 2)
	assert.Equal(t, "hook-new", result.Payload.History[1].ID)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 2)
	assert.Equal(t, "h-keep", proj[0].ID)
	assert.Equal(t, "hook-new", proj[1].ID)
}

func TestDoD_PersistenceProjection_ToolsSegment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "tool-facts",
			Segment: contexty.SegmentTools,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "tool-deferred",
					Role:  contexty.RoleUser,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "tool me@example.com"}},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Tools: []contexty.Message{{
			ID:    "tool-src",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "source tool"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.Tools, 2)
	assert.Contains(t, result.Payload.Tools[1].TextContent(), "[REDACTED]")
	assert.Equal(t, "tool me@example.com", result.Introduced["tool-deferred"].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentTools)
	require.Len(t, proj, 2)
	assert.Equal(t, "tool-src", proj[0].ID)
	assert.Equal(t, "source tool", proj[0].TextContent())
	assert.Equal(t, "tool-deferred", proj[1].ID)
	assert.Equal(t, "tool me@example.com", proj[1].TextContent())
}

func TestDoD_EphemeralPatch_PositionAll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "u1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "first"}},
			},
			{
				ID:    "a1",
				Role:  contexty.RoleAssistant,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "mid"}},
			},
			{
				ID:    "u2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "second"}},
			},
		},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionAll,
			}, "PATCHED"),
		},
	})
	require.NoError(t, err)
	for _, id := range []string{"u1", "u2"} {
		m := findTestMessageByID(result.Payload.History, id)
		require.NotNil(t, m)
		assert.Equal(t, "PATCHED", m.TextContent())
	}
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 3)
	assert.Equal(t, "first", proj[0].TextContent())
	assert.Equal(t, "mid", proj[1].TextContent())
	assert.Equal(t, "second", proj[2].TextContent())
}

func TestDoD_RenderView_BuiltinTakesPrecedenceOverRegistry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithNamedView(string(contexty.ViewLLMXML), contexty.ViewConfiguration{
			SourceSegment: contexty.SegmentHistory,
			Formatter: func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
				out := make([]contexty.Message, len(msgs))
				for i := range msgs {
					out[i] = contexty.TextMessage(contexty.RoleUser, "OVERRIDDEN")
				}
				return out, nil
			},
		}),
	)
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "hello"),
	})
	out, err := engine.RenderView(ctx, snap, string(contexty.ViewLLMXML))
	require.NoError(t, err)
	assert.Contains(t, out, "<user>hello</user>")
	assert.NotContains(t, out, "OVERRIDDEN")
}

func TestDoD_PersistenceProjection_DropsBudgetEvicted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			TokenLimit:       5,
			TruncateStrategy: contexty.NewDropStrategy(),
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe))
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
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
	})
	require.NoError(t, err)
	assert.Empty(t, result.Payload.History)
	rec, ok := result.Transformations["h1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionEvicted, rec.Action)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	assert.Empty(t, proj)
}

func TestDoD_MergePolicyReplaceByOrigin_SkipsMessagesWithoutOrigin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "no-origin",
			Segment:     contexty.SegmentSystem,
			MergePolicy: contexty.PolicyReplaceByOrigin,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "incoming-no-origin",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "incoming"}},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{{
			ID:     "existing-with-origin",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "existing"}},
			Origin: &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.System, 2)
	proj := result.DerivePersistenceProjection(contexty.SegmentSystem)
	require.Len(t, proj, 2)
	assert.Equal(t, "existing-with-origin", proj[0].ID)
	assert.Equal(t, "incoming-no-origin", proj[1].ID)
}

func TestDoD_PersistenceProjection_FormattedWithoutIntroducedOmits(t *testing.T) {
	t.Parallel()
	result := contexty.CompileResult{
		Payload: contexty.AbstractPayload{
			Memory: []contexty.Message{{
				ID:    "mem-new",
				Role:  contexty.RoleSystem,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "REDACTED"}},
			}},
		},
		Transformations: map[string]contexty.TransformRecord{
			"mem-new": {Action: contexty.ActionFormatted, Reason: contexty.ReasonEphemeralPatch},
		},
		Source:     contexty.CompileRequest{},
		Introduced: nil,
	}
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	assert.Empty(t, proj)
}

type reintroduceSystemMessageHook struct {
	id   string
	role contexty.Role
	text string
}

func (h reintroduceSystemMessageHook) Transform(
	_ context.Context,
	snap contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	system := append([]contexty.Message{}, snap.Segment(contexty.SegmentSystem)...)
	system = append(system, contexty.Message{
		ID:    h.id,
		Role:  h.role,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: h.text}},
	})
	return snap.WithSegment(contexty.SegmentSystem, system), nil
}

func TestDoD_Recorder_StructuralFormattedNotOverwrittenByPassed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:        "persona",
			Segment:     contexty.SegmentSystem,
			MergePolicy: contexty.PolicyReplaceByOrigin,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "new-persona",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "updated persona"}},
					Origin: &contexty.MessageOrigin{
						TemplateID: "agents/sales",
						LayerID:    "persona-v2",
					},
				}}, nil
			},
		}),
		contexty.WithTransformHooks(reintroduceSystemMessageHook{
			id:   "old-persona",
			role: contexty.RoleSystem,
			text: "reintroduced by hook",
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{{
			ID:     "old-persona",
			Role:   contexty.RoleSystem,
			Parts:  []contexty.ContentPart{contexty.TextPart{Text: "old persona"}},
			Origin: &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona-v1"},
		}},
	})
	require.NoError(t, err)
	oldRec, ok := result.Transformations["old-persona"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionFormatted, oldRec.Action)
	assert.Equal(t, contexty.ReasonReplacedByDeferred, oldRec.Reason)
	proj := result.DerivePersistenceProjection(contexty.SegmentSystem)
	require.Len(t, proj, 1)
	assert.Equal(t, "new-persona", proj[0].ID)
}

func TestDoD_EphemeralPatch_PositionFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "u1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "first"}},
			},
			{
				ID:    "a1",
				Role:  contexty.RoleAssistant,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "mid"}},
			},
			{
				ID:    "u2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "last"}},
			},
		},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionFirst,
			}, "PATCHED"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "PATCHED", result.Payload.History[0].TextContent())
	assert.Equal(t, "last", result.Payload.History[2].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 3)
	assert.Equal(t, "first", proj[0].TextContent())
	assert.Equal(t, "last", proj[2].TextContent())
}

func TestDoD_EphemeralPatch_ZeroPositionDefaultsToFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "u1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "first"}},
			},
			{
				ID:    "u2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "second"}},
			},
		},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment: contexty.SegmentHistory,
				Role:    contexty.RoleUser,
			}, "PATCHED"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "PATCHED", result.Payload.History[0].TextContent())
	assert.Equal(t, "second", result.Payload.History[1].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 2)
	assert.Equal(t, "first", proj[0].TextContent())
}

func TestDoD_EphemeralPatch_ToolsSegment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Tools: []contexty.Message{{
			ID:    "tool-1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "tool payload"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentTools,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "PATCHED"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "PATCHED", result.Payload.Tools[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentTools)
	require.Len(t, proj, 1)
	assert.Equal(t, "tool payload", proj[0].TextContent())
}

func TestDoD_EphemeralPatch_PostBudgetSkipsNonHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine()
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "hist"}},
		}},
		Memory: []contexty.Message{{
			ID:    "mem-1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "mem"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentMemory,
				Role:     contexty.RoleSystem,
				Position: contexty.PositionLast,
			}, "MEM-PATCH"),
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "HIST-PATCH"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "MEM-PATCH", result.Payload.Memory[0].TextContent())
	assert.Equal(t, "HIST-PATCH", result.Payload.History[0].TextContent())
	memRec, ok := result.Transformations["mem-1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ReasonEphemeralPatch, memRec.Reason)
}

func TestDoD_Introduced_BaselineBeforeHooksAndPatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "facts",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:   "mem-deferred",
					Role: contexty.RoleSystem,
					Parts: []contexty.ContentPart{
						contexty.TextPart{Text: "contact me@example.com"},
					},
				}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-src",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "source"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentMemory,
				Role:     contexty.RoleSystem,
				Position: contexty.PositionFirst,
			}, "PATCHED"),
		},
	})
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
