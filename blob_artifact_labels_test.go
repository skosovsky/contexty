package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestBlob_ArtifactLabels(t *testing.T) {
	// Arrange: labels belong to the host, not a core trust classification.
	registry := fixtureArtifactLabelRegistry()
	request := fixtureBlobArtifactRequest()
	request.Extensions = registry
	request.Artifact.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["external"]`}}
	original, err := contexty.ArtifactContentRef(request.Artifact)
	require.NoError(t, err)
	offloader := contexty.BlobOffloader{
		Policy: fixtureBlobPolicy(
			func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
				return contexty.BlobOffloadDecision{Disposition: contexty.BlobOffload,
					Preview: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}}, nil
			},
		),
		PolicyIdentity: contexty.Descriptor{ID: "policy", Revision: "pinned"},
		Storage: fixtureBlobStore{
			put: func(_ context.Context, put contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
				require.Equal(t, []contexty.ContentRef{original}, put.Sources)
				return fixtureBlobReceipt(put), nil
			},
			get: nil,
		},
	}
	trace := fixtureTraceProfile()
	trace.RequireOrigins = true
	trace.Codec.Extensions = registry
	trace.Codecs = []contexty.CodecBinding{fixtureCodecBinding(contexty.CodecLabel, "fixture-label"),
		fixtureCodecBinding(contexty.CodecExtension, "fixture-label")}
	trace.Labels = contexty.LabelProjection{Registry: registry, RequiredTypes: []string{"fixture-label"},
		Policy: fixtureLabelPolicy(func(_ context.Context, inputs []contexty.Message, output contexty.Message,
			_ contexty.Descriptor) (contexty.LabelDecision, error) {
			labels := output.Extensions
			if len(inputs) > 0 {
				labels = inputs[0].Extensions
			}
			return contexty.LabelDecision{Extensions: labels}, nil
		})}
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(trace),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingLabelPolicy, "", "", 0)),
		),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	// Act: offload, compile, checkpoint and replay without any Get port.
	prepared, err := offloader.ProjectArtifact(context.Background(), request)
	require.NoError(t, err)
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "blob-labels", Artifacts: []contexty.ContextArtifact{*prepared.Artifact},
		Lineage: prepared.Lineage, Origins: []contexty.ContentRef{original}})
	require.NoError(t, err)
	stateCodec := contexty.ConversationStateCodec{Extensions: registry}
	delta := contexty.ConversationDelta{Operation: contexty.DeltaUpsertArtifact, Artifact: prepared.Artifact}
	deltaWire, err := stateCodec.EncodeDelta(delta)
	require.NoError(t, err)
	restoredDelta, err := stateCodec.DecodeDelta(deltaWire)
	require.NoError(t, err)
	state, err := contexty.ApplyDelta(contexty.EmptyState(), restoredDelta)
	require.NoError(t, err)
	stateWire, err := stateCodec.EncodeState(state)
	require.NoError(t, err)
	resumed, err := stateCodec.DecodeState(stateWire)
	require.NoError(t, err)
	resumeResult, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "blob-labels-resume", Artifacts: resumed.Artifacts(),
		Lineage: result.Lineage, Origins: []contexty.ContentRef{original}})
	require.NoError(t, err)
	accepted, err := result.Record.Accept("host-accept")
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*result.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), accepted, expected, trace.Codec,
		fixtureBlobReplayOption(t, prepared.Artifact.Blob.Object))
	// Assert: system role/preview never silently clear or upgrade host labels.
	require.NoError(t, err)
	require.Equal(t, request.Artifact.Extensions, result.Payload.Memory[0].Extensions)
	require.Equal(t, result.Payload.Memory, resumeResult.Payload.Memory)
	require.Equal(t, request.Artifact.Extensions, result.Artifacts[0].Extensions)
	require.Equal(t, result.Artifacts, resumed.Artifacts())
	require.Equal(t, result.Artifacts, replayed.Artifacts)
	require.Equal(t, original, replayed.Artifacts[0].Blob.Original)
	require.Contains(t, string(deltaWire), `"type_id":"fixture-label"`)
	projection := contexty.CompileProjection{
		Messages:    result.Payload.Memory,
		Lineage:     result.Lineage,
		Artifacts:   result.Artifacts,
		ArtifactIDs: []string{"large"},
	}
	for _, disclose := range []bool{false, true} {
		selection := contexty.ExportSelection{ArtifactIDs: []string{"large"}}
		if disclose {
			selection.Metadata.ExtensionTypes = []string{"fixture-label"}
		}
		exported, exportErr := contexty.ExportProjection(projection, selection, trace.Codec)
		require.NoError(t, exportErr)
		exportWire, exportErr := json.Marshal(exported)
		require.NoError(t, exportErr)
		if disclose {
			require.Contains(t, string(exportWire), `"type_id":"fixture-label"`)
			require.Contains(t, string(exportWire), "external")
		} else {
			require.NotContains(t, string(exportWire), "external")
		}
		for _, forbidden := range []string{"write-scope", "retention", "opaque-object"} {
			require.NotContains(t, string(exportWire), forbidden)
		}
	}
	failedReplay, replayErr := contexty.Replay(
		context.Background(),
		accepted,
		expected,
		contexty.DefaultJSONSerializer(),
	)
	require.ErrorIs(t, replayErr, contexty.ErrReplayCodec)
	require.Zero(t, failedReplay)
}

func TestArtifact_LabelCodecFailures(t *testing.T) {
	// Arrange: wire explicitly carries the host label identity and original value.
	artifact := fixtureBlobArtifactRequest().Artifact
	artifact.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["external"]`}}
	wire, err := json.Marshal(artifact)
	require.NoError(t, err)
	for _, scenario := range []string{"missing", "lossy", "nil-label", "nil-decoder", "typed-nil-result"} {
		t.Run(scenario, func(t *testing.T) {
			registry := contexty.NewExtensionRegistry()
			if scenario == "lossy" {
				registry.Register("fixture-label", func([]byte) (contexty.Extension, error) {
					return fixtureWireExtension{wire: `[]`}, nil
				})
			}
			if scenario == "nil-decoder" {
				registry.Register("fixture-label", nil)
			}
			if scenario == "typed-nil-result" {
				registry.Register("fixture-label", func([]byte) (contexty.Extension, error) {
					return (*fixtureWireExtension)(nil), nil
				})
			}
			input := artifact.Clone()
			if scenario == "nil-label" {
				input.Extensions = []contexty.Extension{nil}
			}
			// Act/Assert: no plain-map fallback or partially decoded artifact.
			decoded, decodeErr := contexty.UnmarshalArtifactJSON(wire, registry)
			require.ErrorIs(t, decodeErr, contexty.ErrMissingLabelCodec)
			require.Zero(t, decoded)
			codec := contexty.ConversationCodec{Extensions: registry}
			encoded, encodeErr := codec.Encode(contexty.EmptySnapshot().WithArtifact(input))
			require.ErrorIs(t, encodeErr, contexty.ErrMissingLabelCodec)
			require.Nil(t, encoded)
			request := fixtureBlobArtifactRequest()
			request.Artifact, request.Extensions = input, registry
			prepared, prepareErr := (contexty.BlobOffloader{}).ProjectArtifact(context.Background(), request)
			require.ErrorIs(t, prepareErr, contexty.ErrMissingLabelCodec)
			require.Zero(t, prepared)
		})
	}
}

