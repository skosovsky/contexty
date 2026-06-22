package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResource_ConfigurationIdentity(t *testing.T) {
	// Arrange: label/encoding intent is separate from the text projection.
	resolver, request, _ := fixtureLabeledResourceFixture(t)
	baseline, err := resolver.Resolve(context.Background(), request)
	require.NoError(t, err)
	baselineRef, err := baseline.Configuration.Ref()
	require.NoError(t, err)
	for _, field := range []string{"reader", "projection", "labels", "codec", "encoding"} {
		t.Run(field, func(t *testing.T) {
			changed, changedRequest, _ := fixtureLabeledResourceFixture(t)
			switch field {
			case "reader":
				changed.ReaderIdentity.Revision = "changed"
			case "projection":
				changed.ProjectionIdentity.Revision = "changed"
			case "labels":
				changed.LabelPolicyIdentity.Revision = "changed"
			case "codec":
				changed.Codecs[0].Descriptor.Revision = "changed"
			case "encoding":
				profile := fixtureExtensionProfile("metadata")
				profile.Encoding.Revision = "changed"
				changed.Reporter, err = contexty.NewEstimateReporter(
					contexty.CharTokenEstimator{},
					profile,
					fixtureExtensionEstimateCodec(),
				)
				require.NoError(t, err)
			}
			// Act: execute the same text transform under changed host intent.
			result, resolveErr := changed.Resolve(context.Background(), changedRequest)
			require.NoError(t, resolveErr)
			ref, refErr := result.Configuration.Ref()
			// Assert: identity changes even when projection bytes remain equal.
			require.NoError(t, refErr)
			require.Equal(t, baseline.Message.TextContent(), result.Message.TextContent())
			require.NotEqual(t, baselineRef, ref)
			require.Equal(t, changed.LabelPolicyIdentity, result.Lineage.Records[2].Transform)
		})
	}
}

func TestResource_ConfigurationPreflight(t *testing.T) {
	for _, scenario := range []string{"missing-label-id", "surplus-label-id", "reserved-label-id", "typed-nil-label-policy",
		"missing-codec", "duplicate-codec", "surplus-codec"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: incomplete bindings fail before host I/O.
			resolver, request, _ := fixtureLabeledResourceFixture(t)
			resolver.Reader = fixtureResourceReader(
				func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
					t.Fatal("invalid configuration must not read a resource")
					return contexty.ResourceBody{}, nil
				},
			)
			switch scenario {
			case "missing-label-id":
				resolver.LabelPolicyIdentity = contexty.Descriptor{}
			case "surplus-label-id":
				resolver.Labels.Policy = nil
			case "reserved-label-id":
				resolver.LabelPolicyIdentity = contexty.Descriptor{
					ID:       "contexty/resource-source-metadata",
					Revision: "intrinsic",
				}
			case "typed-nil-label-policy":
				var policy fixtureLabelPolicy
				resolver.Labels.Policy = policy
			case "missing-codec":
				resolver.Codecs = nil
			case "duplicate-codec":
				resolver.Codecs = append(resolver.Codecs, resolver.Codecs[0])
			case "surplus-codec":
				resolver.Codecs = append(resolver.Codecs, fixtureCodecBinding(contexty.CodecLabel, "unused"))
			}
			// Act / Assert: there is no aggregate descriptor fallback.
			result, err := resolver.Resolve(context.Background(), request)
			require.Error(t, err)
			require.Zero(t, result)
		})
	}
}

func TestResource_UnusedCodecBinding(t *testing.T) {
	// Arrange: a configured but unused decoder still changes configuration intent.
	resolver, request, _ := fixtureLabeledResourceFixture(t)
	baseline, err := resolver.Configuration()
	require.NoError(t, err)
	baselineRef, err := baseline.Ref()
	require.NoError(t, err)
	resolver.Labels.Registry.Register("unused", func(data []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(data)}, nil
	})
	// Act / Assert: missing binding cannot infer identity from a function/type name.
	_, err = resolver.Configuration()
	require.Error(t, err)
	resolver.Codecs = append(resolver.Codecs, fixtureCodecBinding(contexty.CodecLabel, "unused"))
	result, err := resolver.Resolve(context.Background(), request)
	require.NoError(t, err)
	ref, err := result.Configuration.Ref()
	require.NoError(t, err)
	require.NotEqual(t, baselineRef, ref)
	require.Equal(t, "safe", result.Message.TextContent())
	// Assert: returned configuration owns its maps, slices and nested identities.
	copyConfiguration := result.Configuration.Clone()
	copyConfiguration.Estimate.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
	copyConfiguration.Trace.Codecs[0].Descriptor.Revision = "caller-mutated"
	copyConfiguration.Trace.RequiredLabelTypes[0] = "caller-mutated"
	again, err := result.Configuration.Ref()
	require.NoError(t, err)
	require.Equal(t, ref, again)
}

func TestResource_ConfigurationSnapshot(t *testing.T) {
	// Arrange: reader mutates caller-owned bindings/required types mid-resolution.
	resolver, request, body := fixtureLabeledResourceFixture(t)
	baseline, err := resolver.Configuration()
	require.NoError(t, err)
	baselineRef, err := baseline.Ref()
	require.NoError(t, err)
	bindings := resolver.Codecs
	required := resolver.Labels.RequiredTypes
	resolver.Reader = fixtureResourceReader(
		func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
			bindings[0].Descriptor.Revision = "reader-mutated"
			required[0] = "reader-mutated"
			return body, nil
		},
	)
	// Act: current operation uses its pre-I/O frozen configuration.
	result, err := resolver.Resolve(context.Background(), request)
	// Assert: mutation neither changes the recorded identity nor relaxes codecs.
	require.NoError(t, err)
	ref, err := result.Configuration.Ref()
	require.NoError(t, err)
	require.Equal(t, baselineRef, ref)
}

func TestResource_ConfigurationBindingOrder(t *testing.T) {
	// Arrange: binding declarations and repeated required types have no ordering semantics.
	resolver, _, _ := fixtureLabeledResourceFixture(t)
	before, err := resolver.Configuration()
	require.NoError(t, err)
	beforeRef, err := before.Ref()
	require.NoError(t, err)
	resolver.Codecs[0], resolver.Codecs[1] = resolver.Codecs[1], resolver.Codecs[0]
	resolver.Labels.RequiredTypes = []string{"fixture-label", "fixture-label"}
	// Act.
	after, err := resolver.Configuration()
	require.NoError(t, err)
	afterRef, err := after.Ref()
	// Assert: canonical metadata does not depend on caller declaration order.
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, beforeRef, afterRef)
}
