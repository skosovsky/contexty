package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_PersistenceProjection_DropsTruncatedByDropHead(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:   contexty.EffectiveInputBudget(35),
			DropHead: contexty.DropHeadConfig{MinMessages: 1},
		},
		&contexty.FixedEstimator{TokensPerMessage: 15},
	)
	engine := fixtureEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe))
	// Act.
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
	// Assert.
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	ids := messageIDsFromSlice(proj)
	assert.NotContains(t, ids, "h1")
	assert.Contains(t, ids, "h3")
}

func TestAcceptance_PersistenceProjection_KeepsOriginalOnRedaction(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithTransformHooks(fixtureEmailTransform()),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "u1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "contact me@example.com"}},
		}},
	})
	// Assert.
	require.NoError(t, err)
	assert.Contains(t, result.Payload.History[0].TextContent(), "[REDACTED]")
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "contact me@example.com", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_ExcludesPending(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
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
	// Assert.
	require.NoError(t, err)
	assert.Len(t, result.Payload.History, 2)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "h1", proj[0].ID)
}

func TestAcceptance_PersistenceProjection_ReplacedByFormatter(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
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
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "mem-new", proj[0].ID)
	assert.Equal(t, "new", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_IncludesSummary(t *testing.T) {
	// Arrange.
	t.Parallel()
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
	engine := fixtureEngine(
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
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "summary-1", proj[0].ID)
}

func TestAcceptance_PersistenceProjection_TextReplacement(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "u1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret@mail.com"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "u1", Text: "REDACTED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "secret@mail.com", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_MemorySegment(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "facts",
			Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "mem-deferred",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "from deferred"}},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "mem-stored",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "stored"}},
		}},
	})
	// Assert.
	require.NoError(t, err)
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	ids := messageIDsFromSlice(proj)
	assert.Contains(t, ids, "mem-stored")
	assert.Contains(t, ids, "mem-deferred")
}

func TestAcceptance_PersistenceProjection_DeferredPlusHook(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithTransformHooks(fixtureEmailTransform()),
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
	assert.Contains(t, result.Payload.Memory[0].TextContent(), "[REDACTED]")
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "contact me@example.com", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_DeferredPlusPreBudgetPatch(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "hint",
			Segment: contexty.SegmentSystem,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "sys-deferred",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret locale"}},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentSystem, MessageID: "sys-deferred", Text: "REDACTED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.System[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentSystem)
	require.Len(t, proj, 1)
	assert.Equal(t, "secret locale", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_SummaryPlusTextReplacement(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget: contexty.EffectiveInputBudget(25),
			Summarizer: stubSummarizer(
				func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
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
	engine := fixtureEngine(
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
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "summary-1", Text: "REDACTED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "secret@mail.com", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_DropsTruncated(t *testing.T) {
	// Arrange.
	t.Parallel()
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
	engine := fixtureEngine(
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
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "summary-1", proj[0].ID)
	for _, id := range []string{"h-a", "h-b", "h-c"} {
		rec, ok := result.Transformations[id]
		require.True(t, ok, "missing %s", id)
		assert.Equal(t, contexty.ActionTruncated, rec.Final().Action)
	}
}

func TestAcceptance_PersistenceProjection_InPlaceFormatter(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	// Act.
	engine := fixtureEngine(
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
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "fmt:raw", result.Payload.Memory[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "raw", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_ReplacedByHook(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithTransformHooks(replaceHistoryHook{}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h-old",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "original"}},
		}},
	})
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 1)
	assert.Equal(t, "hook-new", result.Payload.History[0].ID)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "hook-new", proj[0].ID)
}

func TestAcceptance_PersistenceProjection_PreBudgetPatchNonHistory(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Memory: []contexty.Message{{
			ID:    "m1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentMemory, MessageID: "m1", Text: "REDACTED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.Memory[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "secret", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_SummarizeReusesTruncatedID(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget: contexty.EffectiveInputBudget(25),
			Summarizer: stubSummarizer(
				func(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
					msgs := request.Messages
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
	engine := fixtureEngine(
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
	rec, ok := result.Transformations["h-a"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionTruncated, rec.Final().Action)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	assert.Empty(t, proj)
}

func TestAcceptance_PersistenceProjection_OversizedSummaryRejected(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget: contexty.EffectiveInputBudget(5),
			Summarizer: stubSummarizer(
				func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
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
	engine := fixtureEngine(
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
		},
	})
	// Assert: an oversized summary is rejected without a persistence projection.
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Zero(t, result)
}

func TestAcceptance_PersistenceProjection_DeferredPlusInPlaceFormatter(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	// Act.
	engine := fixtureEngine(
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
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "mem-deferred",
					Role:  contexty.RoleSystem,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "deferred raw"}},
				}}}, nil
			},
		}),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "fmt:deferred raw", result.Payload.Memory[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	require.Len(t, proj, 1)
	assert.Equal(t, "deferred raw", proj[0].TextContent())
}

func TestAcceptance_PersistenceProjection_PartialHookReplaceOrder(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithTransformHooks(partialReplaceHistoryHook{}),
	)
	// Act.
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
	// Assert.
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 2)
	assert.Equal(t, "hook-new", result.Payload.History[1].ID)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 2)
	assert.Equal(t, "h-keep", proj[0].ID)
	assert.Equal(t, "hook-new", proj[1].ID)
}

