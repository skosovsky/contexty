package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestTextReplacement_Identity(t *testing.T) {
	// Arrange: an exact ID is not affected by order or role and keeps non-text data.
	first := contexty.TextMessage(contexty.RoleUser, "first")
	first.ID = "first"
	selected := contexty.TextMessage(contexty.RoleAssistant, "original")
	selected.ID = "selected"
	selected.Parts = append(
		selected.Parts,
		contexty.MediaPart{MIMEType: "application/octet-stream", Data: []byte{1, 2}},
	)
	selected.SourceRefs = []contexty.SourceRef{{ID: "source"}}
	req := contexty.CompileRequest{History: []contexty.Message{selected, first}, Options: []contexty.CompileOption{
		contexty.WithTextReplacement(
			contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "selected", Text: "safe"},
		),
		contexty.WithTextReplacement(
			contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "selected", Text: "final"},
		),
	}}
	// Act.
	result, err := fixtureEngine().CompileSnapshot(context.Background(), req)
	// Assert: ordered replacement, metadata and persistence remain independent.
	require.NoError(t, err)
	require.Equal(t, "final", result.Payload.History[0].TextContent())
	require.Equal(t, selected.Parts[1], result.Payload.History[0].Parts[1])
	require.Equal(t, selected.SourceRefs, result.Payload.History[0].SourceRefs)
	require.Equal(t, selected.Role, result.Payload.History[0].Role)
	require.Equal(t, "first", result.Payload.History[1].TextContent())
	require.Equal(t, selected, fixturePersistenceSegment(t, result, contexty.SegmentHistory)[0])
	require.Equal(t, "original", req.History[0].TextContent())
}

func TestTextReplacement_Failures(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		value contexty.TextReplacement
		want  error
	}{
		{name: "empty-id", value: contexty.TextReplacement{Segment: contexty.SegmentHistory}, want: contexty.ErrInvalidTextReplacement},
		{name: "empty-segment", value: contexty.TextReplacement{MessageID: "input"}, want: contexty.ErrInvalidTextReplacement},
		{name: "unknown-segment", value: contexty.TextReplacement{Segment: "unknown", MessageID: "input"}, want: contexty.ErrInvalidTextReplacement},
		{name: "missing", value: contexty.TextReplacement{Segment: contexty.SegmentHistory, MessageID: "missing"}, want: contexty.ErrMissingReplacementTarget},
		{name: "wrong-segment", value: contexty.TextReplacement{Segment: contexty.SegmentMemory, MessageID: "input"}, want: contexty.ErrMissingReplacementTarget},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange.
			input := contexty.TextMessage(contexty.RoleUser, "input")
			input.ID = "input"
			req := contexty.CompileRequest{History: []contexty.Message{input},
				Options: []contexty.CompileOption{contexty.WithTextReplacement(scenario.value)}}
			// Act.
			result, err := fixtureEngine().CompileSnapshot(context.Background(), req)
			// Assert: exact replacement never falls back to another message.
			require.ErrorIs(t, err, scenario.want)
			require.Zero(t, result)
		})
	}
}

func TestTextReplacement_BudgetRemoval(t *testing.T) {
	// Arrange: the explicitly selected ID is evicted by history budget.
	var history []contexty.Message
	for _, id := range []string{"a", "b", "c"} {
		message := contexty.TextMessage(contexty.RoleUser, id)
		message.ID = id
		history = append(history, message)
	}
	engine := fixtureEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory,
		contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(25)},
			&contexty.FixedEstimator{TokensPerMessage: 10})))
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		History: history, Options: []contexty.CompileOption{contexty.WithTextReplacement(contexty.TextReplacement{
			Segment: contexty.SegmentHistory, MessageID: "a", Text: "safe",
		})},
	})
	// Assert: do not silently patch a surviving ID or claim the requested edit occurred.
	require.ErrorIs(t, err, contexty.ErrMissingReplacementTarget)
	require.Zero(t, result)
	require.Equal(t, "a", history[0].TextContent())
}
