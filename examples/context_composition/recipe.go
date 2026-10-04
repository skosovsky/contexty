package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/blob/memory"
)

const (
	regionKey        = "region"
	noiseRepeats     = 100
	blobByteLimit    = 8192
	previewByteLimit = 64
	promptLimit      = 400
	restoreLimit     = 4096
)

type hostFact struct {
	Key     string               `json:"key"`
	Value   string               `json:"value"`
	Version int                  `json:"version"`
	Sources []contexty.SourceRef `json:"sources"`
}

type factDecision struct {
	Key             string              `json:"key"`
	PreviousVersion int                 `json:"previous_version"`
	AcceptedVersion int                 `json:"accepted_version"`
	Policy          contexty.Descriptor `json:"policy"`
}

func replaceFact(previous, next hostFact) (hostFact, factDecision, error) {
	// Host domain rule: an explicitly sourced higher revision supersedes the same
	// logical key. This is not a core truth extraction or reconciliation policy.
	if previous.Key != next.Key || next.Version <= previous.Version || len(next.Sources) == 0 {
		return hostFact{}, factDecision{}, errors.New("host fact revision conflict")
	}
	return next, factDecision{Key: next.Key, PreviousVersion: previous.Version, AcceptedVersion: next.Version,
		Policy: contexty.Descriptor{ID: "host-monotonic-fact", Revision: "v1"}}, nil
}

