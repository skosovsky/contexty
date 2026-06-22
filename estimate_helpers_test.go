package contexty_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureExtensionPolicy(metadata bool) contexty.EstimateExtensionPolicy {
	return contexty.EstimateExtensionPolicy{Codec: contexty.Descriptor{ID: "host-codec", Revision: "pinned"},
		Policy: contexty.Descriptor{ID: "wire-classification", Revision: "pinned"}, MetadataOnly: metadata}
}

func fixtureExtensionEstimateCodec() contexty.JSONSerializer {
	codec := contexty.DefaultJSONSerializer()
	codec.Extensions.Register("fixture-label", func(body []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(body)}, nil
	})
	return codec
}

func fixtureExtensionProfile(mode string) contexty.EstimateProfile {
	profile := fixtureEstimateProfile()
	if mode == "metadata" || mode == "content" {
		profile.Extensions = map[string]contexty.EstimateExtensionPolicy{
			"fixture-label": fixtureExtensionPolicy(mode == "metadata"),
		}
	}
	if mode == "content" {
		profile.Capabilities[contexty.EstimateExtension] = contexty.EstimateEstimated
	}
	if mode == "fallback" {
		profile.Fallback = &contexty.EstimateFallback{
			Policy: contexty.Descriptor{ID: "fallback", Revision: "pinned"}, Tokens: 11}
	}
	return profile
}

func fixtureCheckExtensionProfileCopy(
	t *testing.T,
	reporter *contexty.EstimateReporter,
	counter contexty.TokenEstimator,
	profile contexty.EstimateProfile,
	request contexty.EstimateRequest,
	report contexty.EstimateReport,
) {
	t.Helper()
	profile.Extensions["fixture-label"] = fixtureExtensionPolicy(false)
	again, err := reporter.Report(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, report.ProfileDigest, again.ProfileDigest)
	replacement, err := contexty.NewEstimateReporter(counter, profile, fixtureExtensionEstimateCodec())
	require.NoError(t, err)
	_, err = replacement.Report(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrUnknownEstimateCost)
}

func fixtureResignEstimate(t *testing.T, report *contexty.EstimateReport) {
	t.Helper()
	report.Digest = ""
	wire, err := json.Marshal(report)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.UseNumber()
	var value any
	require.NoError(t, decoder.Decode(&value))
	canonical, err := json.Marshal(value)
	require.NoError(t, err)
	digest := sha256.Sum256(canonical)
	report.Digest = hex.EncodeToString(digest[:])
}

func fixtureReportWithWire(t *testing.T) contexty.EstimateReport {
	t.Helper()
	message := contexty.TextMessage(contexty.RoleUser, "secret")
	message.ID = "m"
	wire := fixtureRefForMessage(t, message)
	wire.ID = "exact-wire"
	reporter, err := contexty.NewEstimateReporter(contexty.CharTokenEstimator{}, fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	report, err := reporter.Report(context.Background(), contexty.EstimateRequest{
		Budget: contexty.WindowInputBudget(20, 5, 3), WireRef: &wire,
		Segments: []contexty.EstimateSegment{{Name: "history", Messages: []contexty.Message{message}}}})
	require.NoError(t, err)
	return report
}

func fixtureEstimateProfile() contexty.EstimateProfile {
	return contexty.EstimateProfile{Model: contexty.Descriptor{ID: "model", Revision: "pinned"},
		Estimator: contexty.Descriptor{ID: "estimator", Revision: "pinned"},
		Method:    contexty.Descriptor{ID: "semantic", Revision: "pinned"},
		Encoding:  contexty.Descriptor{ID: "json", Revision: "pinned"},
		Capabilities: map[contexty.EstimateKind]contexty.EstimateQuality{
			contexty.EstimateText: contexty.EstimateEstimated, contexty.EstimateToolResult: contexty.EstimateEstimated,
			contexty.EstimateImage: contexty.EstimateUnknown, contexty.EstimateToolCall: contexty.EstimateUnknown,
			contexty.EstimateMedia: contexty.EstimateUnknown, contexty.EstimateExtension: contexty.EstimateUnknown,
		}}
}

type fixtureEvidenceEstimator struct {
	per   func(context.Context, []contexty.Message) ([]int, error)
	total func(context.Context, []contexty.Message) (int, error)
}

func (e fixtureEvidenceEstimator) Estimate(ctx context.Context, messages []contexty.Message) (int, error) {
	return e.total(ctx, messages)
}

func (e fixtureEvidenceEstimator) EstimatePerMessage(ctx context.Context, messages []contexty.Message) ([]int, error) {
	return e.per(ctx, messages)
}
