package memory_test

import (
	"context"
	"fmt"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/contexty/adapters/blob/memory"
)

func ExampleStore() {
	ctx := context.Background()
	store, err := memory.New(memory.Config{Namespace: "application", MaxObjectBytes: 64,
		Authorize: func(_ context.Context, access memory.Access) error {
			// The real host checks current permissions here; refs never grant them.
			if access.ScopeRef != "host-approved" {
				return contexty.ErrBlobDenied
			}
			return nil
		}})
	if err != nil {
		panic(err)
	}
	prepared, err := contexty.PutBlob(ctx, store, contexty.BlobPutRequest{
		ScopeRef: "host-approved", RetentionRef: "provisional-claim",
		Content: contexty.BlobContent{MIMEType: "text/plain", Bytes: []byte("payload")}, Sources: nil,
	})
	if err != nil {
		panic(err)
	}
	// Compile a prepared artifact and persist its checkpoint in the host.
	// This example assumes authoritative confirmation of that checkpoint commit.
	// Unknown outcome: keep the provisional claim and reconcile; never abort it.
	accepted := contexty.Descriptor{ID: "checkpoint", Revision: "accepted"}
	if err = store.CommitCheckpoint(
		ctx,
		"host-approved",
		accepted,
		[]string{prepared.Published.RetentionRef},
	); err != nil {
		panic(err)
	}
	// If persistence had definitively failed instead, use ReconcileCleanup with
	// Object/ScopeRef/RetentionRef from prepared.Published, not ReleaseCheckpoint.
	// Only after the host retires this accepted checkpoint may its claim release.
	if err = store.ReleaseCheckpoint(ctx, "host-approved", accepted); err != nil {
		panic(err)
	}
	deleted, err := store.ReconcileCleanup(ctx, "host-approved", contexty.BlobCleanupIntent{
		Object: prepared.Published.Object, ScopeRef: prepared.Published.ScopeRef,
		RetentionRef: prepared.Published.RetentionRef,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(deleted)
	// Output: true
}
