package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestDoD_CompileResultImmutableContract(t *testing.T) {
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
				contexty.BudgetConfig{TokenLimit: 1000},
				&contexty.FixedEstimator{TokensPerMessage: 10},
			),
		),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{contexty.TextMessage(contexty.RoleUser, "past")},
		Pending: []contexty.Message{pending},
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 2)
	assert.Equal(t, "pending-1", result.Payload.History[1].ID)
	assert.Equal(t, "current turn", result.Payload.History[1].TextContent())
	// Pending is merged into History; host must not patch Payload after compile.
}

type testExtension struct {
	Tenant string  `json:"tenant"`
	Score  float64 `json:"score"`
}

func (e testExtension) ExtensionType() string { return "test_extension" }

func (e testExtension) CloneExtension() contexty.Extension { return e }

func newTestExtensionRegistry() *contexty.ExtensionRegistry {
	reg := contexty.NewExtensionRegistry()
	reg.Register("test_extension", func(data []byte) (contexty.Extension, error) {
		var ext testExtension
		err := json.Unmarshal(data, &ext)
		return ext, err
	})
	return reg
}

func TestDoD_ExtensionsRoundTrip(t *testing.T) {
	reg := newTestExtensionRegistry()
	codec := contexty.ConversationCodec{
		Provenance: contexty.DefaultProvenanceRegistry(),
		Extensions: reg,
	}
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{{
		ID:         "ext-1",
		Role:       contexty.RoleUser,
		Parts:      []contexty.ContentPart{contexty.TextPart{Text: "hi"}},
		Extensions: []contexty.Extension{testExtension{Tenant: "acme", Score: 3}},
	}})
	data, err := codec.Encode(snap)
	require.NoError(t, err)
	decoded, err := codec.Decode(data)
	require.NoError(t, err)
	msgs := decoded.Segment(contexty.SegmentHistory)
	require.Len(t, msgs, 1)
	ext, ok := msgs[0].Extensions[0].(testExtension)
	require.True(t, ok)
	assert.Equal(t, "acme", ext.Tenant)
	assert.InEpsilon(t, float64(3), ext.Score, 0)
}

func TestDoD_TransformationsByMessageID(t *testing.T) {
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
		&contexty.FixedEstimator{TokensPerMessage: 10},
	)
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
	)
	req := contexty.CompileRequest{History: []contexty.Message{
		{
			ID:    "drop",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "old"}},
		},
		{
			ID:    "keep",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "new"}},
		},
	}}
	result, err := engine.CompileSnapshot(ctx, req)
	require.NoError(t, err)
	rec, ok := result.Transformations["drop"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionTruncated, rec.Action)
	assert.Equal(t, contexty.ReasonTokenBudgetExceeded, rec.Reason)
	_, ok = result.Transformations["keep"]
	assert.True(t, ok)
}

func TestDoD_SegmentFormatterInjectedByHost(t *testing.T) {
	ctx := context.Background()
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
	require.NoError(t, err)
	require.Len(t, result.Payload.Memory, 1)
	assert.Contains(t, result.Payload.Memory[0].TextContent(), "<memory>")
}

func TestDoD_PendingNeverEvicted(t *testing.T) {
	ctx := context.Background()
	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 15, DropHead: contexty.DropHeadConfig{MinMessages: 1}},
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
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "a"),
			contexty.TextMessage(contexty.RoleUser, "b"),
			contexty.TextMessage(contexty.RoleUser, "c"),
		},
		Pending: []contexty.Message{pending},
	})
	require.NoError(t, err)
	last := result.Payload.History[len(result.Payload.History)-1]
	assert.Equal(t, "pending-protected", last.ID)
	assert.Equal(t, "must stay", last.TextContent())
	rec := result.Transformations["pending-protected"]
	assert.Equal(t, contexty.ActionPassed, rec.Action)
	assert.Equal(t, contexty.ReasonProtectedPending, rec.Reason)
}

func TestDoD_PendingExceedsBudget(t *testing.T) {
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{TokenLimit: 50},
				&contexty.FixedEstimator{TokensPerMessage: 30},
			),
		),
	)
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Pending: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "p1"),
			contexty.TextMessage(contexty.RoleUser, "p2"),
		},
	})
	require.ErrorIs(t, err, contexty.ErrPendingExceedsBudget)
}

