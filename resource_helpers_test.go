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

func fixtureAdapterResourceBlock(
	t *testing.T,
	resolver contexty.ResourceResolver,
	request contexty.ResourceResolveRequest,
) contexty.DeferredBlock {
	t.Helper()
	configuration, err := resolver.Configuration()
	require.NoError(t, err)
	return contexty.DeferredBlock{
		Name:          "selected",
		ResourceCodec: contexty.ResourceCodec{Messages: contexty.DefaultJSONSerializer()},
		Resources: []contexty.ResourceSelection{
			{ID: request.ID, Resource: request.Read.Resource, Configuration: configuration,
				Budget: request.Budget, MaxBytes: request.Read.MaxBytes},
		},
		Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
			resource, resolveErr := resolver.Resolve(ctx, request)
			return contexty.DeferredResult{Resources: []contexty.ResolvedResource{resource}}, resolveErr
		},
	}
}

func fixtureDedupRecordingEngine(block contexty.DeferredBlock) *contexty.Engine {
	return contexty.NewEngine(
		contexty.WithDeferredBlocks(block),
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile("memory"), fixtureBinding(contexty.RecordingResolver, "", "", 0)),
		),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
}

func fixtureResourceBlock(t *testing.T) (contexty.DeferredBlock, *int) {
	t.Helper()
	return fixtureResourceBlockWithProjection(t, nil)
}

func fixtureResourceBlockWithProjection(
	t *testing.T,
	change func(*contexty.ContextArtifact),
) (contexty.DeferredBlock, *int) {
	t.Helper()
	resolver, request, body := fixtureResourceFixture(t)
	project := resolver.Projection
	resolver.Projection = fixtureResourcePolicy(
		func(ctx context.Context, body contexty.ResourceBody) (contexty.ContextArtifact, error) {
			artifact, err := project.ProjectResource(ctx, body)
			artifact.Lifecycle = contexty.ArtifactLifecyclePersistent
			if change != nil {
				change(&artifact)
			}
			return artifact, err
		},
	)
	calls := new(int)
	resolver.Reader = fixtureResourceReader(
		func(_ context.Context, received contexty.ResourceReadRequest) (contexty.ResourceBody, error) {
			*calls++
			require.Equal(t, request.Read, received)
			return body, nil
		},
	)
	configuration, err := resolver.Configuration()
	require.NoError(t, err)
	selection := contexty.ResourceSelection{ID: request.ID, Resource: request.Read.Resource,
		Configuration: configuration, Budget: request.Budget, MaxBytes: request.Read.MaxBytes}
	return contexty.DeferredBlock{Name: "selected", Resources: []contexty.ResourceSelection{selection},
		ResourceCodec: contexty.ResourceCodec{Messages: contexty.DefaultJSONSerializer()},
		Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
			resource, resolveErr := resolver.Resolve(ctx, request)
			return contexty.DeferredResult{Resources: []contexty.ResolvedResource{resource}}, resolveErr
		}}, calls
}

func fixtureNamedReplacementBlock(t *testing.T, id string, artifactID ...string) contexty.DeferredBlock {
	t.Helper()
	resolver, request, _ := fixtureResourceFixture(t)
	request.ID = id
	project := resolver.Projection
	resolver.Projection = fixtureResourcePolicy(
		func(ctx context.Context, body contexty.ResourceBody) (contexty.ContextArtifact, error) {
			artifact, err := project.ProjectResource(ctx, body)
			artifact.ID = id
			if len(artifactID) != 0 {
				artifact.ID = artifactID[0]
			}
			artifact.Lifecycle = contexty.ArtifactLifecyclePersistent
			artifact.MergePolicy = contexty.PolicyReplaceByOrigin
			return artifact, err
		},
	)
	return fixtureAdapterResourceBlock(t, resolver, request)
}

func fixtureResolvedResource(t *testing.T, labeled bool) (contexty.ResolvedResource, contexty.ResourceCodec) {
	t.Helper()
	resolver, request, _ := fixtureResourceFixture(t)
	codec := contexty.ResourceCodec{Messages: contexty.DefaultJSONSerializer(), Labels: nil}
	if labeled {
		resolver, request, _ = fixtureLabeledResourceFixture(t)
		codec.Messages = fixtureExtensionEstimateCodec()
		codec.Labels = resolver.Labels.Registry
	}
	result, err := resolver.Resolve(context.Background(), request)
	require.NoError(t, err)
	return result, codec
}

