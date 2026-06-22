package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestLineage_RenderedOutput(t *testing.T) {
	// Arrange: a view renders content from two segments into a separate typed node.
	a := contexty.TextMessage(contexty.RoleSystem, "instruction")
	a.ID = "a"
	a.SourceRefs = []contexty.SourceRef{{ID: "instruction-source"}}
	b := contexty.TextMessage(contexty.RoleUser, "question")
	b.ID = "b"
	b.SourceRefs = []contexty.SourceRef{{ID: "question-source"}}
	profile := fixtureTraceProfile()
	profile.RequireOrigins = true
	request := contexty.CompileRequest{
		CompilationID: "view",
		System:        []contexty.Message{a},
		History: []contexty.Message{
			b,
		},
		Origins: []contexty.ContentRef{fixtureRefForMessage(t, a), fixtureRefForMessage(t, b)},
		Targets: []contexty.CompileTarget{{Name: "xml", View: string(contexty.ViewLLMXML)}},
	}
	// Act.
	result, err := contexty.NewEngine(contexty.WithTraceProfile(profile)).CompileSnapshot(context.Background(), request)
	// Assert: rendering has its own identity, source links, policy and branch.
	require.NoError(t, err)
	projection := result.Projections["xml"]
	require.NotNil(t, projection.Rendered)
	require.Equal(t, projection.Text, projection.Rendered.Message.TextContent())
	require.Equal(t, profile.Stages["render"], projection.Rendered.Renderer)
	require.Len(t, projection.Rendered.Message.SourceRefs, 2)
	require.NoError(t, projection.Lineage.Validate())
	var found bool
	for _, record := range projection.Lineage.Records {
		if record.Stage == "render" && len(record.Outputs) > 0 {
			found = true
			require.Len(t, record.Inputs, 2)
			require.Equal(t, projection.Rendered.Ref, record.Outputs[0])
		}
	}
	require.True(t, found)
	for _, record := range result.Lineage.Records {
		require.NotEqual(t, "render", record.Stage)
	}
	// Arrange / Act / Assert: rendering lineage follows wire order, not snapshot order.
	tool := contexty.TextMessage(contexty.RoleUser, "tool instruction")
	tool.ID = "tool"
	memory := contexty.TextMessage(contexty.RoleUser, "memory")
	memory.ID = "memory"
	request.Tools, request.Memory = []contexty.Message{tool}, []contexty.Message{memory}
	request.Origins = append(request.Origins, fixtureRefForMessage(t, tool), fixtureRefForMessage(t, memory))
	result, err = contexty.NewEngine(contexty.WithTraceProfile(profile)).CompileSnapshot(context.Background(), request)
	require.NoError(t, err)
	for _, record := range result.Projections["xml"].Lineage.Records {
		if record.Stage == "render" && len(record.Outputs) > 0 {
			var ids []string
			for _, input := range record.Inputs {
				ids = append(ids, input.ID)
			}
			require.Equal(t, []string{"a", "b", "tool", "memory"}, ids)
		}
	}
	// Arrange / Act / Assert: empty rendering does not invent an origin.
	request.System, request.History, request.Memory, request.Tools, request.Origins = nil, nil, nil, nil, nil
	result, err = contexty.NewEngine(contexty.WithTraceProfile(profile)).CompileSnapshot(context.Background(), request)
	require.NoError(t, err)
	require.Empty(t, result.Projections["xml"].Lineage.Unresolved)
	delete(profile.Stages, "render")
	_, err = contexty.NewEngine(contexty.WithTraceProfile(profile)).CompileSnapshot(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrInvalidDescriptor)
}

