package contexty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResource_Projection(t *testing.T) {
	// Arrange: descriptor is metadata only; host explicitly selects and reads it.
	resolver, request, body := fixtureResourceFixture(t)
	reads := 0
	resolver.Reader = fixtureResourceReader(
		func(_ context.Context, received contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
			reads++
			require.Equal(t, request.Read, received)
			return body, nil
		},
	)
	// Act.
	result, err := resolver.Resolve(context.Background(), request)
	// Assert: original and projection remain distinct, with cost and full ancestry.
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	require.Equal(t, body, result.Source)
	require.Equal(t, "private body", result.Source.Artifact.Payload.Text)
	require.Equal(t, "safe", result.Artifact.Payload.Text)
	require.Equal(t, "safe", result.Message.TextContent())
	require.Equal(t, body.Artifact.SourceRefs, result.Message.SourceRefs)
	require.Equal(t, 4, result.Estimate.Total)
	require.Equal(t, 4, result.Estimate.EffectiveLimit)
	require.NoError(t, result.Lineage.Validate())
	require.Len(t, result.Lineage.Records, 4)
	root, err := contexty.ResourceDescriptorRef(request.Read.Resource)
	require.NoError(t, err)
	require.Equal(t, []contexty.ContentRef{root}, result.Lineage.Records[0].Inputs)
	require.Equal(t, resolver.ReaderIdentity, result.Lineage.Records[0].Transform)
	require.Equal(t, resolver.ProjectionIdentity, result.Lineage.Records[1].Transform)
	require.Equal(t, request.Read.Resource.Content.Digest, result.Lineage.Records[0].Outputs[0].Digest)
	require.Equal(t, result.Estimate.Segments[0].Messages[0].Digest, result.Lineage.Records[3].Outputs[0].Digest)
	result.Source.Artifact.SourceRefs[0].ID = "changed"
	result.Artifact.SourceRefs[0].ID = "changed"
	result.Message.SourceRefs[0].ID = "changed"
	require.Equal(t, "opaque-source", body.Artifact.SourceRefs[0].ID)
}

func TestResource_ChangedBody(t *testing.T) {
	for _, scenario := range []string{"revision", "content", "length", "oversize", "unsupported"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: a valid pinned descriptor, then a changed actual response.
			resolver, request, body := fixtureResourceFixture(t)
			want := contexty.ErrResourceMismatch
			switch scenario {
			case "revision":
				body.Reference.Revision = "changed"
			case "content":
				body.Artifact.Payload.Text = "altered body"
			case "length":
				request.Read.Resource.Length--
			case "oversize":
				body.Artifact.Payload.Text += "expanded"
				want = contexty.ErrResourceSizeLimit
			case "unsupported":
				body.Artifact.Kind = "host-private-kind"
				want = contexty.ErrResourceUnsupported
			}
			resolver.Reader = fixtureResourceReader(
				func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) { return body, nil },
			)
			projections := 0
			resolver.Projection = fixtureResourcePolicy(
				func(context.Context, contexty.ResourceBody) (contexty.ContextArtifact, error) {
					projections++
					return body.Artifact, nil
				},
			)
			// Act / Assert: failed pin/byte checks do not project or return any body.
			result, err := resolver.Resolve(context.Background(), request)
			require.ErrorIs(t, err, want)
			require.Zero(t, result)
			require.Zero(t, projections)
		})
	}
}

func TestResource_ReaderFailures(t *testing.T) {
	for _, failure := range []error{contexty.ErrResourceMissing, contexty.ErrResourceDenied, errors.New("host unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			// Arrange: reader fails even though descriptor claims a body exists.
			resolver, request, body := fixtureResourceFixture(t)
			resolver.Reader = fixtureResourceReader(
				func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
					return body, failure
				},
			)
			// Act / Assert: descriptor alone never supplies body or permission.
			result, err := resolver.Resolve(context.Background(), request)
			require.ErrorIs(t, err, failure)
			require.Zero(t, result)
		})
	}
	resolver, request, body := fixtureResourceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver.Reader = fixtureResourceReader(
		func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
			cancel()
			return body, nil
		},
	)
	resolver.Projection = fixtureResourcePolicy(
		func(context.Context, contexty.ResourceBody) (contexty.ContextArtifact, error) {
			t.Fatal("projection must not execute after canceled read")
			return body.Artifact, nil
		},
	)
	// Act / Assert: cancellation dominates successful read and stops later stages.
	result, err := resolver.Resolve(ctx, request)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
}