func fixtureMutateResourceWire(t *testing.T, wire []byte, field string, value json.RawMessage) []byte {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &envelope))
	envelope[field] = value
	envelope["digest"] = json.RawMessage(`""`)
	unsigned, err := json.Marshal(envelope)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(unsigned))
	decoder.UseNumber()
	var canonicalValue any
	require.NoError(t, decoder.Decode(&canonicalValue))
	canonical, err := json.Marshal(canonicalValue)
	require.NoError(t, err)
	digest := sha256.Sum256(canonical)
	envelope["digest"], err = json.Marshal(hex.EncodeToString(digest[:]))
	require.NoError(t, err)
	encoded, err := json.Marshal(envelope)
	require.NoError(t, err)
	return encoded
}

func fixtureIndependentResourceRecord(
	t *testing.T,
	appendExisting ...bool,
) (contexty.SavedCompileRecord, contexty.JSONSerializer, contexty.ResourceCodec) {
	t.Helper()
	appendInput := len(appendExisting) != 0 && appendExisting[0]
	resolver, request, _ := fixtureLabeledResourceFixture(t)
	messages := fixtureExtensionEstimateCodec()
	messages.Extensions.Register("unused-message", func([]byte) (contexty.Extension, error) {
		t.Fatal("unused message decoder must not execute")
		return nil, contexty.ErrReplayCodec
	})
	resolver.Reporter = nil
	var err error
	resolver.Reporter, err = contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureExtensionProfile("metadata"),
		messages,
	)
	require.NoError(t, err)
	resolver.Labels.Registry.Register("unused-label", func([]byte) (contexty.Extension, error) {
		t.Fatal("unused label decoder must not execute")
		return nil, contexty.ErrReplayCodec
	})
	resolver.Codecs = append(resolver.Codecs, fixtureCodecBinding(contexty.CodecExtension, "unused-message"),
		fixtureCodecBinding(contexty.CodecLabel, "unused-label"))
	project := resolver.Projection
	resolver.Projection = fixtureResourcePolicy(
		func(ctx context.Context, body contexty.ResourceBody) (contexty.ContextArtifact, error) {
			artifact, projectErr := project.ProjectResource(ctx, body)
			artifact.Lifecycle = contexty.ArtifactLifecyclePersistent
			if appendInput {
				artifact.MergePolicy = contexty.PolicyAppend
			}
			return artifact, projectErr
		},
	)
	configuration, err := resolver.Configuration()
	require.NoError(t, err)
	resourceCodec := contexty.ResourceCodec{Messages: messages, Labels: resolver.Labels.Registry}
	block := contexty.DeferredBlock{
		Name:          "selected",
		ResourceCodec: resourceCodec,
		Resources: []contexty.ResourceSelection{
			{ID: request.ID, Resource: request.Read.Resource, Configuration: configuration,
				Budget: request.Budget, MaxBytes: request.Read.MaxBytes},
		},
		Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
			resource, resolveErr := resolver.Resolve(ctx, request)
			return contexty.DeferredResult{Resources: []contexty.ResolvedResource{resource}}, resolveErr
		},
	}
	trace := fixtureTraceProfile()
	trace.RequireOrigins = appendInput
	trace.Codec = fixtureExtensionEstimateCodec()
	trace.Codecs = []contexty.CodecBinding{
		fixtureCodecBinding(contexty.CodecExtension, "fixture-label"),
		fixtureCodecBinding(contexty.CodecLabel, "fixture-label"),
	}
	trace.Labels = contexty.LabelProjection{
		Registry: trace.Codec.Extensions,
		Policy: fixtureLabelPolicy(
			func(_ context.Context, _ []contexty.Message, output contexty.Message, _ contexty.Descriptor) (contexty.LabelDecision, error) {
				return contexty.LabelDecision{Extensions: output.Extensions}, nil
			},
		),
	}
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(trace),
		contexty.WithDeferredBlocks(block),
		contexty.WithCompileRecording(fixtureBindings(
			fixtureRecordProfile("first", "second"),
			fixtureBinding(
				contexty.RecordingResolver,
				"",
				"",
				0,
			),
			fixtureBinding(contexty.RecordingLabelPolicy, "", "", 0),
		)),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		fixtureIndependentResourceRequest(t, appendInput),
	)
	require.NoError(t, err)
	accepted, err := compiled.Record.Accept("host-accept")
	require.NoError(t, err)
	return accepted, trace.Codec, resourceCodec
}

