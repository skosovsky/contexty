package contexty_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResource_RecordRoundTrip(t *testing.T) {
	for _, labeled := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "labels"}[labeled], func(t *testing.T) {
			// Arrange: runtime reader/projection already ran; codec has no runtime ports.
			ctx := context.Background()
			resource, codec := fixtureResolvedResource(t, labeled)
			require.NoError(t, resource.Validate(ctx, codec))
			// Act: host explicitly stores local evidence, including original body.
			wire, err := contexty.EncodeResolvedResource(ctx, resource, codec)
			require.NoError(t, err)
			restored, err := contexty.DecodeResolvedResource(ctx, wire, codec)
			// Assert: no inferred execution; exact data/identities/cost/lineage retained.
			require.NoError(t, err)
			require.Equal(t, resource, restored)
			require.Contains(t, string(wire), "private body")
			require.NotContains(t, string(wire), "fresh-read")
			cloned, err := restored.Clone()
			require.NoError(t, err)
			cloned.Source.Artifact.SourceRefs[0].ID = "changed"
			cloned.Projected.SourceRefs[0].ID = "changed"
			cloned.Artifact.SourceRefs[0].ID = "changed"
			cloned.Message.SourceRefs[0].ID = "changed"
			cloned.Configuration.Estimate.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
			cloned.Estimate.Profile.Capabilities[contexty.EstimateText] = contexty.EstimateUnknown
			cloned.Lineage.Records[0].Inputs[0].ID = "changed"
			require.NoError(t, restored.Validate(ctx, codec))
			require.Equal(t, resource, restored)
		})
	}
}

func TestResource_RecordBindings(t *testing.T) {
	for _, scenario := range []string{"id", "source-revision", "source-content", "projection-content", "artifact-content",
		"message-role", "estimate", "profile", "reader", "label-policy", "lineage-input", "lineage-extra", "intrinsic-decision"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: independent valid evidence, then one inconsistent component.
			resource, codec := fixtureResolvedResource(t, false)
			switch scenario {
			case "id":
				resource.ID = "changed"
			case "source-revision":
				resource.Source.Reference.Revision = "changed"
			case "source-content":
				resource.Source.Artifact.Payload.Text = "changed"
			case "projection-content":
				resource.Projected.Payload.Text = "changed"
			case "artifact-content":
				resource.Artifact.Payload.Text = "changed"
			case "message-role":
				resource.Message.Role = contexty.RoleUser
			case "estimate":
				resource.Estimate.Total++
			case "profile":
				resource.Configuration.Estimate.Encoding.Revision = "changed"
			case "reader":
				resource.Configuration.Reader.Revision = "changed"
			case "label-policy":
				resource.Configuration.Labels.Revision = "changed"
			case "lineage-input":
				resource.Lineage.Records[0].Inputs[0].ID = "changed"
			case "lineage-extra":
				resource.Lineage.Unresolved = []contexty.ContentRef{fixtureRef(t, "extra", "unresolved")}
			case "intrinsic-decision":
				resource.Lineage.Records[2].DecisionRef = "forged-upgrade"
			}
			// Act / Assert: inconsistent evidence cannot be published as a saved record.
			err := resource.Validate(context.Background(), codec)
			require.Error(t, err)
			wire, encodeErr := contexty.EncodeResolvedResource(context.Background(), resource, codec)
			require.Error(t, encodeErr)
			require.Empty(t, wire)
		})
	}
}

