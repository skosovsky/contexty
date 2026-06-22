package contexty_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestBlob_ArtifactCompileAndResume(t *testing.T) {
	// Arrange: the complete typed payload is much larger than prompt budget.
	request := fixtureBlobArtifactRequest()
	original, err := contexty.ArtifactContentRef(request.Artifact)
	require.NoError(t, err)
	var stored contexty.BlobPutRequest
	policy, err := contexty.NewBlobThresholdPolicy(contexty.BlobThresholdLimits{MaxInlineBytes: 3, MaxBlobBytes: 20000},
		fixtureBlobPreviewer(func(context.Context, contexty.BlobContent) (contexty.BlobContent, error) {
			return contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}, nil
		}))
	require.NoError(t, err)
	offloader := contexty.BlobOffloader{
		Policy:         policy,
		PolicyIdentity: contexty.Descriptor{ID: "threshold", Revision: "pinned"},
		Storage: fixtureBlobStore{
			put: func(_ context.Context, put contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
				stored = put
				return fixtureBlobReceipt(put), nil
			},
			get: nil,
		},
	}
	profile := fixtureTraceProfile()
	profile.RequireOrigins = true
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(profile),
		contexty.WithCompileRecording(fixtureRecordProfile()),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, contexty.NewBudgetPipeline(
			contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(2)}, contexty.CharTokenEstimator{})),
	)
	// Act: explicit host preparation precedes both artifact and final admission.
	prepared, err := offloader.ProjectArtifact(context.Background(), request)
	require.NoError(t, err)
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "blob-artifact", Artifacts: []contexty.ContextArtifact{*prepared.Artifact},
		Lineage: prepared.Lineage, Origins: []contexty.ContentRef{original}})
	// Assert: budget counts preview, while lineage/storage preserve original.
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 1)
	require.Len(t, result.ArtifactEstimates, 1)
	require.Equal(t, 2, result.ArtifactEstimates[0].Tokens)
	require.Equal(t, []contexty.ContentPart{contexty.TextPart{Text: "ok"}}, result.Payload.Memory[0].Parts)
	require.Equal(t, request.Artifact.SourceRefs, result.Payload.Memory[0].SourceRefs)
	require.Equal(t, original, result.Artifacts[0].Blob.Original)
	require.NoError(t, result.Lineage.Validate())
	require.Equal(t, "artifact-offload", result.Lineage.Records[0].Stage)
	var restored contexty.ToolPayload
	require.NoError(t, json.Unmarshal(stored.Content.Bytes, &restored))
	require.Equal(t, request.Artifact.Payload, restored)
	require.Equal(t, []contexty.ContentRef{original}, stored.Sources)
	codec := contexty.ConversationCodec{}
	wire, err := codec.Encode(contexty.EmptySnapshot().WithArtifacts(result.Artifacts))
	require.NoError(t, err)
	require.NotContains(t, string(wire), "private ")
	resumed, err := codec.Decode(wire)
	require.NoError(t, err)
	require.Equal(t, result.Artifacts, resumed.Artifacts())
	resumeResult, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "blob-resume", Artifacts: resumed.Artifacts(), Lineage: result.Lineage,
		Origins: []contexty.ContentRef{original}})
	require.NoError(t, err)
	require.Equal(t, result.Payload.Memory, resumeResult.Payload.Memory)
	accepted, err := result.Record.Accept("host-accept")
	require.NoError(t, err)
	recordWire, err := contexty.EncodeSavedRecord(accepted)
	require.NoError(t, err)
	require.NotContains(t, string(recordWire), "private ")
	record, err := contexty.DecodeSavedRecord(recordWire)
	require.NoError(t, err)
	expected, err := contexty.ReplayExpectationFor(*result.Manifest)
	require.NoError(t, err)
	replayed, err := contexty.Replay(context.Background(), record, expected, contexty.DefaultJSONSerializer(),
		fixtureBlobReplayOption(t, prepared.Artifact.Blob.Object))
	require.NoError(t, err)
	require.Equal(t, result.Artifacts, replayed.Artifacts)
	projection := contexty.CompileProjection{Messages: result.Payload.Memory, Lineage: result.Lineage}
	exported, err := contexty.ExportProjection(projection, result.Artifacts,
		contexty.ExportSelection{ArtifactIDs: []string{"large"}}, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	exportWire, err := json.Marshal(exported)
	require.NoError(t, err)
	for _, forbidden := range []string{"private ", "write-scope", "retention", "opaque-object"} {
		require.NotContains(t, string(exportWire), forbidden)
	}
	prepared.Selection.Threshold.MaxBlobBytes = 1
	require.EqualValues(t, 20000, prepared.Artifact.Blob.Threshold.MaxBlobBytes)
}