func TestAcceptance_PersistenceProjection_ToolsSegment(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine(
		contexty.WithTransformHooks(fixtureEmailTransform()),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "tool-facts",
			Segment: contexty.SegmentTools,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				return contexty.DeferredResult{Messages: []contexty.Message{{
					ID:    "tool-deferred",
					Role:  contexty.RoleUser,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "tool me@example.com"}},
				}}}, nil
			},
		}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Tools: []contexty.Message{{
			ID:    "tool-src",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "source tool"}},
		}},
	})
	// Assert.
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

func TestAcceptance_PersistenceProjection_DropsBudgetEvicted(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:           contexty.EffectiveInputBudget(5),
			TruncateStrategy: contexty.NewDropStrategy(),
		},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := fixtureEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe))
	// Act.
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
	// Assert.
	require.NoError(t, err)
	assert.Empty(t, result.Payload.History)
	rec, ok := result.Transformations["h1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionEvicted, rec.Final().Action)
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	assert.Empty(t, proj)
}

func TestAcceptance_PersistenceProjection_FormattedWithoutIntroducedOmits(t *testing.T) {
	// Arrange.
	t.Parallel()
	result := contexty.CompileResult{
		Payload: contexty.AbstractPayload{
			Memory: []contexty.Message{{
				ID:    "mem-new",
				Role:  contexty.RoleSystem,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "REDACTED"}},
			}},
		},
		Transformations: map[string]contexty.TransformChain{
			"mem-new": {{Action: contexty.ActionFormatted, Reason: contexty.ReasonTextReplacement}},
		},
		Source:     contexty.CompileRequest{},
		Introduced: nil,
	}
	// Act.
	proj := result.DerivePersistenceProjection(contexty.SegmentMemory)
	// Assert.
	assert.Empty(t, proj)
}