func TestResource_Preflight(t *testing.T) {
	for _, scenario := range []string{"id", "scope", "reference", "digest", "limit", "negative-limit", "budget", "reader", "projection", "reporter"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: invalid setup must not invoke any reader.
			resolver, request, _ := fixtureResourceFixture(t)
			resolver.Reader = fixtureResourceReader(
				func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
					t.Fatal("invalid request must not read")
					return contexty.ResourceBody{}, nil
				},
			)
			switch scenario {
			case "id":
				request.ID = ""
			case "scope":
				request.Read.ScopeRef = ""
			case "reference":
				request.Read.Resource.Reference.Revision = ""
			case "digest":
				request.Read.Resource.Content.Digest = "invalid"
			case "limit":
				request.Read.MaxBytes--
			case "negative-limit":
				request.Read.MaxBytes = -1
			case "budget":
				request.Budget = contexty.EffectiveInputBudget(-1)
			case "reader":
				resolver.Reader = nil
			case "projection":
				resolver.Projection = nil
			case "reporter":
				resolver.Reporter = nil
			}
			// Act / Assert.
			result, err := resolver.Resolve(context.Background(), request)
			require.Error(t, err)
			require.Zero(t, result)
		})
	}
}

func TestResource_ProjectionFailures(t *testing.T) {
	// Arrange: projection is valid but exceeds explicitly bounded token capacity.
	resolver, request, _ := fixtureResourceFixture(t)
	request.Budget = contexty.EffectiveInputBudget(3)
	// Act / Assert: no trimming or fallback disguises overflow.
	result, err := resolver.Resolve(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrBudgetExceeded)
	require.Zero(t, result)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver.Projection = fixtureResourcePolicy(
		func(_ context.Context, body contexty.ResourceBody) (contexty.ContextArtifact, error) {
			cancel()
			return body.Artifact, nil
		},
	)
	result, err = resolver.Resolve(ctx, request)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
}

func TestResource_LabelsAndIsolation(t *testing.T) {
	// Arrange: projection tries to drop labels/ancestry and mutate its input copy.
	resolver, request, body := fixtureLabeledResourceFixture(t)
	resolver.Projection = fixtureResourcePolicy(
		func(_ context.Context, received contexty.ResourceBody) (contexty.ContextArtifact, error) {
			received.Artifact.SourceRefs[0].ID = "projector-mutated"
			artifact := contexty.NewMemoryBlock("projected", contexty.TextPayload("safe")).ContextArtifact
			return artifact, nil
		},
	)
	// Act.
	result, err := resolver.Resolve(context.Background(), request)
	// Assert: policy preserves opaque host labels, unions source ancestry and freezes raw body.
	require.NoError(t, err)
	require.Equal(t, body, result.Source)
	require.Equal(t, body.Artifact.Extensions, result.Artifact.Extensions)
	require.Equal(t, body.Artifact.Extensions, result.Message.Extensions)
	require.Equal(t, body.Artifact.SourceRefs, result.Artifact.SourceRefs)
	require.Equal(t, contexty.ArtifactLifecyclePersistent, result.Artifact.Lifecycle)
	require.JSONEq(t, `{"host":"external"}`, result.Artifact.Extensions[0].(fixtureWireExtension).wire)
}

func TestResource_LabelFailures(t *testing.T) {
	for _, scenario := range []string{"codec", "policy", "upgrade", "conflict"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: strict label transport must not silently strip metadata.
			resolver, request, _ := fixtureLabeledResourceFixture(t)
			want := contexty.ErrMissingLabelCodec
			switch scenario {
			case "codec":
				resolver.Labels.Registry = nil
				resolver.Codecs = []contexty.CodecBinding{fixtureCodecBinding(contexty.CodecExtension, "fixture-label")}
			case "policy":
				resolver.Labels.Policy = nil
				resolver.LabelPolicyIdentity = contexty.Descriptor{}
				want = contexty.ErrMissingLabelPolicy
			case "upgrade":
				resolver.Labels.Policy = fixtureLabelPolicy(
					func(context.Context, []contexty.Message, contexty.Message, contexty.Descriptor) (contexty.LabelDecision, error) {
						return contexty.LabelDecision{Upgrade: true}, nil
					},
				)
				want = contexty.ErrInvalidTrustUpgrade
			case "conflict":
				resolver.Labels.Policy = fixtureLabelPolicy(
					func(context.Context, []contexty.Message, contexty.Message, contexty.Descriptor) (contexty.LabelDecision, error) {
						return contexty.LabelDecision{}, contexty.ErrLabelConflict
					},
				)
				want = contexty.ErrLabelConflict
			}
			// Act / Assert: labels cannot become permissions or disappear on success.
			result, err := resolver.Resolve(context.Background(), request)
			require.ErrorIs(t, err, want)
			require.Zero(t, result)
		})
	}
}

