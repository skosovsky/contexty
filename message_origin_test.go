package contexty_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestDoD_OriginAndLLMCacheRoundTrip(t *testing.T) {
	t.Parallel()

	reg := contexty.DefaultProvenanceRegistry()
	msg := contexty.TextMessage(contexty.RoleSystem, "rules")
	msg.Origin = &contexty.MessageOrigin{TemplateID: "agents/sales", LayerID: "persona"}
	msg.LLMCache = &contexty.CachePolicyRef{Type: "ephemeral"}

	raw, err := contexty.MarshalMessageJSON(msg, reg)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"origin"`)
	require.Contains(t, string(raw), `"template_id"`)
	require.Contains(t, string(raw), `"llm_cache"`)
	require.NotContains(t, string(raw), `"prompt_origin"`)
	require.NotContains(t, string(raw), `"prompty.layer_ref"`)

	restored, err := contexty.UnmarshalMessageJSON(raw, reg)
	require.NoError(t, err)
	require.NotNil(t, restored.Origin)
	require.Equal(t, "agents/sales", restored.Origin.TemplateID)
	require.Equal(t, "persona", restored.Origin.LayerID)
	require.NotNil(t, restored.LLMCache)
	require.Equal(t, "ephemeral", restored.LLMCache.Type)
}

func TestDoD_MessageEqualIncludesOrigin(t *testing.T) {
	t.Parallel()

	a := contexty.TextMessage(contexty.RoleSystem, "x")
	a.Origin = &contexty.MessageOrigin{TemplateID: "t1", LayerID: "l1"}
	a.LLMCache = &contexty.CachePolicyRef{Type: "ephemeral"}

	b := a.Clone()
	require.True(t, contexty.MessageEqual(a, b))

	b.Origin.LayerID = "other"
	require.False(t, contexty.MessageEqual(a, b))
}

func TestMessage_CloneCopiesOriginAndLLMCache(t *testing.T) {
	t.Parallel()

	msg := contexty.TextMessage(contexty.RoleSystem, "x")
	msg.Origin = &contexty.MessageOrigin{TemplateID: "m", LayerID: "l"}
	msg.LLMCache = &contexty.CachePolicyRef{Type: "ephemeral"}

	cloned := msg.Clone()
	require.NotSame(t, msg.Origin, cloned.Origin)
	require.Equal(t, msg.Origin.TemplateID, cloned.Origin.TemplateID)
	require.NotSame(t, msg.LLMCache, cloned.LLMCache)
	require.Equal(t, msg.LLMCache.Type, cloned.LLMCache.Type)
}
