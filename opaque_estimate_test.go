package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureOpaqueEstimateCodec() contexty.JSONSerializer {
	codec := contexty.DefaultJSONSerializer()
	codec.Extensions.RegisterOpaquePayload("fixture-label", contexty.Descriptor{ID: "host-codec", Revision: "pinned"},
		func(body []byte) (contexty.Extension, error) { return fixtureWireExtension{wire: string(body)}, nil })
	return codec
}

func fixtureOpaqueEstimateMessage() contexty.Message {
	message := contexty.TextMessage(contexty.RoleUser, "abc")
	message.ID = "opaque-carrier"
	message.Extensions = []contexty.Extension{
		contexty.OpaqueState{
			ID:        "state",
			Payload:   fixtureWireExtension{wire: `{"body":"opaque-bytes"}`},
			Codec:     contexty.Descriptor{ID: "host-codec", Revision: "pinned"},
			Placement: contexty.OpaquePlacement{AfterPart: 0},
			Binding: contexty.OpaqueBinding{
				Profile:  contexty.Descriptor{ID: "host-profile", Revision: "pinned"},
				Required: nil,
				Prefix:   nil,
				Boundary: "",
			},
		},
	}
	return message
}

func TestOpaqueEstimate_StrictAndExplicitFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict", true: "fallback"}[fallback], func(t *testing.T) {
			// Arrange: opaque wire state has no implicit token weight.
			message := fixtureOpaqueEstimateMessage()
			profile := fixtureEstimateProfile()
			if fallback {
				profile.Fallback = &contexty.EstimateFallback{
					Policy: contexty.Descriptor{ID: "opaque-fallback", Revision: "1"},
					Tokens: 11,
				}
			}
			reporter, err := contexty.NewEstimateReporter(
				contexty.CharTokenEstimator{},
				profile,
				fixtureOpaqueEstimateCodec(),
			)
			require.NoError(t, err)
			// Act.
			report, err := reporter.Report(
				context.Background(),
				contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(100),
					Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}},
			)
			// Assert: fallback stays unknown and does not count bytes.
			if !fallback {
				require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
				require.Zero(t, report)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 14, report.Total)
			require.Equal(t, contexty.EstimateUnknown, report.Quality)
			wire, err := contexty.EncodeEstimateReport(report)
			require.NoError(t, err)
			restored, err := contexty.DecodeEstimateReport(wire)
			require.NoError(t, err)
			require.Equal(t, report, restored)
		})
	}
}

func TestOpaqueEstimate_ExplicitAdapterAndRequiredCodec(t *testing.T) {
	// Arrange: the adapter explicitly knows the wire cost of its state.
	message := fixtureOpaqueEstimateMessage()
	profile := fixtureEstimateProfile()
	profile.Capabilities[contexty.EstimateExtension] = contexty.EstimateEstimated
	profile.Extensions = map[string]contexty.EstimateExtensionPolicy{
		contexty.OpaqueStateExtensionType: fixtureExtensionPolicy(false),
	}
	calls := 0
	estimator := fixtureEvidenceEstimator{
		per: func(_ context.Context, messages []contexty.Message) ([]int, error) {
			calls++
			require.Len(t, messages[0].Extensions, 1)
			return []int{23}, nil
		},
		total: func(_ context.Context, _ []contexty.Message) (int, error) { calls++; return 23, nil },
	}
	reporter, err := contexty.NewEstimateReporter(estimator, profile, fixtureOpaqueEstimateCodec())
	require.NoError(t, err)
	request := contexty.EstimateRequest{Budget: contexty.EffectiveInputBudget(100),
		Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}}
	// Act.
	report, err := reporter.Report(context.Background(), request)
	// Assert: cost comes from the adapter, not the opaque bytes.
	require.NoError(t, err)
	require.Equal(t, 23, report.Total)
	require.Positive(t, calls)

	// Arrange: a builtin envelope codec cannot replace its mandatory nested codec.
	calls = 0
	missing, err := contexty.NewEstimateReporter(estimator, profile, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	// Act.
	_, err = missing.Report(context.Background(), request)
	// Assert: failure occurs before invoking the adapter.
	require.ErrorIs(t, err, contexty.ErrMissingOpaqueStateCodec)
	require.Zero(t, calls)

	// Arrange: state is always wire content, never metadata-only.
	profile.Extensions[contexty.OpaqueStateExtensionType] = fixtureExtensionPolicy(true)
	// Act.
	_, err = contexty.NewEstimateReporter(estimator, profile, fixtureOpaqueEstimateCodec())
	// Assert.
	require.ErrorIs(t, err, contexty.ErrInvalidEstimateReport)
}

func TestOpaqueEstimate_BuiltinsRejectState(t *testing.T) {
	for _, estimator := range []contexty.TokenEstimator{contexty.CharTokenEstimator{},
		&contexty.CharFallbackEstimator{CharsPerToken: 4, TokensPerNonTextPart: 0, EstimateTool: nil},
		&contexty.FixedEstimator{TokensPerMessage: 1, TokensPerContentPart: 1, TokensPerToolCall: 1}} {
		// Arrange.
		message := fixtureOpaqueEstimateMessage()
		// Act.
		_, err := estimator.EstimatePerMessage(context.Background(), []contexty.Message{message})
		// Assert: no builtin silently prices state as zero.
		require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
	}
}
