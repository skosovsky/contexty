package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_TextReplacement_ExactLastUserID(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
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
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "u2", Text: "REDACTED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	last := result.Payload.History[len(result.Payload.History)-1]
	assert.Equal(t, "REDACTED", last.TextContent())
}

func TestAcceptance_TextReplacement_NotMutatesInput(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	req := contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "u1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "original"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "u1", Text: "REDACTED"},
			),
		},
	}
	engine := fixtureEngine()
	// Act.
	result, err := engine.CompileSnapshot(ctx, req)
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
	assert.Equal(t, "original", result.Source.History[0].TextContent())
	assert.Equal(t, "original", req.History[0].TextContent())
}

func TestAcceptance_TextReplacement_EmptySegmentMissingTarget(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "missing", Text: "REDACTED"},
			),
		},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingReplacementTarget)
	require.Zero(t, result)
}

func TestAcceptance_TextReplacement_MissingIDDoesNotRetarget(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "a1",
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "only assistant"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "missing", Text: "REDACTED"},
			),
		},
	})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingReplacementTarget)
	require.Zero(t, result)
}

func TestAcceptance_TextReplacement_PendingTurn(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
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
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "p1", Text: "REDACTED"},
			),
		},
	}
	// Act.
	result, err := engine.CompileSnapshot(ctx, req)
	// Assert.
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

func TestAcceptance_TextReplacement_PreBudgetSkipsHistory(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "secret@mail.com"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "h1", Text: "REDACTED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	// post-budget pass applies patch
	assert.Equal(t, "REDACTED", result.Payload.History[0].TextContent())
}

func TestAcceptance_TextReplacement_MultipleExplicitIDs(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
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
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "u1", Text: "PATCHED"},
			),
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "u2", Text: "PATCHED"},
			),
		},
	})
	// Assert.
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

func TestAcceptance_TextReplacement_ExplicitFirstID(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
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
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "u1", Text: "PATCHED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "PATCHED", result.Payload.History[0].TextContent())
	assert.Equal(t, "last", result.Payload.History[2].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 3)
	assert.Equal(t, "first", proj[0].TextContent())
	assert.Equal(t, "last", proj[2].TextContent())
}

func TestAcceptance_TextReplacement_ExplicitIDWithoutDefaults(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
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
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "u1", Text: "PATCHED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "PATCHED", result.Payload.History[0].TextContent())
	assert.Equal(t, "second", result.Payload.History[1].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 2)
	assert.Equal(t, "first", proj[0].TextContent())
}

func TestAcceptance_TextReplacement_ToolsSegment(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		Tools: []contexty.Message{{
			ID:    "tool-1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "tool payload"}},
		}},
		Options: []contexty.CompileOption{
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentTools, MessageID: "tool-1", Text: "PATCHED"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "PATCHED", result.Payload.Tools[0].TextContent())
	proj := result.DerivePersistenceProjection(contexty.SegmentTools)
	require.Len(t, proj, 1)
	assert.Equal(t, "tool payload", proj[0].TextContent())
}

func TestAcceptance_TextReplacement_PostBudgetSkipsNonHistory(t *testing.T) {
	// Arrange.
	t.Parallel()
	ctx := context.Background()
	engine := fixtureEngine()
	// Act.
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
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentMemory, MessageID: "mem-1", Text: "MEM-PATCH"},
			),
			contexty.WithTextReplacement(
				contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "h1", Text: "HIST-PATCH"},
			),
		},
	})
	// Assert.
	require.NoError(t, err)
	assert.Equal(t, "MEM-PATCH", result.Payload.Memory[0].TextContent())
	assert.Equal(t, "HIST-PATCH", result.Payload.History[0].TextContent())
	memRec, ok := result.Transformations["mem-1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ReasonTextReplacement, memRec.Final().Reason)
}
