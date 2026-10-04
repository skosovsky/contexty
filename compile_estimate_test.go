package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestCompile_EstimateReport(t *testing.T) {
	// Arrange: final main/target reports use pinned profiles and actual transformed content.
	ctx := context.Background()
	message := contexty.TextMessage(contexty.RoleUser, "secret")
	message.ID = "m"
	system := contexty.TextMessage(contexty.RoleSystem, "sys")
	system.ID = "sys"
	calls := 0
	counter := fixtureEvidenceEstimator{
		per: func(ctx context.Context, messages []contexty.Message) ([]int, error) {
			calls++
			return contexty.CharTokenEstimator{}.EstimatePerMessage(ctx, messages)
		}, total: func(ctx context.Context, messages []contexty.Message) (int, error) {
			calls++
			return contexty.CharTokenEstimator{}.Estimate(ctx, messages)
		}}
	reporter, err := contexty.NewEstimateReporter(counter, fixtureEstimateProfile(), contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	targetProfile := fixtureEstimateProfile()
	targetProfile.Estimator.ID = "target-estimator"
	targetReporter, err := contexty.NewEstimateReporter(counter, targetProfile, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	mainBudget := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.WindowInputBudget(100, 20, 10)},
		reporter,
	)
	request := contexty.CompileRequest{CompilationID: "reported", History: []contexty.Message{message},
		System: []contexty.Message{system}, Targets: []contexty.CompileTarget{
			{Segments: []contexty.SegmentName{contexty.SegmentHistory},
				Name: "target",
				Budget: contexty.NewBudgetPipeline(
					contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(20)},
					targetReporter,
				),
				Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
					messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "fmt"}}
					return messages, nil
				},
			},
		}}
	engine := fixtureEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureBindings(
			fixtureRecordProfile("target"),
			fixtureBinding(
				contexty.RecordingHook,
				"",
				"",
				0,
			),
			fixtureBinding(contexty.RecordingTargetFormatter, "target", "", 0),
		)),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, mainBudget),
		contexty.WithTransformHooks(fixtureTextTransform{Replacer: func(text string) string { return text + "!" }}),
	)
	// Act.
	result, err := engine.CompileSnapshot(ctx, request)
	// Assert: main report uses wire order and target uses its own estimator/formatter result.
	require.NoError(t, err)
	require.Len(t, result.Estimates, 2)
	require.Equal(t, 11, result.Estimates[0].Report.Total)
	require.Equal(t, 70, result.Estimates[0].Report.EffectiveLimit)
	require.Equal(t, []string{"system", "history", "tools", "memory"}, []string{
		result.Estimates[0].Report.Segments[0].Name, result.Estimates[0].Report.Segments[1].Name,
		result.Estimates[0].Report.Segments[2].Name, result.Estimates[0].Report.Segments[3].Name})
	require.Equal(
		t,
		fixtureRefForMessage(t, result.Payload.History[0]),
		result.Estimates[0].Report.Segments[1].Messages[0],
	)
	require.Equal(t, 3, result.Estimates[1].Report.Total)
	require.Equal(t, targetProfile, result.Estimates[1].Report.Profile)
	require.Equal(t, result.Estimates, result.Manifest.EstimateReports)
	bound, err := result.Manifest.EstimateFor(contexty.ManifestMainOutput, "main")
	require.NoError(t, err)
	require.Equal(t, result.Manifest.Digest, bound.Manifest.Digest)
	require.Nil(t, bound.Report.ManifestRef) // binding envelope avoids a circular digest
	bound.Report.Segments[1].PerMessage[0] = 999
	require.Equal(t, 7, result.Manifest.EstimateReports[0].Report.Segments[1].PerMessage[0])
	result.Estimates[0].Report.Profile.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
	require.Equal(
		t,
		contexty.EstimateEstimated,
		result.Manifest.EstimateReports[0].Report.Profile.Capabilities[contexty.EstimateText],
	)
	wire, err := contexty.EncodeManifest(*result.Manifest)
	require.NoError(t, err)
	manifest, err := contexty.DecodeManifest(wire)
	require.NoError(t, err)
	require.Equal(t, result.Manifest.EstimateReports, manifest.EstimateReports)
	expected, err := contexty.ReplayExpectationFor(manifest)
	require.NoError(t, err)
	expected.Budgets[0].ReportProfile.Model.Revision = "outside mutation"
	require.Equal(t, "pinned", manifest.Budgets[0].ReportProfile.Model.Revision)
	expected, err = contexty.ReplayExpectationFor(manifest)
	require.NoError(t, err)
	accepted, err := result.Record.Accept("host-accept")
	require.NoError(t, err)
	baseline := calls
	replayed, err := contexty.Replay(ctx, accepted, expected, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	require.Equal(t, baseline, calls)
	require.Equal(t, manifest.EstimateReports, replayed.Manifest.EstimateReports)
	manifest.EstimateReports = nil
	require.ErrorIs(t, manifest.Validate(), contexty.ErrMissingEstimateReport)
}

