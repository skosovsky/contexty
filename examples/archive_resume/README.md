Run `go run ./examples/archive_resume` from the repository root. No network,
credentials or provider SDK is used.

The host writes original semantic events to separate `.event.json` files before
building the working context. Resource bodies and catalog descriptors live beside
those events. Six deterministic rolling compactions replace old text with a short
summary that retains source references. The working state is explicitly projected,
committed through the reference state store, encoded to a checkpoint file and
reopened. A retained `SourceRef` selects the excluded episode from catalog metadata;
`ResourceResolver` loads precisely that original with fresh authorization, a byte
bound, pinned revision and typed content digest. The summary contains no access code;
the restored original does. The test also rejects stale revisions/content and denied
or undersized reads.

These files are a host fixture, not a production execution journal or durable store:
there is no fsync, transaction, concurrency or crash-recovery protocol. Reopening a
codec checkpoint demonstrates representation recovery; it does not make the memory
state/blob adapters durable. A production host supplies its own journal and durable
checkpoint/blob backend. Catalog/query types remain local to the example.

The summary callback is deterministic fixture logic. Its repeated compactions and
reference retention prove lifecycle mechanics, not LLM summary quality. Rune counts
are semantic estimates and do not measure provider usage or monetary cost. Retrieved
originals are explicitly materialized with `RoleUser`; their data does not authorize
execution or override system policy.