//nolint:funlen,gocognit // Sequential checkpoint and retention handoff keeps its ordering explicit.
func runComposition(
	ctx context.Context,
) (compositionReport, error) {
	var report compositionReport
	// The immutable completed result is held by the host journal. Never replace
	// an active tool call/result payload in Compile to reduce token cost.
	original := "access code: 4317; " + strings.Repeat("irrelevant tool rows; ", noiseRepeats)
	completed := contexty.Message{ID: "lookup-result", Role: contexty.RoleTool, Parts: []contexty.ContentPart{
		contexty.ToolResultPart{
			ToolCallID: "lookup-call",
			Name:       "lookup",
			Payload:    contexty.TextPayload(original),
			IsError:    false,
		},
	}, SourceRefs: []contexty.SourceRef{evidenceSource()}}
	toolResult, ok := completed.Parts[0].(contexty.ToolResultPart)
	if !ok {
		return report, errors.New("completed evidence is not a tool result")
	}
	evidence := contexty.NewRetrievalDocument(
		"lookup-evidence",
		contexty.TextPayload(toolResult.Payload.PlainText()),
	).ContextArtifact
	evidence.Lifecycle = contexty.ArtifactLifecyclePersistent
	evidence.SourceRefs = completed.SourceRefs
	store, err := memory.New(memory.Config{Namespace: "composition", MaxObjectBytes: blobByteLimit,
		Authorize: func(_ context.Context, access memory.Access) error {
			if access.ScopeRef != scope {
				return contexty.ErrBlobDenied
			}
			return nil
		}})
	if err != nil {
		return report, err
	}
	policy, err := contexty.NewBlobThresholdPolicy(
		contexty.BlobThresholdLimits{MaxInlineBytes: previewByteLimit, MaxBlobBytes: blobByteLimit},
		previewer{},
	)
	if err != nil {
		return report, err
	}
	offloader := contexty.BlobOffloader{
		Policy:         policy,
		PolicyIdentity: contexty.Descriptor{ID: "explicit-preview", Revision: "v1"},
		Storage:        store,
	}
	offloaded, err := offloader.ProjectArtifact(
		ctx,
		contexty.BlobArtifactRequest{
			ID:                       "offload-evidence",
			Artifact:                 evidence,
			ScopeRef:                 scope,
			RetentionRef:             "checkpoint-claim-v1",
			MaxPreviewBytes:          previewByteLimit,
			AllowedPreviewMediaTypes: []string{"text/plain"},
			Extensions:               nil,
		},
	)
	if err != nil {
		return report, err
	}
	if offloaded.Artifact == nil || offloaded.Selection.Stored == nil {
		return report, errors.New("fixture must offload")
	}
	report.OriginalBytes = len(original)
	report.Preview = offloaded.Artifact.Payload.PlainText()
	old := hostFact{
		Key:     regionKey,
		Value:   "east",
		Version: 1,
		Sources: []contexty.SourceRef{
			{Namespace: "host", Kind: "settings", ID: regionKey, CheckpointID: "region:v1", URI: ""},
		},
	}
	next := hostFact{
		Key:     regionKey,
		Value:   "west",
		Version: 2,
		Sources: []contexty.SourceRef{
			{Namespace: "host", Kind: "settings", ID: regionKey, CheckpointID: "region:v2", URI: ""},
		},
	}
	fact, decision, err := replaceFact(old, next)
	if err != nil {
		return report, err
	}
	report.FactReplacement = decision
	factPayload, err := contexty.StructuredPayload(fact)
	if err != nil {
		return report, err
	}
	memoryFact := contexty.NewMemoryBlock("region-fact", factPayload).ContextArtifact
	memoryFact.SourceRefs = fact.Sources
	summary := &fixtureSummary{calls: 0}
	pipeline := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{ //nolint:exhaustruct_v5 // Rolling summary uses no explicit truncation/retention config.
			Budget:     contexty.EffectiveInputBudget(promptLimit),
			Summarizer: summary,
		},
		contexty.CharTokenEstimator{},
		contexty.WithRollingSummary(
			contexty.RollingSummaryPolicy{
				Descriptor:     contexty.Descriptor{ID: "recent-two", Revision: "v1"},
				RecentMessages: 2,
			},
		),
	)
	history := []contexty.Message{
		text("constraint", "constraint: do not disclose credentials"),
		text("noise", strings.Repeat("old discussion; ", noiseRepeats)),
		text("recent-1", "Which region is current?"),
		text("recent-2", "Restore original access code."),
	}
	history[0].SourceRefs = []contexty.SourceRef{
		{Namespace: "host-journal", Kind: "constraint", ID: "constraint", CheckpointID: "constraint:v1", URI: ""},
	}
	engine := contexty.NewEngine(
		contexty.WithArtifactMaterialization(hostMaterialization()),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipeline),
	)
	compiled, err := engine.Compile(
		ctx,
		contexty.CompileRequest{ //nolint:exhaustruct_v5 // Direct fixture input, no store/deferred state.
			History:   history,
			Artifacts: []contexty.ContextArtifact{*offloaded.Artifact, memoryFact},
		},
	)
	if err != nil {
		return report, err
	}
	if summary.calls != 1 || len(compiled.Payload.History) != 3 {
		return report, errors.New("rolling fixture did not compact")
	}
	report.Summary = compiled.Payload.History[0].TextContent()
	for _, message := range compiled.Payload.History[1:] {
		report.RecentIDs = append(report.RecentIDs, message.ID)
	}
	// An ephemeral host checkpoint owns the preview reference. Serialize and save
	// before claim commit. A durable host must atomically persist bytes, claims and
	// authoritative checkpoint state in a durable adapter; this map cannot do so.
	checkpoint := contexty.Descriptor{ID: "working-checkpoint", Revision: "v1"}
	state, err := compiled.DerivePersistenceState(
		contexty.DefaultJSONSerializer(),
		contexty.Descriptor{ID: "", Revision: ""},
	)
	if err != nil {
		return report, err
	}
	state, err = contexty.ProjectCheckpoint(
		state,
		contexty.DefaultJSONSerializer(),
		contexty.Descriptor{ID: "", Revision: ""},
	)
	if err != nil {
		return report, err
	}
	codec := contexty.ConversationStateCodec{
		OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""},
		Provenance:    nil,
		Extensions:    nil,
	}
	checkpointWire, err := codec.EncodeState(state)
	if err != nil {
		return report, err
	}

	checkpoints := map[contexty.Descriptor][]byte{checkpoint: checkpointWire}
	if len(checkpoints[checkpoint]) == 0 {
		return report, errors.New("checkpoint not saved")
	}
	if err = store.CommitCheckpoint(
		ctx,
		scope,
		checkpoint,
		[]string{offloaded.Selection.Stored.RetentionRef},
	); err != nil {
		return report, err
	}
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		estimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	if err != nil {
		return report, err
	}
	resolver := contexty.BlobResolver{
		Storage:         store,
		Decoder:         payloadDecoder{},
		DecoderIdentity: contexty.Descriptor{ID: "host-tool-payload", Revision: "v1"},
		Reporter:        reporter,
	}
	request := contexty.BlobResolveRequest{
		ID:       "restore-evidence",
		ScopeRef: scope,
		Blob:     *offloaded.Selection.Stored,
		Limits: contexty.BlobLimits{
			MaxBytes:          blobByteLimit,
			AllowedMediaTypes: []string{"application/json"},
		},
		Budget: contexty.EffectiveInputBudget(restoreLimit),
	}
	bounded := request
	bounded.Limits.MaxBytes = 1
	_, err = resolver.Resolve(ctx, bounded)
	report.BoundedRead = errors.Is(err, contexty.ErrBlobSizeLimit)
	if !report.BoundedRead {
		return report, fmt.Errorf("bounded read: %w", err)
	}
	denied := request
	denied.ScopeRef = "revoked"
	_, err = resolver.Resolve(ctx, denied)
	report.DeniedRead = errors.Is(err, contexty.ErrBlobDenied)
	if !report.DeniedRead {
		return report, fmt.Errorf("denied read: %w", err)
	}
	stale := request
	stale.Blob = stale.Blob.Clone()
	stale.Blob.Object.Revision = "stale"
	_, err = resolver.Resolve(ctx, stale)
	report.StaleRead = errors.Is(err, contexty.ErrBlobMissing)
	if !report.StaleRead {
		return report, fmt.Errorf("stale read: %w", err)
	}
	restored, err := resolver.Resolve(ctx, request)
	if err != nil {
		return report, err
	}
	report.RestoredOriginal = restored.Messages[0].TextContent() == original &&
		toolResult.Payload.PlainText() == original
	if !report.RestoredOriginal {
		return report, errors.New("original mutated")
	}
	// Source retirement does not silently release checkpoint claims.
	if _, err = store.RetireSource(ctx, scope, offloaded.Artifact.Blob.Original); err != nil {
		return report, err
	}
	_, err = store.Collect(ctx, scope, offloaded.Selection.Stored.Object)
	report.ClaimProtected = errors.Is(err, memory.ErrActiveClaim)
	if !report.ClaimProtected {
		return report, fmt.Errorf("claim protection: %w", err)
	}
	delete(checkpoints, checkpoint) // Authoritative host retirement precedes release.
	if err = store.ReleaseCheckpoint(ctx, scope, checkpoint); err != nil {
		return report, err
	}
	report.Collected, err = store.Collect(ctx, scope, offloaded.Selection.Stored.Object)
	if err != nil {
		return report, err
	}
	report.ProviderUsage = "not measured"
	report.Durability = "ephemeral reference backend; durable host adapter required"
	return report, nil
}

func text(id, value string) contexty.Message {
	message := contexty.TextMessage(contexty.RoleUser, value)
	message.ID = id
	return message
}

func estimateProfile() contexty.EstimateProfile {
	return contexty.EstimateProfile{
		Model:        contexty.Descriptor{ID: "offline-fixture", Revision: "v1"},
		Estimator:    contexty.Descriptor{ID: "rune-count", Revision: "v1"},
		Method:       contexty.Descriptor{ID: "semantic", Revision: "v1"},
		Encoding:     contexty.Descriptor{ID: "unicode", Revision: "v1"},
		Capabilities: (contexty.CharTokenEstimator{}).EstimateCapabilities(),
		Fallback:     nil,
		Extensions:   nil,
	}
}
