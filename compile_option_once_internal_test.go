package contexty

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompile_OptionsEvaluatedOnce(t *testing.T) {
	// Arrange: a host option's second evaluation would produce a different patch.
	calls := 0
	option := CompileOption(func(options *compileOptions) {
		calls++
		WithTextReplacement(
			TextReplacement{Segment: SegmentHistory, MessageID: "input", Text: fmt.Sprintf("evaluation-%d", calls)},
		)(
			options,
		)
	})
	input := TextMessage(RoleUser, "original")
	input.ID = "input"
	engine := NewEngine()
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), CompileRequest{
		History: []Message{input}, Options: []CompileOption{option},
		Targets: []CompileTarget{
			{Name: "one", Segments: []SegmentName{SegmentHistory}},
			{Name: "two", Segments: []SegmentName{SegmentHistory}},
		},
	})
	// Assert: both compile phases use the exact same evaluated option configuration.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "evaluation-1", result.Payload.History[0].TextContent())
	for _, projection := range result.Projections {
		require.Equal(t, "evaluation-1", projection.Messages[0].TextContent())
	}
	require.Equal(t, "original", input.TextContent())
}

func TestTextReplacement_OptionDigest(t *testing.T) {
	// Arrange: every behavior-affecting replacement field and ordering is pinned.
	a := TextReplacement{Segment: SegmentHistory, MessageID: "a", Text: "private-a"}
	b := TextReplacement{Segment: SegmentHistory, MessageID: "b", Text: "private-b"}
	baseline, err := compileOptionsRef(
		applyCompileOptions([]CompileOption{WithTextReplacement(a), WithTextReplacement(b)}),
	)
	require.NoError(t, err)
	for _, scenario := range []string{"id", "segment", "replacement-value", "order"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange.
			changed := a
			switch scenario {
			case "id":
				changed.MessageID = "changed"
			case "segment":
				changed.Segment = SegmentMemory
			case "replacement-value":
				changed.Text = "changed-private"
			}
			options := []CompileOption{WithTextReplacement(changed), WithTextReplacement(b)}
			if scenario == "order" {
				options[0], options[1] = options[1], options[0]
			}
			// Act.
			ref, digestErr := compileOptionsRef(applyCompileOptions(options))
			// Assert: canonical digest cannot discard a semantically relevant change.
			require.NoError(t, digestErr)
			require.NotEqual(t, baseline.Digest, ref.Digest)
		})
	}
}
