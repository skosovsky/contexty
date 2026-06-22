package contexty_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestResourceAppend_MediaRejected(t *testing.T) {
	// Arrange: media cannot be flattened to text by append.
	block, _ := fixtureResourceBlockWithProjection(
		t,
		func(artifact *contexty.ContextArtifact) { artifact.MergePolicy = contexty.PolicyAppend },
	)
	old := contexty.NewMemoryBlock(
		"projected",
		contexty.ToolPayload{MIMEType: "image/png", Binary: []byte{1}},
	).ContextArtifact
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(block))
	// Act.
	compiled, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{old}},
	)
	// Assert: unsupported media returns no partial prompt/artifact state.
	require.ErrorIs(t, err, contexty.ErrResourceUnsupported)
	require.Zero(t, compiled)
	require.Equal(t, []byte{1}, old.Payload.Binary)
}

func TestResourceAppend_BlobPreviewRejected(t *testing.T) {
	// Arrange: old preview has a valid immutable-object binding which append cannot reuse.
	request := fixtureBlobArtifactRequest()
	request.Artifact.ID = "projected"
	offloader := contexty.BlobOffloader{
		PolicyIdentity: contexty.Descriptor{ID: "offload", Revision: "pinned"},
		Policy: fixtureBlobPolicy(
			func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error) {
				return contexty.BlobOffloadDecision{Disposition: contexty.BlobOffload,
					Preview: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("ok")}}, nil
			},
		),
		Storage: fixtureBlobStore{
			put: func(_ context.Context, put contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
				return fixtureBlobReceipt(put), nil
			},
			get: nil,
		},
	}
	prepared, err := offloader.ProjectArtifact(context.Background(), request)
	require.NoError(t, err)
	block, _ := fixtureResourceBlockWithProjection(
		t,
		func(artifact *contexty.ContextArtifact) { artifact.MergePolicy = contexty.PolicyAppend },
	)
	engine := contexty.NewEngine(contexty.WithDeferredBlocks(block))
	// Act.
	compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		Artifacts: []contexty.ContextArtifact{*prepared.Artifact},
	})
	// Assert: old descriptor remains valid; no text merge bypasses its preview digest binding.
	require.ErrorIs(t, err, contexty.ErrResourceUnsupported)
	require.Zero(t, compiled)
	_, err = contexty.ArtifactContentRef(*prepared.Artifact)
	require.NoError(t, err)
}

func TestResourceAppend_EstimatorCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "cancel"}[canceled], func(t *testing.T) {
			// Arrange: only the merged eight-character admission invokes the failing counter path.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			block, _ := fixtureResourceBlockWithProjection(t, func(artifact *contexty.ContextArtifact) {
				artifact.MergePolicy = contexty.PolicyAppend
				artifact.Budget = &contexty.ArtifactBudgetPolicy{TokenLimit: 8}
			})
			failures := 0
			counter := fixtureEvidenceEstimator{
				total: func(ctx context.Context, messages []contexty.Message) (int, error) {
					if len(messages) == 1 && messages[0].TextContent() == "old\nsafe" {
						failures++
						if canceled {
							cancel()
						}
						return 0, contexty.ErrResourceDenied
					}
					return contexty.CharTokenEstimator{}.Estimate(ctx, messages)
				},
				per: contexty.CharTokenEstimator{}.EstimatePerMessage,
			}
			pipe := contexty.NewBudgetPipeline(
				contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(100)},
				counter,
			)
			engine := contexty.NewEngine(
				contexty.WithDeferredBlocks(block),
				contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
			)
			old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
			// Act / Assert: no count failure is silently changed into a successful zero-cost merge.
			compiled, err := engine.CompileSnapshot(
				ctx,
				contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{old}},
			)
			want := contexty.ErrTokenCountFailed
			if canceled {
				want = context.Canceled
			}
			require.ErrorIs(t, err, want)
			require.Zero(t, compiled)
			require.Equal(t, 1, failures)
			require.Equal(t, "old", old.Payload.Text)
		})
	}
}

func TestResourceAppend_DerivedPrivacy(t *testing.T) {
	// Arrange: host approves originals but denies the derived artifact bytes.
	block, _ := fixtureResourceBlockWithProjection(
		t,
		func(artifact *contexty.ContextArtifact) { artifact.MergePolicy = contexty.PolicyAppend },
	)
	engine := contexty.NewEngine(
		contexty.WithDeferredBlocks(block),
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(
			fixtureBindings(fixtureRecordProfile(), fixtureBinding(contexty.RecordingResolver, "", "", 0)),
		),
		contexty.WithCompileContentCapture(contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(func(_ context.Context, candidate contexty.CaptureCandidate) (bool, error) {
				return candidate.Stage != "resource-append" ||
					!strings.Contains(string(candidate.Content.Wire), `old\nsafe`), nil
			})),
	)
	old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
	// Act: prompt-safe compilation is allowed, but derived raw retention is not.
	compiled, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "append-private", Artifacts: []contexty.ContextArtifact{old},
	})
	// Assert: later output capture cannot override denial or replace content with a hash.
	require.NoError(t, err)
	require.Equal(t, "old\nsafe", compiled.Payload.Memory[0].TextContent())
	accepted, err := compiled.Record.Accept("not-enough-content")
	require.ErrorIs(t, err, contexty.ErrMissingReplayDependency)
	require.Zero(t, accepted)
}

func TestResourceAppend_LabelFailures(t *testing.T) {
	for _, scenario := range []string{"preserve", "missing-policy", "conflict", "upgrade", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: old labels belong to the host; resource resolution itself is unlabeled.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			block, _ := fixtureResourceBlockWithProjection(
				t,
				func(artifact *contexty.ContextArtifact) { artifact.MergePolicy = contexty.PolicyAppend },
			)
			profile := fixtureTraceProfile()
			profile.Codec.Extensions = fixtureArtifactLabelRegistry()
			profile.Labels.Registry = profile.Codec.Extensions
			if scenario != "missing-policy" {
				profile.Labels.Policy = fixtureAppendLabelPolicy(scenario, cancel)
			}
			old := contexty.NewMemoryBlock("projected", contexty.TextPayload("old")).ContextArtifact
			old.Extensions = []contexty.Extension{fixtureWireExtension{wire: `["host-owned"]`}}
			engine := contexty.NewEngine(contexty.WithDeferredBlocks(block), contexty.WithTraceProfile(profile))
			// Act.
			compiled, err := engine.CompileSnapshot(
				ctx,
				contexty.CompileRequest{CompilationID: "append-labels", Artifacts: []contexty.ContextArtifact{old}},
			)
			// Assert: conflicts/upgrades/cancellation never publish a partial projection.
			if scenario == "preserve" {
				require.NoError(t, err)
				require.Equal(t, old.Extensions, compiled.Artifacts[0].Extensions)
				require.Equal(t, old.Extensions, compiled.Payload.Memory[0].Extensions)
				return
			}
			require.ErrorIs(t, err, map[string]error{"missing-policy": contexty.ErrMissingLabelPolicy,
				"conflict": contexty.ErrLabelConflict, "upgrade": contexty.ErrInvalidTrustUpgrade, "cancel": context.Canceled}[scenario])
			require.Zero(t, compiled)
		})
	}
}
