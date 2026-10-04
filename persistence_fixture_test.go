package contexty_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixturePersistenceSegment(
	t *testing.T,
	result contexty.CompileResult,
	segment contexty.SegmentName,
) []contexty.Message {
	t.Helper()
	state, err := result.DerivePersistenceState(
		contexty.DefaultJSONSerializer(),
		contexty.Descriptor{ID: "", Revision: ""},
	)
	require.NoError(t, err)
	return state.Segment(segment)
}
