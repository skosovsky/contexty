package contexty

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManifest_ArtifactRevisionInputs(t *testing.T) {
	// Arrange: one identity, two immutable content revisions.
	first, err := ArtifactContentRef(NewMemoryBlock("shared", TextPayload("first")).ContextArtifact)
	require.NoError(t, err)
	second, err := ArtifactContentRef(NewMemoryBlock("shared", TextPayload("second")).ContextArtifact)
	require.NoError(t, err)
	refs := []ContentRef{first, second}
	// Act / Assert: artifact evidence differs from a unique-ID message segment.
	require.NoError(t, validateManifestSegments([]ManifestSegment{{Name: manifestArtifactsSegment, Messages: refs}}))
	require.ErrorIs(
		t,
		validateManifestSegments([]ManifestSegment{{Name: "history", Messages: refs}}),
		ErrInvalidManifest,
	)
	require.ErrorIs(t, validateManifestArtifactInputs([]ContentRef{first, first}), ErrInvalidManifest)
	first.Occurrence = "unexpected"
	require.ErrorIs(t, validateManifestArtifactInputs([]ContentRef{first}), ErrInvalidManifest)
}
