package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/skosovsky/contexty"
)

func TestArchiveResumeOriginalOutsideWorkingCheckpoint(t *testing.T) {
	t.Parallel()
	// Arrange: a fresh host journal, independent of the memory checkpoint store.
	directory := t.TempDir()
	// Act: actual repeated compaction, explicit checkpoint, reopen and retrieval.
	report, err := archiveResume(t.Context(), directory)
	// Assert: the task's necessary original survives outside the prompt window.
	if err != nil {
		t.Fatal(err)
	}
	if report.Compactions < 2 || !report.OriginalAbsent || report.Reads != 1 {
		t.Fatalf("unexpected lifecycle: %+v", report)
	}
	if report.Restored != "Warehouse access code is JADE-713." || report.Reference.ID != "episode-0" ||
		report.Reference.Digest == "" {
		t.Fatalf("original or provenance lost: %+v", report)
	}
	wire, err := os.ReadFile(filepath.Join(directory, "episode-0.event.json"))
	if err != nil {
		t.Fatal(err)
	}
	var original contexty.Message
	if err = contexty.DefaultJSONSerializer().Unmarshal(wire, &original); err != nil {
		t.Fatal(err)
	}
	if original.ID != "episode-0" || original.Role != contexty.RoleUser || original.TextContent() != report.Restored {
		t.Fatalf("original event changed: %+v", original)
	}
}

func TestJournalResourceContracts(t *testing.T) {
	t.Parallel()
	// Arrange: original body and descriptor metadata, with zero body reads.
	archive := &journal{directory: t.TempDir(), reads: 0}
	message := contexty.TextMessage(contexty.RoleUser, "original secret")
	message.ID = "old"
	ref, err := archive.save(message)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := archive.selectEpisode(ref)
	if err != nil {
		t.Fatal(err)
	}
	if archive.reads != 0 {
		t.Fatal("catalog discovery loaded a body")
	}
	// Act/Assert: denied and undersized reads cannot disclose the body.
	_, err = archive.ReadResource(
		t.Context(),
		contexty.ResourceReadRequest{ScopeRef: "wrong", Resource: descriptor, MaxBytes: descriptor.Length},
	)
	if !errors.Is(err, contexty.ErrResourceDenied) || archive.reads != 0 {
		t.Fatalf("denial: %v reads=%d", err, archive.reads)
	}
	_, err = archive.ReadResource(
		t.Context(),
		contexty.ResourceReadRequest{ScopeRef: "read-old-episode", Resource: descriptor, MaxBytes: 1},
	)
	if !errors.Is(err, contexty.ErrResourceSizeLimit) || archive.reads != 0 {
		t.Fatalf("bound: %v reads=%d", err, archive.reads)
	}
	// Act/Assert: a stale revision fails the real resolver identity contract.
	stale := descriptor
	stale.Reference.Revision = "stale"
	_, err = resolveOriginal(t.Context(), archive, stale)
	if !errors.Is(err, contexty.ErrResourceMismatch) {
		t.Fatalf("stale revision: %v", err)
	}
	// Act/Assert: corruption with unchanged revision fails its pinned digest.
	body, err := archive.load(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	body.Artifact.Payload = contexty.TextPayload("altered secret!")
	wire, err := json.Marshal(body) //nolint:musttag // Library ResourceBody is a host port without wire tags.
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(archive.directory, "old.json"), wire, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = resolveOriginal(t.Context(), archive, descriptor)
	if !errors.Is(err, contexty.ErrResourceMismatch) {
		t.Fatalf("stale digest: %v", err)
	}
}

func TestArchiveCancellation(t *testing.T) {
	t.Parallel()
	// Arrange.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Act.
	_, err := archiveResume(ctx, t.TempDir())
	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