func TestAcceptance_Conversation_DeltaStateCodecAndStore(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	initial := contexty.EmptyState().WithSegment(contexty.SegmentHistory, []contexty.Message{
		{
			ID:    "m1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "one"}},
		},
	})
	artifact := contexty.NewMemoryBlock(
		"mem-1",
		contexty.TextPayload("durable memory"),
	).ContextArtifact
	state, err := contexty.ApplyDeltas(
		initial,
		contexty.ConversationDelta{
			Operation: contexty.DeltaAppendMessages,
			Segment:   contexty.SegmentHistory,
			Messages: []contexty.Message{
				{
					ID:    "m2",
					Role:  contexty.RoleAssistant,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "two"}},
				},
			},
		},
		contexty.ConversationDelta{
			Operation: contexty.DeltaUpsertArtifact,
			Artifact:  &artifact,
		},
	)
	require.NoError(t, err)
	assert.Len(t, initial.Segment(contexty.SegmentHistory), 1)
	assert.Len(t, state.Segment(contexty.SegmentHistory), 2)
	require.Len(t, state.Artifacts(), 1)

	codec := contexty.ConversationStateCodec{Provenance: contexty.DefaultProvenanceRegistry()}
	stateData, err := codec.EncodeState(state)
	require.NoError(t, err)
	decodedState, err := codec.DecodeState(stateData)
	require.NoError(t, err)
	assert.True(
		t,
		contexty.MessagesEqual(
			state.Segment(contexty.SegmentHistory),
			decodedState.Segment(contexty.SegmentHistory),
		),
	)
	assert.Equal(t, "mem-1", decodedState.Artifacts()[0].ID)

	deltaData, err := codec.EncodeDelta(contexty.ConversationDelta{
		Operation:  contexty.DeltaRemoveMessages,
		Segment:    contexty.SegmentHistory,
		MessageIDs: []string{"m1"},
	})
	require.NoError(t, err)
	decodedDelta, err := codec.DecodeDelta(deltaData)
	require.NoError(t, err)
	reduced, err := contexty.ApplyDelta(decodedState, decodedDelta)
	require.NoError(t, err)
	assert.Equal(t, []string{"m2"}, messageIDs(reduced.Segment(contexty.SegmentHistory)))
	assert.Equal(t, "mem-1", reduced.Artifacts()[0].ID)

	store := contexty.NewMemoryConversationStateStore()
	err = store.CommitState(ctx, "conversation-1", 0, contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment,
		Segment:   contexty.SegmentHistory,
		Messages:  reduced.Segment(contexty.SegmentHistory),
	})
	require.NoError(t, err)
	stored, err := store.LoadState(ctx, "conversation-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), stored.Version())
	assert.Equal(t, []string{"m2"}, messageIDs(stored.Segment(contexty.SegmentHistory)))

	args, err := contexty.StructuredPayload(struct {
		Query string `json:"query"`
	}{Query: "from store"})
	require.NoError(t, err)
	assistant := contexty.Message{
		ID:   "assistant-call",
		Role: contexty.RoleAssistant,
		Parts: []contexty.ContentPart{
			contexty.ToolCallPart{
				ID:        "call-1",
				Name:      "lookup",
				Arguments: args,
			},
		},
	}
	toolResult := contexty.Message{
		ID:   "tool-result",
		Role: contexty.RoleTool,
		Parts: []contexty.ContentPart{
			contexty.ToolResultPart{
				ToolCallID: "call-1",
				Name:       "lookup",
				Payload:    contexty.TextPayload("tool payload"),
			},
		},
	}
	round := contexty.ToolRound{
		Assistant: assistant,
		Results:   []contexty.Message{toolResult},
	}
	err = store.CommitState(ctx, "conversation-1", 1, contexty.ConversationDelta{
		Operation: contexty.DeltaAppendToolRound,
		ToolRound: &round,
	})
	require.NoError(t, err)
	stored, err = store.LoadState(ctx, "conversation-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), stored.Version())
	assert.Equal(
		t,
		[]string{"m2", "assistant-call", "tool-result"},
		messageIDs(stored.Segment(contexty.SegmentHistory)),
	)
	assert.True(t, contexty.ToolTurnUsesCanonicalLayout(stored.Segment(contexty.SegmentHistory), 1))
	require.Len(t, stored.ToolRounds(), 1)

	engine := fixtureEngine(
		contexty.WithConversationID("conversation-1"),
		contexty.WithStateStore(store),
	)
	// Act.
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, []string{"m2", "assistant-call", "tool-result"}, messageIDs(result.Payload.History))
	err = store.CommitState(
		ctx,
		"conversation-1",
		0,
		contexty.ConversationDelta{Operation: contexty.DeltaClearState},
	)
	require.ErrorIs(t, err, contexty.ErrConversationVersionConflict)
}
