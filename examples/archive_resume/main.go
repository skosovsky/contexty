// Archive/resume keeps original events outside the bounded working checkpoint.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/skosovsky/contexty"
)

const (
	archiveRevision = "fixture-1"
	archiveBudget   = 180
)

type journal struct {
	directory string
	reads     int
}

type scenarioReport struct {
	Compactions    int                 `json:"compactions"`
	Reads          int                 `json:"archive_body_reads"`
	OriginalAbsent bool                `json:"original_absent_from_checkpoint"`
	Restored       string              `json:"restored_original"`
	Reference      contexty.ContentRef `json:"restored_content"`
}

// This host fixture writes each original before context processing. It is not a
// production journal: no fsync, transaction, concurrent writer or recovery policy.
//
//nolint:musttag // ResourceBody is a library host port; this fixture uses local JSON files only.
func (j *journal) save(message contexty.Message) (contexty.SourceRef, error) {
	if !safeID(message.ID) {
		return contexty.SourceRef{}, contexty.ErrInvalidResource
	}
	ref := contexty.SourceRef{
		Namespace:    "host-journal",
		Kind:         "episode",
		CheckpointID: "", URI: "",
		ID: message.ID,
	}
	artifact := contexty.NewRetrievalDocument(message.ID, contexty.TextPayload(message.TextContent())).ContextArtifact
	artifact.SourceRefs = []contexty.SourceRef{ref}
	wire, err := json.Marshal(
		contexty.ResourceBody{
			Reference: contexty.Descriptor{ID: message.ID, Revision: archiveRevision},
			Artifact:  artifact,
		},
	)
	if err != nil {
		return contexty.SourceRef{}, err
	}
	if err = os.WriteFile(filepath.Join(j.directory, message.ID+".json"), wire, 0o600); err != nil {
		return contexty.SourceRef{}, err
	}
	descriptor, err := contexty.DescribeResource(
		contexty.Descriptor{ID: message.ID, Revision: archiveRevision},
		"archived episode",
		artifact,
	)
	if err != nil {
		return contexty.SourceRef{}, err
	}
	metadata, err := json.Marshal(descriptor)
	if err != nil {
		return contexty.SourceRef{}, err
	}
	if err = os.WriteFile(filepath.Join(j.directory, message.ID+".descriptor.json"), metadata, 0o600); err != nil {
		return contexty.SourceRef{}, err
	}
	original, err := contexty.DefaultJSONSerializer().Marshal(message)
	if err != nil {
		return contexty.SourceRef{}, err
	}
	return ref, os.WriteFile(filepath.Join(j.directory, message.ID+".event.json"), original, 0o600)
}

func (j *journal) selectEpisode(ref contexty.SourceRef) (contexty.ResourceDescriptor, error) {
	if ref.Namespace != "host-journal" || ref.Kind != "episode" || !safeID(ref.ID) {
		return contexty.ResourceDescriptor{}, contexty.ErrResourceMissing
	}
	wire, err := os.ReadFile(filepath.Join(j.directory, ref.ID+".descriptor.json"))
	if err != nil {
		return contexty.ResourceDescriptor{}, err
	}
	var descriptor contexty.ResourceDescriptor
	if err = json.Unmarshal(wire, &descriptor); err != nil {
		return contexty.ResourceDescriptor{}, err
	}
	return descriptor, descriptor.Validate()
}

func safeID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".." && !strings.ContainsAny(id, `/\\`)
}

//nolint:musttag // Decode the local fixture envelope, not a public wire contract.
func (j *journal) load(id string) (contexty.ResourceBody, error) {
	wire, err := os.ReadFile(filepath.Join(j.directory, id+".json"))
	if err != nil {
		return contexty.ResourceBody{}, err
	}
	var body contexty.ResourceBody
	if err = json.Unmarshal(wire, &body); err != nil {
		return contexty.ResourceBody{}, err
	}
	return body, nil
}

