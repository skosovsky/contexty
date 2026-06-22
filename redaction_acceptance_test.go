package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Redaction_ThroughCompile(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	store := contexty.NewMemoryConversationStateStore()
	s0, _ := loadState(ctx, store, "t")
	require.NoError(
		t,
		updateSegment(ctx, store, "t", s0.Version(), contexty.SegmentHistory, []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "reach me at a@b.com"),
		}),
	)
	engine := contexty.NewEngine(
		contexty.WithConversationID("t"),
		contexty.WithStateStore(store),
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
	)
	// Act.
	result, err := engine.Compile(ctx, contexty.CompileRequest{})
	// Assert.
	require.NoError(t, err)
	payload := result.Payload
	require.Len(t, payload.History, 1)
	assert.Equal(t, "reach me at [REDACTED]", payload.History[0].TextContent())
	snap, _ := loadState(ctx, store, "t")
	assert.Equal(t, "reach me at a@b.com", snap.Segment(contexty.SegmentHistory)[0].TextContent())
}

func TestAcceptance_Redaction_RecordsTransformation(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	engine := contexty.NewEngine(
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "redact-1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "email user@example.com"}},
		}},
	})
	// Assert.
	require.NoError(t, err)
	rec, ok := result.Transformations["redact-1"]
	require.True(t, ok)
	assert.Equal(t, contexty.ActionFormatted, rec.Final().Action)
	assert.Equal(t, contexty.ReasonTransformHook, rec.Final().Reason)
}