func fixtureIndependentResourceRequest(t *testing.T, appendInput bool) contexty.CompileRequest {
	t.Helper()
	request := contexty.CompileRequest{CompilationID: "independent-resource", Targets: []contexty.CompileTarget{
		{
			Name:             "first",
			Segments:         []contexty.SegmentName{contexty.SegmentMemory},
			IncludeArtifacts: true,
		},
		{Name: "second", Segments: []contexty.SegmentName{contexty.SegmentMemory}, IncludeArtifacts: true},
	}}
	if !appendInput {
		return request
	}
	old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
	old.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["existing"]`}}
	old.SourceRefs = []contexty.SourceRef{{ID: "old-source"}}
	ref, err := contexty.ArtifactContentRef(old)
	require.NoError(t, err)
	request.Artifacts = []contexty.ContextArtifact{old}
	request.Origins = []contexty.ContentRef{ref}
	return request
}

func fixtureResourceRecordingEngine(t *testing.T, policy contexty.RecordContentPolicy) (*contexty.Engine, *int) {
	t.Helper()
	block, calls := fixtureResourceBlock(t)
	return contexty.NewEngine(
		contexty.WithDeferredBlocks(block),
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingResolver, "", "", 0)),
		),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"}, policy),
	), calls
}

type fixtureResourceReader func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error)

func (f fixtureResourceReader) ReadResource(
	ctx context.Context,
	request contexty.ResourceReadRequest,
) (contexty.ResourceBody, error) {
	return f(ctx, request)
}

type fixtureResourcePolicy func(context.Context, contexty.ResourceBody) (contexty.ContextArtifact, error)

func (f fixtureResourcePolicy) ProjectResource(
	ctx context.Context,
	body contexty.ResourceBody,
) (contexty.ContextArtifact, error) {
	return f(ctx, body)
}

func fixtureResourceFixture(
	t *testing.T,
) (contexty.ResourceResolver, contexty.ResourceResolveRequest, contexty.ResourceBody) {
	t.Helper()
	body := contexty.ResourceBody{Reference: contexty.Descriptor{ID: "opaque-source", Revision: "pinned"},
		Artifact: contexty.NewRetrievalDocument("body", contexty.TextPayload("private body")).ContextArtifact}
	body.Artifact.SourceRefs = []contexty.SourceRef{{ID: "opaque-source"}}
	descriptor, err := contexty.DescribeResource(body.Reference, "same display name", body.Artifact)
	require.NoError(t, err)
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	resolver := contexty.ResourceResolver{
		ReaderIdentity:     contexty.Descriptor{ID: "host-reader", Revision: "pinned"},
		ProjectionIdentity: contexty.Descriptor{ID: "host-preview", Revision: "pinned"},
		Reader: fixtureResourceReader(
			func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) { return body, nil },
		),
		Projection: fixtureResourcePolicy(
			func(_ context.Context, received contexty.ResourceBody) (contexty.ContextArtifact, error) {
				received.Artifact.ID = "projected"
				received.Artifact.Payload = contexty.TextPayload("safe")
				return received.Artifact, nil
			},
		),
		Labels:   contexty.LabelProjection{},
		Reporter: reporter,
	}
	request := contexty.ResourceResolveRequest{ID: "resolve", Read: contexty.ResourceReadRequest{
		ScopeRef: "fresh-read",
		Resource: descriptor,
		MaxBytes: descriptor.Length,
	}, Budget: contexty.WindowInputBudget(7, 2, 1)}
	return resolver, request, body
}

func fixtureLabeledResourceFixture(
	t *testing.T,
) (contexty.ResourceResolver, contexty.ResourceResolveRequest, contexty.ResourceBody) {
	t.Helper()
	resolver, request, body := fixtureResourceFixture(t)
	body.Artifact.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"host":"external"}`}}
	descriptor, err := contexty.DescribeResource(body.Reference, request.Read.Resource.Name, body.Artifact)
	require.NoError(t, err)
	request.Read.Resource, request.Read.MaxBytes = descriptor, descriptor.Length
	resolver.Reader = fixtureResourceReader(
		func(context.Context, contexty.ResourceReadRequest) (contexty.ResourceBody, error) { return body, nil },
	)
	codec := fixtureExtensionEstimateCodec()
	resolver.Reporter, err = contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureExtensionProfile("metadata"),
		codec,
	)
	require.NoError(t, err)
	resolver.Labels = contexty.LabelProjection{Registry: codec.Extensions, RequiredTypes: []string{"fixture-label"},
		Policy: fixtureLabelPolicy(func(_ context.Context, inputs []contexty.Message, _ contexty.Message,
			_ contexty.Descriptor) (contexty.LabelDecision, error) {
			return contexty.LabelDecision{Extensions: inputs[0].Extensions}, nil
		})}
	resolver.LabelPolicyIdentity = contexty.Descriptor{ID: "resource-label-policy", Revision: "pinned"}
	resolver.Codecs = []contexty.CodecBinding{fixtureCodecBinding(contexty.CodecExtension, "fixture-label"),
		fixtureCodecBinding(contexty.CodecLabel, "fixture-label")}
	return resolver, request, body
}
