package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_Views_NonMutating(t *testing.T) {
	// Arrange.
	ctx := context.Background()
	snap := contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "secret@email.com"),
	})
	before := snap.Segment(contexty.SegmentHistory)[0].TextContent()
	// Act.
	xml, err := contexty.Render(ctx, snap, contexty.ViewLLMXML)
	// Assert.
	require.NoError(t, err)
	flat, err := contexty.Render(ctx, snap, contexty.ViewFlatClassifier)
	require.NoError(t, err)
	assert.NotEqual(t, xml, flat)
	assert.Equal(t, before, snap.Segment(contexty.SegmentHistory)[0].TextContent())
}