func TestCompile_EstimateFailures(t *testing.T) {
	// Arrange: post-budget formatter expansion must fail under the same profile.
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	message := contexty.TextMessage(contexty.RoleUser, "x")
	message.ID = "m"
	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(2)}, reporter)
	request := contexty.CompileRequest{
		CompilationID: "overflow",
		History:       []contexty.Message{message},
		Targets: []contexty.CompileTarget{
			{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "target", Budget: pipe,
				Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
					messages[0].Parts = []contexty.ContentPart{contexty.TextPart{Text: "expanded"}}
					return messages, nil
				}},
		},
	}
	// Act / Assert: no partial output or misleading successful report escapes.
	result, err := fixtureEngine().CompileSnapshot(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Zero(t, result)
	// Arrange / Act / Assert: reporting works without recording; media strictness also applies before truncation.
	plain, err := fixtureEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe)).CompileSnapshot(
		context.Background(), contexty.CompileRequest{History: []contexty.Message{message}})
	require.NoError(t, err)
	require.Nil(t, plain.Manifest)
	require.Len(t, plain.Estimates, 1)
	require.Equal(t, 1, plain.Estimates[0].Report.Total)
	media := message.Clone()
	media.Parts = []contexty.ContentPart{contexty.ImagePart{URL: "private-url"}}
	result, err = fixtureEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe)).CompileSnapshot(
		context.Background(), contexty.CompileRequest{History: []contexty.Message{media}})
	require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
	require.Zero(t, result)
	// Arrange / Act / Assert: profile/model/encoding mismatches fail before callbacks.
	calls := 0
	deferred := contexty.WithDeferredBlocks(contexty.DeferredBlock{Name: "body", Segment: contexty.SegmentMemory,
		Resolve: func(context.Context) (contexty.DeferredResult, error) {
			calls++
			return contexty.DeferredResult{Messages: nil}, nil
		}})
	for _, mutate := range []func(*contexty.EstimateProfile){
		func(p *contexty.EstimateProfile) { p.Model.Revision = "changed" },
		func(p *contexty.EstimateProfile) { p.Encoding.Revision = "changed" },
		func(p *contexty.EstimateProfile) { p.Estimator.Revision = "changed" },
	} {
		profile := fixtureEstimateProfile()
		mutate(&profile)
		bad, createErr := contexty.NewEstimateReporter(
			contexty.CharTokenEstimator{},
			profile,
			contexty.DefaultJSONSerializer(),
		)
		require.NoError(t, createErr)
		engine := fixtureEngine(deferred, contexty.WithTraceProfile(fixtureTraceProfile()),
			contexty.WithCompileRecording(fixtureRecordProfile()), contexty.WithBudgetPipeline(contexty.SegmentHistory,
				contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)}, bad)))
		_, compileErr := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{CompilationID: "bad"})
		require.ErrorIs(t, compileErr, contexty.ErrStaleEstimate)
		require.Zero(t, calls)
	}
}
