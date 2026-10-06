package contexty

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyMergePolicy_AppendDefault(t *testing.T) {
	// Arrange.
	t.Parallel()
	existing := []Message{{ID: "a"}, {ID: "b"}}
	incoming := []Message{{ID: "c"}}
	out, err := applyMergePolicy(existing, incoming, PolicyAppend)
	require.NoError(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, []string{"a", "b", "c"}, mergePolicyMessageIDs(out))
}

func TestApplyMergePolicy_UnknownPolicyRejected(t *testing.T) {
	// Arrange.
	t.Parallel()
	existing := []Message{{ID: "a"}}
	incoming := []Message{{ID: "b"}}
	out, err := applyMergePolicy(existing, incoming, MergePolicy("unknown"))
	// Assert: unknown configuration never appends.
	require.ErrorIs(t, err, ErrInvalidCompileConfiguration)
	assert.Nil(t, out)
}

func TestApplyMergePolicy_ReplaceByOrigin_EmptyOriginIncoming(t *testing.T) {
	// Arrange.
	t.Parallel()
	existing := []Message{{
		ID:     "old",
		Origin: &MessageOrigin{TemplateID: "t1", LayerID: "l1"},
	}}
	incoming := []Message{{ID: "new"}}
	out, err := applyMergePolicy(existing, incoming, PolicyReplaceByOrigin)
	require.NoError(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, []string{"old", "new"}, mergePolicyMessageIDs(out))
}

func TestApplyMergePolicy_ReplaceByOrigin_ReplacesTemplate(t *testing.T) {
	// Arrange.
	t.Parallel()
	existing := []Message{
		{ID: "keep", Origin: &MessageOrigin{TemplateID: "other", LayerID: "x"}},
		{ID: "drop", Origin: &MessageOrigin{TemplateID: "agents/sales", LayerID: "v1"}},
	}
	incoming := []Message{{
		ID:     "new",
		Origin: &MessageOrigin{TemplateID: "agents/sales", LayerID: "v2"},
	}}
	out, err := applyMergePolicy(existing, incoming, PolicyReplaceByOrigin)
	require.NoError(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, []string{"keep", "new"}, mergePolicyMessageIDs(out))
}

func TestApplyMergePolicy_ReplaceByOrigin_DropsMultipleSameTemplate(t *testing.T) {
	// Arrange.
	t.Parallel()
	existing := []Message{
		{ID: "drop-a", Origin: &MessageOrigin{TemplateID: "agents/sales", LayerID: "v1"}},
		{ID: "drop-b", Origin: &MessageOrigin{TemplateID: "agents/sales", LayerID: "v2"}},
		{ID: "keep", Origin: &MessageOrigin{TemplateID: "other", LayerID: "x"}},
	}
	incoming := []Message{{
		ID:     "new",
		Origin: &MessageOrigin{TemplateID: "agents/sales", LayerID: "v3"},
	}}
	out, err := applyMergePolicy(existing, incoming, PolicyReplaceByOrigin)
	require.NoError(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, []string{"keep", "new"}, mergePolicyMessageIDs(out))
}

func TestApplyMergePolicy_DeduplicateByLayer(t *testing.T) {
	// Arrange.
	t.Parallel()
	existing := []Message{{
		ID:     "old",
		Origin: &MessageOrigin{TemplateID: "t1", LayerID: "facts"},
	}}
	incoming := []Message{
		{ID: "new", Origin: &MessageOrigin{TemplateID: "t2", LayerID: "facts"}},
		{ID: "extra", Origin: &MessageOrigin{TemplateID: "t3", LayerID: "other"}},
	}
	out, err := applyMergePolicy(existing, incoming, PolicyDeduplicateByLayer)
	require.NoError(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, []string{"old", "new", "extra"}, mergePolicyMessageIDs(out))
}

func TestApplyMergePolicy_MultipleDeferredBlocksSimulated(t *testing.T) {
	// Arrange.
	t.Parallel()
	existing := []Message{{ID: "a"}, {ID: "b"}}
	first, err := applyMergePolicy(existing, []Message{{ID: "c"}}, PolicyAppend)
	require.NoError(t, err)
	second, err := applyMergePolicy(first, []Message{{ID: "d"}}, PolicyAppend)
	require.NoError(t, err)
	// Act / Assert: exercise the contract and check its result.
	assert.Equal(t, []string{"a", "b", "c", "d"}, mergePolicyMessageIDs(second))
}

func mergePolicyMessageIDs(msgs []Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids
}