func TestResource_SameNames(t *testing.T) {
	// Arrange: same display name and body ID, but separate opaque host sources.
	resolver, firstRequest, firstBody := fixtureResourceFixture(t)
	secondBody := firstBody.Clone()
	secondBody.Reference.ID = "other-source"
	secondBody.Artifact.SourceRefs[0].ID = "other-source"
	secondDescriptor, err := contexty.DescribeResource(
		secondBody.Reference,
		firstRequest.Read.Resource.Name,
		secondBody.Artifact,
	)
	require.NoError(t, err)
	secondRequest := firstRequest
	secondRequest.ID = "second-resolution"
	secondRequest.Read.Resource, secondRequest.Read.MaxBytes = secondDescriptor, secondDescriptor.Length
	reads := make([]contexty.Descriptor, 0, 2)
	resolver.Reader = fixtureResourceReader(
		func(_ context.Context, read contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
			reads = append(reads, read.Resource.Reference)
			if read.Resource.Reference == firstBody.Reference {
				return firstBody, nil
			}
			return secondBody, nil
		},
	)
	resolver.Projection = fixtureResourcePolicy(
		func(_ context.Context, body contexty.ResourceBody) (contexty.ContextArtifact, error) {
			body.Artifact.ID = body.Reference.ID + "/projection"
			body.Artifact.Payload = contexty.TextPayload("safe")
			return body.Artifact, nil
		},
	)
	// Act: only the explicit selected ref is read each time.
	first, err := resolver.Resolve(context.Background(), firstRequest)
	require.NoError(t, err)
	second, err := resolver.Resolve(context.Background(), secondRequest)
	// Assert: names never become keys or grants; lineage/content remain distinct.
	require.NoError(t, err)
	require.Equal(t, []contexty.Descriptor{firstBody.Reference, secondBody.Reference}, reads)
	require.NotEqual(t, first.Artifact.ID, second.Artifact.ID)
	require.NotEqual(t, first.Lineage.Records[0].Inputs, second.Lineage.Records[0].Inputs)
	require.NotEqual(t, first.Artifact.SourceRefs, second.Artifact.SourceRefs)
}

func TestResourceArtifact_Contracts(t *testing.T) {
	for _, scenario := range []string{"lifecycle", "persistence", "budget", "binary"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: descriptor creation must reject unsupported typed content.
			_, _, body := fixtureResourceFixture(t)
			want := contexty.ErrInvalidResource
			switch scenario {
			case "lifecycle":
				body.Artifact.Lifecycle = "unknown"
			case "persistence":
				body.Artifact.Persistence = "unknown"
			case "budget":
				body.Artifact.Budget = &contexty.ArtifactBudgetPolicy{TokenLimit: -1}
			case "binary":
				body.Artifact.Payload = contexty.ToolPayload{Binary: []byte("opaque")}
				want = contexty.ErrResourceUnsupported
			}
			// Act / Assert: invalid bodies cannot get a usable descriptor.
			descriptor, err := contexty.DescribeResource(body.Reference, "display", body.Artifact)
			require.ErrorIs(t, err, want)
			require.Zero(t, descriptor)
		})
	}
}

func TestResource_UpgradeDecision(t *testing.T) {
	// Arrange: only the host policy may upgrade opaque trust metadata.
	resolver, request, _ := fixtureLabeledResourceFixture(t)
	resolver.Labels.Policy = fixtureLabelPolicy(func(context.Context, []contexty.Message, contexty.Message,
		contexty.Descriptor) (contexty.LabelDecision, error) {
		return contexty.LabelDecision{
			Extensions:  []contexty.Extension{fixtureWireExtension{wire: `{"host":"approved"}`}},
			Upgrade:     true,
			DecisionRef: "host-reviewed-decision",
		}, nil
	})
	// Act.
	result, err := resolver.Resolve(context.Background(), request)
	// Assert: recorded host decision accompanies the actual projected labels.
	require.NoError(t, err)
	require.JSONEq(t, `{"host":"approved"}`, result.Artifact.Extensions[0].(fixtureWireExtension).wire)
	require.JSONEq(t, `{"host":"external"}`, result.Source.Artifact.Extensions[0].(fixtureWireExtension).wire)
	require.Equal(t, "host-reviewed-decision", result.Lineage.Records[2].DecisionRef)
}
