package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestCanonical_Content(t *testing.T) {
	// Arrange: codec output differs in object ordering and whitespace, not content.
	msg := contexty.TextMessage(contexty.RoleUser, "same")
	msg.ID = "m"
	msg.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"z":9007199254740993,"a":"label"}`}}
	a, err := contexty.MessageContentRef(msg, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	msg.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"a":"label", "z":9007199254740993}`}}
	// Act.
	b, err := contexty.MessageContentRef(msg, contexty.DefaultJSONSerializer())
	// Assert: canonical hashing ignores key order but preserves large integers.
	require.NoError(t, err)
	require.Equal(t, a, b)
	msg.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"a":"label", "z":9007199254740992}`}}
	c, err := contexty.MessageContentRef(msg, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.NotEqual(t, a.Digest, c.Digest)
	msg.LLMCache = &contexty.CachePolicyRef{Type: "changed-wire-hint"}
	d, err := contexty.MessageContentRef(msg, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.NotEqual(t, c.Digest, d.Digest)
}

func TestLineage_Validation(t *testing.T) {
	// Arrange: two sources produce a summary, then the same ID is redacted.
	a := fixtureRef(t, "a", "source A")
	b := fixtureRef(t, "b", "source B")
	summary := fixtureRef(t, "summary", "both sources")
	safe := fixtureRef(t, "summary", "safe summary")
	graph := contexty.Lineage{Records: []contexty.LineageRecord{
		{ID: "merge", Transform: contexty.Descriptor{ID: "summarize", Revision: "pinned"},
			Inputs: []contexty.ContentRef{a, b}, Outputs: []contexty.ContentRef{summary}},
		{ID: "redact", Transform: contexty.Descriptor{ID: "redact", Revision: "pinned"},
			Inputs: []contexty.ContentRef{summary}, Outputs: []contexty.ContentRef{safe}},
	}}
	// Act: codec round-trip.
	wire, err := json.Marshal(graph)
	require.NoError(t, err)
	var restored contexty.Lineage
	require.NoError(t, json.Unmarshal(wire, &restored))
	// Assert: both source refs and same-ID content revisions survive.
	require.NoError(t, restored.Validate())
	require.Equal(t, graph, restored)
	copyGraph := graph.Clone()
	copyGraph.Records[0].Inputs[0] = safe
	require.Equal(t, a, graph.Records[0].Inputs[0])

	// Arrange/Act/Assert: duplicate invocation identity must not silently replace.
	_, err = graph.WithRecord(graph.Records[0])
	require.ErrorIs(t, err, contexty.ErrDuplicateTransform)
	// Arrange/Act/Assert: a back-edge cannot produce an earlier content revision.
	_, err = graph.WithRecord(contexty.LineageRecord{ID: "cycle",
		Transform: contexty.Descriptor{ID: "reverse", Revision: "pinned"},
		Inputs:    []contexty.ContentRef{safe}, Outputs: []contexty.ContentRef{a}})
	require.ErrorIs(t, err, contexty.ErrInvalidLineage)
	// Arrange/Act/Assert: invalid refs/descriptors fail explicitly.
	require.ErrorIs(t, (contexty.ContentRef{ID: "missing-digest"}).Validate(), contexty.ErrInvalidContentRef)
	require.ErrorIs(t, (contexty.Descriptor{ID: "unpinned"}).Validate(), contexty.ErrInvalidDescriptor)
}

func TestLabel_Policy(t *testing.T) {
	// Arrange: a host label codec and two distinct sources.
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(data)}, nil
	})
	a := contexty.TextMessage(contexty.RoleUser, "untrusted document")
	a.SourceRefs = []contexty.SourceRef{{ID: "document"}}
	a.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"trust":"external"}`}}
	b := contexty.TextMessage(contexty.RoleSystem, "trusted instruction")
	b.SourceRefs = []contexty.SourceRef{{ID: "instruction"}}
	output := contexty.TextMessage(contexty.RoleSystem, "summary")
	projection := contexty.LabelProjection{Registry: registry, RequiredTypes: []string{"fixture-label"},
		Policy: fixtureLabelPolicy(func(_ context.Context, inputs []contexty.Message,
			_ contexty.Message, _ contexty.Descriptor,
		) (contexty.LabelDecision, error) {
			// Mutating a policy's input must not mutate the caller's source.
			inputs[0].Parts = nil
			return contexty.LabelDecision{Extensions: a.Extensions}, nil
		})}
	descriptor := contexty.Descriptor{ID: "summarize", Revision: "pinned"}
	// Act: role system in the output doesn't authorize a trust upgrade.
	result, decision, err := projection.Project(context.Background(), []contexty.Message{a, b}, output, descriptor)
	// Assert.
	require.NoError(t, err)
	require.Empty(t, decision)
	require.Len(t, result.SourceRefs, 2)
	require.Equal(t, a.Extensions, result.Extensions)
	require.Equal(t, "untrusted document", a.TextContent())

	// Arrange: host declares an upgrade without an authorization reference.
	projection.Policy = fixtureLabelPolicy(func(context.Context, []contexty.Message,
		contexty.Message, contexty.Descriptor,
	) (contexty.LabelDecision, error) {
		return contexty.LabelDecision{Extensions: a.Extensions, Upgrade: true}, nil
	})
	// Act / Assert.
	_, _, err = projection.Project(context.Background(), []contexty.Message{a}, output, descriptor)
	require.ErrorIs(t, err, contexty.ErrInvalidTrustUpgrade)

	// Arrange / Act / Assert: unknown codec is rejected before projecting labels.
	projection.Registry = contexty.NewExtensionRegistry()
	_, _, err = projection.Project(context.Background(), []contexty.Message{a}, output, descriptor)
	require.ErrorIs(t, err, contexty.ErrMissingLabelCodec)
	projection.Registry = registry
	projection.Policy = fixtureLabelPolicy(func(context.Context, []contexty.Message,
		contexty.Message, contexty.Descriptor,
	) (contexty.LabelDecision, error) {
		return contexty.LabelDecision{}, contexty.ErrLabelConflict
	})
	_, _, err = projection.Project(context.Background(), []contexty.Message{a}, output, descriptor)
	require.ErrorIs(t, err, contexty.ErrLabelConflict)
}
