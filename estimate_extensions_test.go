package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestEstimate_Extensions(t *testing.T) {
	for _, mode := range []string{"strict", "fallback", "metadata", "content"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange: host values carry content or explicit non-wire metadata.
			message := contexty.TextMessage(contexty.RoleUser, "abc")
			message.ID = "with-extension"
			message.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"body":"private-media"}`}}
			profile := fixtureExtensionProfile(mode)
			calls := 0
			weights := func(messages []contexty.Message) []int {
				out := make([]int, len(messages))
				for i, message := range messages {
					out[i] = len(message.TextContent()) + 50*len(message.Extensions)
				}
				return out
			}
			counter := fixtureEvidenceEstimator{
				per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
					calls++
					return weights(messages), nil
				},
				total: func(_ context.Context, messages []contexty.Message) (int, error) {
					calls++
					total := 0
					for _, weight := range weights(messages) {
						total += weight
					}
					return total, nil
				},
			}
			reporter, err := contexty.NewEstimateReporter(counter, profile, fixtureExtensionEstimateCodec())
			require.NoError(t, err)
			request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(20),
				Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}}
			// Act.
			report, err := reporter.Report(context.Background(), request)
			// Assert: unknown is not zero; metadata exclusion is explicit and pinned.
			if mode == "strict" {
				require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
				require.Zero(t, report)
				require.Zero(t, calls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, map[string]int{"fallback": 14, "metadata": 3, "content": 53}[mode], report.Total)
			require.Equal(t, [][]string{{"fixture-label"}}, report.Segments[0].ExtensionTypes)
			require.Equal(t, map[string]contexty.EstimateQuality{"fallback": contexty.EstimateUnknown,
				"metadata": contexty.EstimateEstimated, "content": contexty.EstimateEstimated}[mode], report.Quality)
			require.Equal(
				t,
				map[string]string{"content": contexty.ReasonTokenBudgetExceeded}[mode],
				report.OverflowReason,
			)
			require.Len(t, message.Extensions, 1)
			wire, err := contexty.EncodeEstimateReport(report)
			require.NoError(t, err)
			restored, err := contexty.DecodeEstimateReport(wire)
			require.NoError(t, err)
			require.Equal(t, report, restored)
			if mode == "metadata" {
				fixtureCheckExtensionProfileCopy(t, reporter, counter, profile, request, report)
			}
		})
	}
}

func TestEstimate_ExtensionFailures(t *testing.T) {
	// Arrange: a registered decoder is mandatory even for metadata-only values.
	message := contexty.TextMessage(contexty.RoleUser, "abc")
	message.ID = "extension"
	message.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"body":"media"}`}}
	profile := fixtureEstimateProfile()
	profile.Extensions = map[string]contexty.EstimateExtensionPolicy{"fixture-label": fixtureExtensionPolicy(true)}
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		profile,
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(100),
		Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}}
	// Act.
	report, err := reporter.Report(context.Background(), request)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrMissingEstimateExtensionCodec)
	require.Zero(t, report)
	reporter, err = contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		profile,
		fixtureExtensionEstimateCodec(),
	)
	require.NoError(t, err)
	report, err = reporter.Report(context.Background(), request)
	require.NoError(t, err)
	bad := report
	bad.Segments[0].ExtensionTypes = nil
	require.ErrorIs(t, bad.Validate(), contexty.ErrInvalidEstimateReport)
	message.Extensions = []contexty.Extension{nil}
	request.Segments[0].Messages = []contexty.Message{message}
	report, err = reporter.Report(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrInvalidEstimateReport)
	require.Zero(t, report)
	profile.Extensions["fixture-label"] = contexty.EstimateExtensionPolicy{}
	_, err = contexty.NewEstimateReporter(contexty.CharTokenEstimator{}, profile, fixtureExtensionEstimateCodec())
	require.ErrorIs(t, err, contexty.ErrInvalidDescriptor)
}

func TestCompile_ExtensionEstimate(t *testing.T) {
	// Arrange: opt-in fallback is counting policy, not a content projection.
	message := contexty.TextMessage(contexty.RoleSystem, "abc")
	message.ID = "extension"
	message.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"body":"private-media"}`}}
	profile := fixtureExtensionProfile("fallback")
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		profile,
		fixtureExtensionEstimateCodec(),
	)
	require.NoError(t, err)
	engine := fixtureEngine(
		contexty.WithBudgetPipeline(
			contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(20)}, reporter),
		),
	)
	// Act.
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{System: []contexty.Message{message}},
	)
	// Assert: original extension remains, report contains its exact type and cost.
	require.NoError(t, err)
	require.Len(t, compiled.Payload.System[0].Extensions, 1)
	require.Equal(t, message.Extensions, compiled.Source.System[0].Extensions)
	require.Equal(t, message.Extensions, compiled.Payload.System[0].Extensions)
	require.Equal(t, 14, compiled.Estimates[0].Report.Total)
	require.Equal(t, contexty.EstimateUnknown, compiled.Estimates[0].Report.Quality)
	require.Equal(t, [][]string{{"fixture-label"}}, compiled.Estimates[0].Report.Segments[0].ExtensionTypes)
}

func TestUnclassified_ExtensionCannotClaimKnownCost(t *testing.T) {
	// Arrange: a generic capability is not a classification decision for this type.
	message := contexty.TextMessage(contexty.RoleUser, "abc")
	message.ID = "extension"
	message.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"body":"media"}`}}
	profile := fixtureExtensionProfile("fallback")
	profile.Capabilities[contexty.EstimateExtension] = contexty.EstimateEstimated
	counter := fixtureEvidenceEstimator{
		per: func(ctx context.Context, messages []contexty.Message) ([]int, error) {
			for _, message := range messages {
				require.Empty(t, message.Extensions)
			}
			return contexty.CharTokenEstimator{}.EstimatePerMessage(ctx, messages)
		},
		total: func(ctx context.Context, messages []contexty.Message) (int, error) {
			return contexty.CharTokenEstimator{}.Estimate(ctx, messages)
		},
	}
	reporter, err := contexty.NewEstimateReporter(counter, profile, fixtureExtensionEstimateCodec())
	require.NoError(t, err)
	request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(20),
		Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}}
	// Act.
	report, err := reporter.Report(context.Background(), request)
	// Assert: no pinned type policy means unknown, regardless of generic capability.
	require.NoError(t, err)
	require.Equal(t, 14, report.Total)
	require.Equal(t, contexty.EstimateUnknown, report.Quality)
	require.NoError(t, report.Validate())
}