func TestManifest_RoundTrip(t *testing.T) {
	// Arrange: pinned identities, main and named output limits, and loaded revision.
	ctx := context.Background()
	msg := contexty.TextMessage(contexty.RoleUser, "PRIVATE-PAYLOAD")
	msg.ID = "m"
	store := contexty.NewMemoryConversationStateStore()
	err := store.ApplyDelta(ctx, "thread", 0, contexty.ConversationDelta{Operation: contexty.DeltaReplaceSegment,
		Segment: contexty.SegmentHistory, Messages: []contexty.Message{msg}})
	require.NoError(t, err)
	profile := fixtureBindings(
		fixtureRecordProfile("small", "xml"),
		fixtureBinding(contexty.RecordingViewRenderer, "xml", "", 0),
	)
	engine := contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(profile), contexty.WithStateStore(store), contexty.WithConversationID("thread"),
		contexty.WithBudgetPipeline(
			contexty.SegmentHistory,
			contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
				contexty.CharTokenEstimator{},
			),
		))
	request := contexty.CompileRequest{CompilationID: "manifest", Targets: []contexty.CompileTarget{
		{Name: "xml", View: string(contexty.ViewLLMXML)},
		{
			Name: "small",
			Budget: contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(50)},
				contexty.CharTokenEstimator{},
			),
		},
	}}
	// Act: build locally and round-trip the complete descriptor-only record.
	result, err := engine.Compile(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, result.Manifest)
	wire, err := contexty.EncodeManifest(*result.Manifest)
	require.NoError(t, err)
	restored, err := contexty.DecodeManifest(wire)
	// Assert: ordered inputs/outputs, actual budgets and source revision survive.
	require.NoError(t, err)
	require.Equal(t, *result.Manifest, restored)
	require.Equal(t, int64(1), restored.SourceRevision)
	require.Equal(t, int64(1), result.NormalizedSnapshot.Version())
	require.Equal(
		t,
		[]contexty.ManifestBudget{
			{
				Kind:            contexty.ManifestMainOutput,
				Target:          "main",
				TokenLimit:      100,
				EstimatedTokens: len("PRIVATE-PAYLOAD"),
				Request:         contexty.EffectiveInputBudget(100),
				ReportProfile:   nil,
				Estimator: contexty.EstimatorIdentity{Descriptor: contexty.Descriptor{
					ID: "contexty/estimate/characters", Revision: "contract"}},
				Truncation: contexty.TruncationProfile{Descriptor: contexty.Descriptor{
					ID: "contexty/truncate/drop-head", Revision: "contract"},
					DropHead: &contexty.DropHeadConfig{KeepTurnAtomicity: contexty.BoolPtr(true)}},
			},
			{
				Kind:            contexty.ManifestTargetOutput,
				Target:          "small",
				TokenLimit:      50,
				EstimatedTokens: len("PRIVATE-PAYLOAD"),
				Request:         contexty.EffectiveInputBudget(50),
				ReportProfile:   nil,
				Estimator: contexty.EstimatorIdentity{Descriptor: contexty.Descriptor{
					ID: "contexty/estimate/characters", Revision: "contract"}},
				Truncation: contexty.TruncationProfile{Descriptor: contexty.Descriptor{
					ID: "contexty/truncate/drop-head", Revision: "contract"},
					DropHead: &contexty.DropHeadConfig{KeepTurnAtomicity: contexty.BoolPtr(true)}},
			},
		},
		restored.Budgets,
	)
	require.Equal(t, "main", restored.Outputs[0].Name)
	require.Equal(t, "small", restored.Outputs[1].Name)
	require.Equal(t, "xml", restored.Outputs[2].Name)
	require.Equal(t, fixtureRefForMessage(t, msg), restored.Inputs[1].Messages[0])
	require.NotContains(t, string(wire), "PRIVATE-PAYLOAD")
	require.NotContains(t, string(wire), "Source")
	require.Equal(t, profile, restored.Profile)
	// Arrange / Act / Assert: tampered content cannot retain the old self-digest.
	restored.Budgets[0].TokenLimit++
	_, err = contexty.EncodeManifest(restored)
	require.ErrorIs(t, err, contexty.ErrInvalidBudgetRequest)
}

func TestManifest_IdentityAndPolicies(t *testing.T) {
	// Arrange: two identical compiles with pinned deterministic execution.
	msg := contexty.TextMessage(contexty.RoleUser, "same")
	msg.ID = "m"
	request := contexty.CompileRequest{CompilationID: "id", History: []contexty.Message{msg}, SourceRevision: 7,
		Targets: []contexty.CompileTarget{{Name: "main"}}}
	profile := fixtureRecordProfile("main")
	compile := func(record contexty.RecordProfile, limit int) (contexty.CompileResult, error) {
		return contexty.NewEngine(contexty.WithTraceProfile(fixtureTraceProfile()), contexty.WithCompileRecording(record),
			contexty.WithBudgetPipeline(contexty.SegmentHistory,
				contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(limit)}, contexty.CharTokenEstimator{}),
			),
		).
			CompileSnapshot(context.Background(), request)
	}
	// Act.
	a, err := compile(profile, 10)
	require.NoError(t, err)
	b, err := compile(profile, 10)
	// Assert: no clock/map order/callback identity contaminates the record.
	require.NoError(t, err)
	require.Equal(t, a.Manifest.Digest, b.Manifest.Digest)
	require.Equal(t, int64(7), a.Manifest.SourceRevision)
	require.Equal(t, "main", a.Manifest.Outputs[1].Name)
	require.Equal(t, contexty.ManifestTargetOutput, a.Manifest.Outputs[1].Kind)
	profile.Prompt.Revision = "changed"
	b, err = compile(profile, 10)
	require.NoError(t, err)
	require.NotEqual(t, a.Manifest.Digest, b.Manifest.Digest)
	b, err = compile(fixtureRecordProfile("main"), 11)
	require.NoError(t, err)
	require.NotEqual(t, a.Manifest.Digest, b.Manifest.Digest)
	// Arrange / Act / Assert: an undeclared target or untraced compile cannot record.
	_, err = compile(fixtureRecordProfile(), 10)
	require.ErrorIs(t, err, contexty.ErrInvalidDescriptor)
	_, err = contexty.NewEngine(contexty.WithCompileRecording(profile)).CompileSnapshot(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrInvalidManifest)
	_, err = contexty.DecodeManifest(json.RawMessage(`{"id":"forged"}`))
	require.Error(t, err)
}