func TestDoD_BudgetPreflightReservesPendingAndSystem(t *testing.T) {
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{
					TokenLimit: 40,
					DropHead:   contexty.DropHeadConfig{MinMessages: 1},
				},
				&contexty.FixedEstimator{TokensPerMessage: 10},
			),
		),
	)
	// reserved: system(10) + pending(10) = 20, available history = 20 -> at most 2 history msgs survive (+ pending merged after)
	pendingMsg := contexty.Message{
		ID: "p", Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "pend"}},
	}
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{contexty.TextMessage(contexty.RoleSystem, "sys")},
		History: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "h1"),
			contexty.TextMessage(contexty.RoleUser, "h2"),
			contexty.TextMessage(contexty.RoleUser, "h3"),
		},
		Pending: []contexty.Message{pendingMsg},
	})
	require.NoError(t, err)
	// 2 history trimmed + 1 pending = 3 in payload history
	require.Len(t, result.Payload.History, 3)
	assert.Equal(t, "p", result.Payload.History[2].ID)
}

func TestDoD_FormatterAffectsTokenBudget(t *testing.T) {
	ctx := context.Background()
	basePipe := contexty.BudgetConfig{
		TokenLimit: 35,
		DropHead:   contexty.DropHeadConfig{MinMessages: 1},
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
	without, err := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(basePipe, est),
		),
	).CompileSnapshot(ctx, req)
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

func TestDoD_FormatterSameIDRecordsFormatted(t *testing.T) {
	ctx := context.Background()
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
	require.NoError(t, err)
	rec, ok := result.Transformations["mem-same"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionFormatted, rec.Action)
	assert.Equal(t, contexty.ReasonSegmentFormatter, rec.Reason)
}

func TestDoD_CompileSnapshotSelfContained(t *testing.T) {
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	_, _ = loadState(ctx, store, "ignored")
	engine := contexty.NewEngine(
		contexty.WithStateStore(store),
		contexty.WithConversationID("ignored"),
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{TokenLimit: 1000},
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
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{hist},
		Tools:   tools,
		Pending: pending,
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.Tools, 1)
	assert.Equal(t, "tool-1", result.Payload.Tools[0].ID)
	require.Len(t, result.Payload.History, 2)
	assert.Equal(t, "pend-1", result.Payload.History[1].ID)
}

func TestDoD_PendingHistoryIDCollision(t *testing.T) {
	ctx := context.Background()
	engine := contexty.NewEngine()
	dup := contexty.Message{
		ID:    "dup-id",
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
	}
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{dup},
		Pending: []contexty.Message{dup.Clone()},
	})
	require.ErrorIs(t, err, contexty.ErrDuplicateMessageID)
}

func TestDoD_DeferredDuplicateMessageID(t *testing.T) {
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(contexty.DeferredBlock{
			Name:    "dup-deferred",
			Segment: contexty.SegmentHistory,
			Resolve: func(context.Context) ([]contexty.Message, error) {
				return []contexty.Message{{
					ID:    "hist-dup",
					Role:  contexty.RoleUser,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: "from deferred"}},
				}}, nil
			},
		}),
	)
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "hist-dup",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "existing"}},
		}},
	})
	require.ErrorIs(t, err, contexty.ErrDuplicateMessageID)
}

func TestDoD_DuplicateMessageIDWithinHistory(t *testing.T) {
	ctx := context.Background()
	engine := contexty.NewEngine()
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
	require.ErrorIs(t, err, contexty.ErrDuplicateMessageID)
}

func TestDoD_StrictSystemExceedsBudget(t *testing.T) {
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{TokenLimit: 15},
				&contexty.FixedEstimator{TokensPerMessage: 20},
			),
		),
	)
	_, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		System: []contexty.Message{
			contexty.TextMessage(contexty.RoleSystem, "large system block"),
		},
	})
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
}

func TestDoD_RedactionRecordsTransformation(t *testing.T) {
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
	)
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "redact-1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "email user@example.com"}},
		}},
	})
	require.NoError(t, err)
	rec, ok := result.Transformations["redact-1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionFormatted, rec.Action)
	assert.Equal(t, contexty.ReasonTransformHook, rec.Reason)
}

func TestDoD_FormatterReplacedByFormatter(t *testing.T) {
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
	oldRec, ok := result.Transformations["mem-old"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionFormatted, oldRec.Action)
	assert.Equal(t, contexty.ReasonReplacedByFormatter, oldRec.Reason)
	newRec, ok := result.Transformations["mem-new"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionPassed, newRec.Action)
}

func TestDoD_SummarizeTransformationByMessageID(t *testing.T) {
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
	require.Len(t, result.Payload.History, 1)
	assert.Equal(t, "summary-1", result.Payload.History[0].ID)
	for _, id := range []string{"h-a", "h-b", "h-c"} {
		rec, ok := result.Transformations[id]
		require.True(t, ok, "missing %s", id)
		assert.Equal(t, contexty.ActionTruncated, rec.Action)
	}
	sumRec, ok := result.Transformations["summary-1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionPassed, sumRec.Action)
}

func cloneMsgs(in []contexty.Message) []contexty.Message {
	out := make([]contexty.Message, len(in))
	for i := range in {
		out[i] = in[i].Clone()
	}
	return out
}