// ReadResource checks authorization and bounds before body I/O. The resolver
// subsequently checks the complete typed content digest and pinned revision.
//
//nolint:musttag // Decode the local fixture envelope, not a public wire contract.
func (j *journal) ReadResource(
	ctx context.Context,
	request contexty.ResourceReadRequest,
) (contexty.ResourceBody, error) {
	if err := ctx.Err(); err != nil {
		return contexty.ResourceBody{}, err
	}
	if request.ScopeRef != "read-old-episode" || !safeID(request.Resource.Reference.ID) {
		return contexty.ResourceBody{}, contexty.ErrResourceDenied
	}
	path := filepath.Join(j.directory, request.Resource.Reference.ID+".json")
	info, err := os.Stat(path)
	if err != nil {
		return contexty.ResourceBody{}, err
	}
	// Resource Length bounds the typed artifact, while the file has a small
	// envelope. This host adds a fixed bounded envelope allowance before loading.
	const envelopeAllowance, maxEpisodeBytes = 256, 4096
	if request.MaxBytes < 0 || request.MaxBytes > maxEpisodeBytes {
		return contexty.ResourceBody{}, contexty.ErrResourceSizeLimit
	}
	if request.Resource.Length > request.MaxBytes || info.Size() > request.MaxBytes+envelopeAllowance {
		return contexty.ResourceBody{}, contexty.ErrResourceSizeLimit
	}
	file, err := os.Open(path)
	if err != nil {
		return contexty.ResourceBody{}, err
	}
	bound := request.MaxBytes + envelopeAllowance
	wire, readErr := io.ReadAll(io.LimitReader(file, bound+1))
	closeErr := file.Close()
	if readErr != nil {
		return contexty.ResourceBody{}, readErr
	}
	if closeErr != nil {
		return contexty.ResourceBody{}, closeErr
	}
	if int64(len(wire)) > bound {
		return contexty.ResourceBody{}, contexty.ErrResourceSizeLimit
	}
	j.reads++
	var body contexty.ResourceBody
	if err = json.Unmarshal(wire, &body); err != nil {
		return contexty.ResourceBody{}, err
	}
	return body, nil
}

type archiveSummary struct{ calls int }

func (s *archiveSummary) Summarize(ctx context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
	if err := ctx.Err(); err != nil {
		return contexty.Message{}, err
	}
	s.calls++
	message := contexty.TextMessage(contexty.RoleAssistant, "Earlier episodes available by source reference.")
	message.ID = "summary-" + strconv.Itoa(s.calls)
	for _, input := range request.Messages {
		message.SourceRefs = append(message.SourceRefs, input.SourceRefs...)
	}
	return message, nil
}

type originalProjection struct{}

func (originalProjection) ProjectResource(
	_ context.Context,
	body contexty.ResourceBody,
) (contexty.ContextArtifact, error) {
	return body.Artifact.Clone(), nil
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	directory, err := os.MkdirTemp("", "contexty-archive-")
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			log.Print(removeErr)
		}
	}()
	report, err := archiveResume(ctx, directory)
	if err != nil {
		return err
	}
	wire, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(wire))
	return nil
}

func archiveResume(ctx context.Context, directory string) (scenarioReport, error) {
	archive := &journal{directory: directory, reads: 0}
	summary := &archiveSummary{calls: 0}
	const hardLimit, triggerPercent, targetPercent = archiveBudget, 80, 70

	pipe := contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget:     contexty.EffectiveInputBudget(hardLimit),
		Summarizer: summary,
		Compaction: &contexty.CompactionPolicy{
			Descriptor:     contexty.Descriptor{ID: "host-archive-summary", Revision: archiveRevision},
			TriggerPercent: triggerPercent,
			TargetPercent:  targetPercent,
		},
	}, contexty.CharTokenEstimator{}, contexty.WithRollingSummary(contexty.RollingSummaryPolicy{
		Descriptor: contexty.Descriptor{ID: "host-recent-tail", Revision: archiveRevision}, RecentMessages: 1,
	}))
	working, oldRef, err := buildWorking(ctx, archive, pipe)
	if err != nil {
		return scenarioReport{}, err
	}
	state := contexty.EmptyState().WithSegment(contexty.SegmentHistory, working)
	projected, err := contexty.ProjectCheckpoint(
		state,
		contexty.DefaultJSONSerializer(),
		contexty.Descriptor{ID: "", Revision: ""},
	)
	if err != nil {
		return scenarioReport{}, err
	}
	store := contexty.NewMemoryConversationStateStore()

	if err = store.CommitState(
		ctx,
		"session",
		0,
		contexty.ConversationDelta{
			Operation: contexty.DeltaReplaceSegment,
			Segment:   contexty.SegmentHistory,
			Messages:  projected.Segment(contexty.SegmentHistory),
		},
	); err != nil {
		return scenarioReport{}, err
	}
	checkpoint, err := store.LoadState(ctx, "session")
	if err != nil {
		return scenarioReport{}, err
	}
	resumed, err := reopenCheckpoint(directory, checkpoint)
	if err != nil {
		return scenarioReport{}, err
	}
	// New host journal instance demonstrates reopening files, not durability of
	// the reference in-memory state store or blob adapter.
	reopened := &journal{directory: directory, reads: 0}
	var retainedRef contexty.SourceRef
	for _, message := range resumed.Segment(contexty.SegmentHistory) {
		for _, ref := range message.SourceRefs {
			if ref == oldRef {
				retainedRef = ref
			}
		}
	}
	selected, err := reopened.selectEpisode(retainedRef)
	if err != nil {
		return scenarioReport{}, err
	}
	resolved, err := resolveOriginal(ctx, reopened, selected)
	if err != nil {
		return scenarioReport{}, err
	}
	absent := true
	for _, message := range resumed.Segment(contexty.SegmentHistory) {
		absent = absent && !strings.Contains(message.TextContent(), "JADE-713")
	}
	return scenarioReport{Compactions: summary.calls, Reads: reopened.reads, OriginalAbsent: absent,
		Restored: resolved.Message.TextContent(), Reference: selected.Content}, nil
}