func TestManifest_CurrentTurnAndDependencies(t *testing.T) {
	// Arrange: current-turn raw/safe/persisted differ, and resolver identity is opaque.
	raw := contexty.TextMessage(contexty.RoleUser, "PRIVATE-RAW")
	raw.ID = "turn"
	safe := contexty.TextMessage(contexty.RoleUser, "safe")
	safe.ID = raw.ID
	turn := contexty.NewCurrentTurn(raw).WithPromptSafe(safe)
	profile := fixtureTraceProfile()
	profile.Stages["deferred"] = contexty.Descriptor{ID: "host-selected-resolver", Revision: "pinned"}
	resolved := contexty.TextMessage(contexty.RoleUser, "PRIVATE-RESOLVED-BODY")
	resolved.ID = "resource-body"
	artifact := contexty.NewMemoryBlock("artifact", contexty.TextPayload("PRIVATE-ARTIFACT-BODY")).ContextArtifact
	request := contexty.CompileRequest{CompilationID: "deps", TurnID: "current", CurrentTurn: &turn,
		Artifacts: []contexty.ContextArtifact{artifact}}
	calls := 0
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(profile),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingResolver, "", "", 0)),
		),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{Name: "selected", Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				calls++
				return contexty.DeferredResult{Messages: []contexty.Message{resolved}}, nil
			}}),
	)
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: dependencies are identified by stage, not guessed from descriptor text.
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Len(t, result.Manifest.ResolvedDependencies, 1)
	ref := result.Manifest.ResolvedDependencies[0]
	require.Equal(t, "resource-body", ref.ID)
	require.Equal(t, fixtureRefForMessage(t, resolved).Digest, ref.Digest)
	require.Equal(t, contexty.CoverageIncluded,
		fixtureCoverage(t, *result.Manifest, "main", "resolved", "resource-body").Status)
	require.Equal(t, "current", result.Manifest.TurnID)
	inputRefs := make(map[string][]contexty.ContentRef)
	for _, segment := range result.Manifest.Inputs {
		inputRefs[segment.Name] = segment.Messages
	}
	require.Equal(t, []contexty.ContentRef{fixtureRefForMessage(t, raw)}, inputRefs["current/raw"])
	require.Equal(t, []contexty.ContentRef{fixtureRefForMessage(t, safe)}, inputRefs["current/prompt"])
	require.Equal(t, inputRefs["current/raw"], inputRefs["current/persistence"])
	artifactRef, err := contexty.ArtifactContentRef(artifact)
	require.NoError(t, err)
	require.Equal(t, []contexty.ContentRef{artifactRef}, inputRefs["artifacts"])
	require.Equal(t, []contexty.ContentRef{artifactRef}, result.Manifest.Artifacts)
	wire, err := contexty.EncodeManifest(*result.Manifest)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "PRIVATE-")
	// Arrange / Act / Assert: inherited lineage isn't a resource resolution this run.
	next := contexty.CompileRequest{CompilationID: "deps-next", Lineage: result.Lineage,
		Memory: result.Payload.Memory}
	noResolveProfile := fixtureRecordProfile()
	noResolveProfile.Pipeline.Revision = "without-resolution"
	nextResult, nextErr := contexty.NewEngine(contexty.WithTraceProfile(profile),
		contexty.WithCompileRecording(noResolveProfile)).CompileSnapshot(context.Background(), next)
	require.NoError(t, nextErr)
	require.Empty(t, nextResult.Manifest.ResolvedDependencies)
	_, err = contexty.DecodeManifest(append(wire, []byte(` {}`)...))
	require.ErrorIs(t, err, contexty.ErrInvalidManifest)
	// Arrange / Act / Assert: invalid recording configuration causes no resolution.
	bad := fixtureRecordProfile()
	bad.Model.Revision = ""
	engine = contexty.NewEngine(contexty.WithTraceProfile(profile), contexty.WithCompileRecording(bad),
		contexty.WithDeferredBlocks(contexty.DeferredBlock{Name: "selected", Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				calls++
				return contexty.DeferredResult{Messages: nil}, nil
			}}))
	_, err = engine.CompileSnapshot(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrInvalidDescriptor)
	require.Equal(t, 1, calls)
}