func TestResource_RecordMissingContent(t *testing.T) {
	// Arrange: even a self-consistent envelope hash cannot replace required bytes.
	resource, codec := fixtureResolvedResource(t, false)
	wire, err := contexty.EncodeResolvedResource(context.Background(), resource, codec)
	require.NoError(t, err)
	for _, field := range []string{"source", "projected", "artifact", "message"} {
		t.Run(field, func(t *testing.T) {
			missing := fixtureMutateResourceWire(t, wire, field, json.RawMessage("null"))
			// Act / Assert: no refetch or partial restored value.
			result, decodeErr := contexty.DecodeResolvedResource(context.Background(), missing, codec)
			require.ErrorIs(t, decodeErr, contexty.ErrMissingReplayDependency)
			require.Zero(t, result)
		})
	}
	changed := fixtureMutateResourceWire(t, wire, "id", json.RawMessage(`"changed"`))
	result, err := contexty.DecodeResolvedResource(context.Background(), changed, codec)
	require.ErrorIs(t, err, contexty.ErrInvalidLineage)
	require.Zero(t, result)
}

func TestResource_RecordCodecFailures(t *testing.T) {
	// Arrange: labels require their precise registered decoders.
	resource, codec := fixtureResolvedResource(t, true)
	wire, err := contexty.EncodeResolvedResource(context.Background(), resource, codec)
	require.NoError(t, err)
	missing := contexty.ResourceCodec{Messages: contexty.DefaultJSONSerializer(), Labels: nil}
	// Act / Assert: missing codec is not an untyped map fallback.
	result, err := contexty.DecodeResolvedResource(context.Background(), wire, missing)
	require.ErrorIs(t, err, contexty.ErrReplayCodec)
	require.Zero(t, result)
	codec.Labels = contexty.NewExtensionRegistry()
	codec.Labels.Register("fixture-label", func([]byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: `{"lossy":true}`}, nil
	})
	result, err = contexty.DecodeResolvedResource(context.Background(), wire, codec)
	require.Error(t, err)
	require.Zero(t, result)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	encoded, err := contexty.EncodeResolvedResource(ctx, resource, codec)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, encoded)
	result, err = contexty.DecodeResolvedResource(ctx, wire, codec)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
}

func TestResource_RecordStrictEnvelope(t *testing.T) {
	// Arrange: valid record, then unknown fields/trailing JSON or unsigned edits.
	resource, codec := fixtureResolvedResource(t, false)
	wire, err := contexty.EncodeResolvedResource(context.Background(), resource, codec)
	require.NoError(t, err)
	unknown := append([]byte(`{"unknown":true,`), wire[1:]...)
	trailing := append(bytes.Clone(wire), []byte(` {}`)...)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &envelope))
	envelope["id"] = json.RawMessage(`"unsigned-change"`)
	unsigned, err := json.Marshal(envelope)
	require.NoError(t, err)
	for _, malformed := range [][]byte{unknown, trailing, unsigned} {
		// Act / Assert: no successful decode of unverified state.
		result, decodeErr := contexty.DecodeResolvedResource(context.Background(), malformed, codec)
		require.Error(t, decodeErr)
		require.Zero(t, result)
	}
}

func TestResource_RecordDecoderCancellation(t *testing.T) {
	for _, stage := range []string{"artifact", "message"} {
		for _, fail := range []bool{false, true} {
			t.Run(stage+map[bool]string{false: "/success", true: "/error"}[fail], func(t *testing.T) {
				// Arrange: valid saved bytes; a host decoder cancels during restoration.
				resource, codec := fixtureResolvedResource(t, true)
				wire, err := contexty.EncodeResolvedResource(context.Background(), resource, codec)
				require.NoError(t, err)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				registry := contexty.NewExtensionRegistry()
				registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
					calls++
					cancel()
					if fail {
						return nil, errors.New("decoder failed after cancellation")
					}
					return fixtureWireExtension{wire: string(data)}, nil
				})
				if stage == "artifact" {
					codec.Labels = registry
				} else {
					codec.Messages.Extensions = registry
				}
				// Act: cancellation must dominate even the callback's own error.
				result, decodeErr := contexty.DecodeResolvedResource(ctx, wire, codec)
				// Assert: later callbacks stop and no partially restored evidence escapes.
				require.ErrorIs(t, decodeErr, context.Canceled)
				require.Zero(t, result)
				require.Equal(t, 1, calls)
			})
		}
	}
}
