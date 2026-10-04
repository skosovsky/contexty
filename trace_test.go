package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestLineage_Stages(t *testing.T) {
	// Arrange: two independent sources pass through two hooks and a role change.
	ctx := context.Background()
	a := contexty.TextMessage(contexty.RoleSystem, "instruction")
	a.ID = "operator"
	a.SourceRefs = []contexty.SourceRef{{ID: "operator-source"}}
	b := contexty.TextMessage(contexty.RoleUser, "retrieved document")
	b.ID = "document"
	b.SourceRefs = []contexty.SourceRef{{ID: "document-source"}}
	profile := fixtureTraceProfile()
	profile.RequireOrigins = true
	refs := []contexty.ContentRef{fixtureRefForMessage(t, a), fixtureRefForMessage(t, b)}
	hook := contexty.RedactionHook{Replacer: func(text string) string { return text + "!" }}
	formatter := func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
		msgs[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "safe"}}
		return msgs, nil
	}
	engine := contexty.NewEngine(contexty.WithTraceProfile(profile),
		contexty.WithTransformHooks(hook, hook),
		contexty.WithRoleProjectionPolicy(contexty.RoleProjectionFunc(func(contexty.Message) (contexty.Role, error) {
			return contexty.RoleUser, nil
		})),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, contexty.NewBudgetPipeline(
			contexty.BudgetConfig{
				Budget: contexty.EffectiveInputBudget(12),
				Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
					msg := contexty.TextMessage(contexty.RoleAssistant, "sum")
					msg.ID = "summary"
					return msg, nil
				}),
			},
			contexty.CharTokenEstimator{},
		)),
	)
	req := contexty.CompileRequest{
		CompilationID: "trace-run",
		History:       []contexty.Message{a, b},
		Origins:       refs,
		Targets: []contexty.CompileTarget{
			{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "one", Formatter: formatter},
			{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "two"},
		},
	}
	// Act.
	result, err := engine.CompileSnapshot(ctx, req)
	// Assert: every same-ID revision remains, and the summary traces both inputs.
	require.NoError(t, err)
	require.NoError(t, result.Lineage.Validate())
	var summary contexty.LineageRecord
	for _, record := range result.Lineage.Records {
		if record.Transform.ID == "summarize" {
			summary = record
			break
		}
	}
	require.Len(t, summary.Inputs, 2)
	require.Equal(t, "operator", summary.Inputs[0].ID)
	require.Equal(t, "document", summary.Inputs[1].ID)
	require.NotEmpty(t, summary.Inputs[0].Occurrence)
	require.Len(t, result.Transformations["operator"], 5) // source + 2 hooks + role + truncate
	require.Equal(t, "safe", result.Projections["one"].Messages[0].TextContent())
	require.Equal(t, "instruction!!", result.Projections["two"].Messages[0].TextContent())
	require.Equal(t, "sum", result.Payload.History[0].TextContent())
	require.Len(t, result.Payload.History[0].SourceRefs, 2)
	require.NotEqual(t, result.Projections["one"].Lineage, result.Lineage)
	persisted := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, persisted, 1)
	require.Equal(t, "sum", persisted[0].TextContent())
	require.Len(t, persisted[0].SourceRefs, 2)
}

func TestTrace_MissingOrigins(t *testing.T) {
	// Arrange: strict origins cannot be invented for legacy inputs.
	profile := fixtureTraceProfile()
	profile.RequireOrigins = true
	engine := contexty.NewEngine(contexty.WithTraceProfile(profile))
	msg := contexty.TextMessage(contexty.RoleUser, "legacy")
	msg.ID = "legacy"
	req := contexty.CompileRequest{CompilationID: "missing", History: []contexty.Message{msg}}
	// Act.
	_, err := engine.CompileSnapshot(context.Background(), req)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingLineage)

	// Arrange: permissive tracing still exposes the unresolved reference.
	profile.RequireOrigins = false
	engine = contexty.NewEngine(contexty.WithTraceProfile(profile))
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), req)
	// Assert: no false original-source record.
	require.NoError(t, err)
	require.Len(t, result.Lineage.Unresolved, 1)
	require.Equal(t, "legacy", result.Lineage.Unresolved[0].ID)
	require.Equal(t, result.Lineage.Unresolved[0], result.Lineage.Records[0].Inputs[0])
}

func TestTrace_ExplicitMapping(t *testing.T) {
	// Arrange: a host formatter creates a fresh identity.
	profile := fixtureTraceProfile()
	msg := contexty.TextMessage(contexty.RoleUser, "original")
	msg.ID = "source"
	formatter := func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
		msgs[0].ID = "derived"
		return msgs, nil
	}
	req := contexty.CompileRequest{CompilationID: "mapping", History: []contexty.Message{msg}}
	makeEngine := func() *contexty.Engine {
		return contexty.NewEngine(
			contexty.WithTraceProfile(profile),
			contexty.WithSegmentFormatter(contexty.SegmentHistory, formatter),
		)
	}
	// Act / Assert: do not infer all inputs as the origin of a new ID.
	_, err := makeEngine().CompileSnapshot(context.Background(), req)
	require.ErrorIs(t, err, contexty.ErrMissingLineage)
	// Arrange: the host explicitly attributes this output to an available input.
	profile.Mapping = func(_ context.Context, _ string, before, _ []contexty.Message) (map[string][]contexty.ContentRef, error) {
		ref, refErr := contexty.MessageContentRef(before[0], profile.Codec)
		return map[string][]contexty.ContentRef{"derived": {ref}}, refErr
	}
	// Act.
	result, err := makeEngine().CompileSnapshot(context.Background(), req)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, "derived", result.Payload.History[0].ID)
	require.Equal(t, "original", req.History[0].TextContent())
	require.Equal(t, "source", req.History[0].ID)
}