func TestBlob_ArtifactTampering(t *testing.T) {
	// Arrange: a valid stored artifact binds exactly one preview and source.
	request := fixtureBlobArtifactRequest()
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
				return fixtureBlobReceipt(put), nil
			},
			get: nil,
		},
	}
	prepared, err := offloader.ProjectArtifact(context.Background(), request)
	require.NoError(t, err)
	for _, scenario := range []string{"preview", "source", "media", "policy", "threshold", "merged-preview"} {
		t.Run(scenario, func(t *testing.T) {
			artifact := prepared.Artifact.Clone()
			want := contexty.ErrInvalidBlob
			switch scenario {
			case "preview":
				artifact.Payload.Text = "altered"
				want = contexty.ErrBlobDigestMismatch
			case "source":
				artifact.Blob.Original.ID = "different-artifact"
			case "media":
				artifact.Blob.Object.MIMEType = fixtureBlobMIME
			case "policy":
				artifact.Blob.Policy = contexty.Descriptor{}
			case "threshold":
				artifact.Blob.Threshold = &contexty.BlobThresholdLimits{MaxInlineBytes: 10, MaxBlobBytes: 1}
				want = contexty.ErrInvalidBlobPolicy
			case "merged-preview":
				artifact.MergePolicy = contexty.PolicyAppend
				want = contexty.ErrBlobDigestMismatch
			}
			artifacts := []contexty.ContextArtifact{artifact}
			if scenario == "merged-preview" {
				artifacts = append(artifacts, artifact.Clone())
			}
			// Act.
			result, compileErr := contexty.NewEngine().
				CompileSnapshot(context.Background(), contexty.CompileRequest{Artifacts: artifacts})
			// Assert: corruption never reaches a prompt or silently drops metadata.
			require.ErrorIs(t, compileErr, want)
			require.Zero(t, result)
			codec := contexty.ConversationCodec{}
			snapshot := contexty.EmptySnapshot()
			for _, item := range artifacts {
				snapshot = snapshot.WithArtifact(item)
			}
			encoded, encodeErr := codec.Encode(snapshot)
			require.ErrorIs(t, encodeErr, want)
			require.Nil(t, encoded)
			// Bypass the public validating encoder to inject corrupt storage.
			type rawArtifact contexty.ContextArtifact
			rawArtifacts := make([]rawArtifact, len(artifacts))
			for i, item := range artifacts {
				rawArtifacts[i] = rawArtifact(item)
			}
			wire, wireErr := json.Marshal(map[string]any{"artifacts": rawArtifacts})
			require.NoError(t, wireErr)
			decoded, decodeErr := codec.Decode(wire)
			require.ErrorIs(t, decodeErr, want)
			require.Zero(t, decoded)
		})
	}
	// Arrange/Act/Assert: reference preparation cannot recursively offload a preview.
	request.Artifact = *prepared.Artifact
	failed, err := offloader.ProjectArtifact(context.Background(), request)
	require.ErrorIs(t, err, contexty.ErrInvalidBlob)
	require.Zero(t, failed)
}

func TestBlob_ArtifactSelection(t *testing.T) {
	for _, disposition := range []contexty.BlobDisposition{contexty.BlobInline, contexty.BlobReject, contexty.BlobOffload} {
		t.Run(string(disposition), func(t *testing.T) {
			// Arrange: failed Put cannot return a prepared artifact.
			request := fixtureBlobArtifactRequest()
			failure := errors.New("partial Put")
			puts := 0
			offloader := contexty.BlobOffloader{
				Policy: fixtureBlobPolicy(
					func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
						return contexty.BlobOffloadDecision{Disposition: disposition,
							Preview: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}}, nil
					},
				),
				PolicyIdentity: contexty.Descriptor{ID: "policy", Revision: "pinned"},
				Storage: fixtureBlobStore{
					put: func(_ context.Context, put contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
						puts++
						return fixtureBlobReceipt(put), failure
					},
					get: nil,
				},
			}
			// Act.
			prepared, err := offloader.ProjectArtifact(context.Background(), request)
			// Assert.
			switch disposition {
			case contexty.BlobInline:
				require.NoError(t, err)
				require.Equal(t, request.Artifact, *prepared.Artifact)
				require.Zero(t, puts)
			case contexty.BlobReject:
				require.ErrorIs(t, err, contexty.ErrBlobRejected)
				require.Nil(t, prepared.Artifact)
				require.Zero(t, puts)
			case contexty.BlobOffload:
				require.ErrorIs(t, err, failure)
				require.Nil(t, prepared.Artifact)
				require.Nil(t, prepared.Selection.Stored)
				require.NotNil(t, prepared.Selection.Cleanup)
				require.Equal(t, 1, puts)
			}
		})
	}
}
