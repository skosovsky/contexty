package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestTrace_LabelPersistenceRoundTrip(t *testing.T) {
	// Arrange: host-owned labels differ; a summary must preserve both sources.
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(data)}, nil
	})
	profile := fixtureTraceProfile()
	profile.Codec.Extensions = registry
	profile.RequireOrigins = true
	profile.Labels = contexty.LabelProjection{Registry: registry, RequiredTypes: []string{"fixture-label"},
		Policy: fixtureLabelPolicy(func(_ context.Context, inputs []contexty.Message,
			_ contexty.Message, descriptor contexty.Descriptor,
		) (contexty.LabelDecision, error) {
			labels := inputs[0].Extensions
			if descriptor.ID == "summarize" {
				labels = []contexty.Extension{fixtureWireExtension{wire: `["external","instruction"]`}}
			}
			return contexty.LabelDecision{Extensions: labels, DecisionRef: "host-label-decision"}, nil
		})}
	a := contexty.TextMessage(contexty.RoleUser, "long external document")
	a.ID = "a"
	a.SourceRefs = []contexty.SourceRef{{ID: "document"}}
	a.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["external"]`}}
	b := contexty.TextMessage(contexty.RoleSystem, "long instruction")
	b.ID = "b"
	b.SourceRefs = []contexty.SourceRef{{ID: "instruction"}}
	b.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["instruction"]`}}
	var origins []contexty.ContentRef
	for _, msg := range []contexty.Message{a, b} {
		ref, err := contexty.MessageContentRef(msg, profile.Codec)
		require.NoError(t, err)
		origins = append(origins, ref)
	}
	engine := fixtureEngine(contexty.WithTraceProfile(profile),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, contexty.NewBudgetPipeline(contexty.BudgetConfig{
			Budget: contexty.EffectiveInputBudget(10),
			Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
				msg := contexty.TextMessage(contexty.RoleAssistant, "sum")
				msg.ID = "summary"
				return msg, nil
			}),
		}, contexty.CharTokenEstimator{})))
	// Act: compile, derive persistence, then reconstruct messages and graph from codecs.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "labels",
		History: []contexty.Message{
			a,
			b,
		},
		Origins: origins,
		Targets: []contexty.CompileTarget{
			{
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Name:     "consumer",
				Budget: contexty.NewBudgetPipeline(
					contexty.BudgetConfig{
						Budget: contexty.EffectiveInputBudget(10),
						Summarizer: stubSummarizer(
							func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
								m := contexty.TextMessage(contexty.RoleAssistant, "sum")
								m.ID = "consumer-summary"
								return m, nil
							},
						),
					},
					contexty.CharTokenEstimator{},
				),
			},
		},
	})
	require.NoError(t, err)
	persisted := fixturePersistenceSegment(t, result, contexty.SegmentHistory)
	codec := contexty.ConversationCodec{Extensions: registry, OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""}}
	wire, err := codec.Encode(contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, persisted))
	require.NoError(t, err)
	restored, err := codec.Decode(wire)
	require.NoError(t, err)
	lineageWire, err := contexty.EncodeLineage(result.Projections["consumer"].Lineage)
	require.NoError(t, err)
	graph, err := contexty.DecodeLineage(lineageWire)
	// Assert: actual persisted summary and target retain host labels and decisions.
	require.NoError(t, err)
	for _, msg := range []contexty.Message{restored.Segment(contexty.SegmentHistory)[0],
		result.Projections["consumer"].Messages[0]} {
		require.Equal(t, "sum", msg.TextContent())
		require.Equal(t, []contexty.SourceRef{{ID: "document"}, {ID: "instruction"}}, msg.SourceRefs)
		require.Equal(t, []contexty.Extension{fixtureWireExtension{wire: `["external","instruction"]`}}, msg.Extensions)
	}
	require.Equal(t, result.Projections["consumer"].Lineage, graph)
	for _, record := range graph.Records {
		if record.Transform.ID == "summarize" && len(record.Outputs) > 0 {
			require.Len(t, record.Inputs, 2)
			require.Equal(t, "host-label-decision", record.DecisionRef)
		}
	}
	require.Equal(t, `["external"]`, a.Extensions[0].(fixtureWireExtension).wire)
}