func reopenCheckpoint(directory string, checkpoint contexty.ConversationState) (contexty.ConversationState, error) {
	codec := contexty.ConversationCodec{
		OpaqueProfile: contexty.Descriptor{ID: "", Revision: ""},
		Provenance:    contexty.DefaultProvenanceRegistry(),
		Extensions:    contexty.NewExtensionRegistry(),
	}
	wire, err := codec.Encode(checkpoint)
	if err != nil {
		return contexty.ConversationState{}, err
	}
	path := filepath.Join(directory, "working-checkpoint.json")
	if err = os.WriteFile(path, wire, 0o600); err != nil {
		return contexty.ConversationState{}, err
	}
	wire, err = os.ReadFile(path)
	if err != nil {
		return contexty.ConversationState{}, err
	}
	return codec.Decode(wire)
}

func resolveOriginal(
	ctx context.Context,
	archive *journal,
	selected contexty.ResourceDescriptor,
) (contexty.ResolvedResource, error) {
	profile := contexty.EstimateProfile{
		Model: contexty.Descriptor{ID: "offline", Revision: archiveRevision},
		Estimator: contexty.Descriptor{
			ID:       "runes",
			Revision: archiveRevision,
		},
		Method: contexty.Descriptor{ID: "approximate", Revision: archiveRevision},
		Encoding: contexty.Descriptor{
			ID:       "semantic",
			Revision: archiveRevision,
		},
		Capabilities: map[contexty.EstimateKind]contexty.EstimateQuality{
			contexty.EstimateText:       contexty.EstimateEstimated,
			contexty.EstimateImage:      contexty.EstimateUnknown,
			contexty.EstimateToolCall:   contexty.EstimateUnknown,
			contexty.EstimateToolResult: contexty.EstimateUnknown,
			contexty.EstimateMedia:      contexty.EstimateUnknown,
			contexty.EstimateExtension:  contexty.EstimateUnknown,
		},
	}
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		profile,
		contexty.DefaultJSONSerializer(),
	)
	if err != nil {
		return contexty.ResolvedResource{}, err
	}

	resolver := contexty.ResourceResolver{
		Reader:             archive,
		ReaderIdentity:     contexty.Descriptor{ID: "host-file-journal", Revision: archiveRevision},
		Projection:         originalProjection{},
		ProjectionIdentity: contexty.Descriptor{ID: "original", Revision: archiveRevision},
		Reporter:           reporter,
		Materialization: &contexty.ArtifactMaterializationPolicy{
			Identity: contexty.Descriptor{ID: "host-original-user", Revision: archiveRevision},
			Materialize: func(_ context.Context, artifact contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
				parts, partsErr := contexty.ArtifactContentParts(artifact)
				return contexty.ArtifactRepresentation{Role: contexty.RoleUser, Parts: parts}, partsErr
			},
		},
	}
	return resolver.Resolve(ctx, contexty.ResourceResolveRequest{ID: "old-original", Read: contexty.ResourceReadRequest{
		ScopeRef: "read-old-episode",
		Resource: selected,
		MaxBytes: selected.Length,
	}, Budget: contexty.EffectiveInputBudget(archiveBudget)})
}

func buildWorking(
	ctx context.Context,
	archive *journal,
	pipe *contexty.BudgetPipeline,
) ([]contexty.Message, contexty.SourceRef, error) {
	const detailRepeats = 3
	var working []contexty.Message
	var oldRef contexty.SourceRef
	for i := range 8 {
		text := "Warehouse access code is JADE-713."
		if i > 0 {
			text = strings.Repeat("Unrelated episode detail. ", detailRepeats)
		}
		message := contexty.TextMessage(contexty.RoleUser, text)
		message.ID = "episode-" + strconv.Itoa(i)
		ref, err := archive.save(message)
		if err != nil {
			return nil, contexty.SourceRef{}, err
		}
		message.SourceRefs = []contexty.SourceRef{ref}
		if i == 0 {
			oldRef = ref
		}
		result, err := pipe.Apply(ctx, append(working, message))
		if err != nil {
			return nil, contexty.SourceRef{}, err
		}
		working = result.Messages
	}
	return working, oldRef, nil
}