func TestArtifact_LabelCodecCancellation(t *testing.T) {
	// Arrange: a codec cancellation must stop preparation before policy/Put.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
		cancel()
		return fixtureWireExtension{wire: string(data)}, nil
	})
	request := fixtureBlobArtifactRequest()
	request.Artifact.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["external"]`}}
	request.Extensions = registry
	// Act: missing policy/storage would fail differently if work continued.
	prepared, err := (contexty.BlobOffloader{}).ProjectArtifact(ctx, request)
	// Assert.
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, prepared)
}

func TestArtifact_AppendPreservesLabels(t *testing.T) {
	// Arrange: append cannot silently discard labels or ancestry of its left side.
	left := contexty.NewMemoryBlock("merged", contexty.TextPayload("left")).ContextArtifact
	left.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["external"]`}}
	left.SourceRefs = []contexty.SourceRef{{ID: "left-source"}}
	right := contexty.NewMemoryBlock("merged", contexty.TextPayload("right")).ContextArtifact
	right.MergePolicy = contexty.PolicyAppend
	right.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["instruction"]`}}
	right.SourceRefs = []contexty.SourceRef{{ID: "right-source"}}
	// Act: state append preserves both opaque values, leaving reconciliation to host.
	state := contexty.EmptyState().WithArtifact(left).WithArtifact(right)
	codec := contexty.ConversationCodec{Extensions: fixtureArtifactLabelRegistry()}
	wire, err := codec.Encode(state)
	require.NoError(t, err)
	restored, err := codec.Decode(wire)
	// Assert.
	require.NoError(t, err)
	merged := restored.Artifacts()[0]
	require.Equal(t, "left\nright", merged.Payload.Text)
	require.Equal(t, append(left.Extensions, right.Extensions...), merged.Extensions)
	require.Equal(t, []contexty.SourceRef{{ID: "left-source"}, {ID: "right-source"}}, merged.SourceRefs)
	require.Equal(t, `["external"]`, left.Extensions[0].(fixtureWireExtension).wire)
}
