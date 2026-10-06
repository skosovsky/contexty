# Review remediation contracts

This specification is the contract baseline for Task 24. It selects the final
semantics before their implementation. The execution journal records which gates
have been implemented and verified; this document alone is not completion evidence.
Existing behavior that contradicts this baseline is a defect to fix in stages 3–5.

## Public API and configuration

- D01: `WithBudgetPipeline(pipe)` configures the whole compiled request. Only
  optional history is evicted. Fixed segments, pending and current turn participate
  in one aggregate estimate; request overhead is charged once (F08).
- D02: `Provenance` is a host extension port with `ProvenanceType() string` and
  `CloneProvenance() Provenance`. Clone owns all mutable data and retains type
  identity. Built-in user/system wire discriminators remain unchanged. Registries
  panic on invalid registration (empty type, nil decoder, duplicate); wire decode
  returns errors on nil/typed-nil or discriminator mismatch (F12), preserving
  callback errors. No replacement with system/empty metadata.
- D03: budget execution requires an explicitly supplied, nonnil estimator,
  including typed nil. Invalid estimator/configuration fails before callbacks.
  Constructors remain pointer-returning; Apply/compile validates configuration.
  CharTokenEstimator is an explicit deterministic test approximation.
- D04: nil System/History/Memory means inherit stored data; a nonnil empty slice
  means clear that segment for this compilation only. Tools always comes from the
  request. Freeze/normalization must preserve the distinction before store merge.
- D05: transform hooks and segment formatters operate on source-derived segments,
  not Pending/CurrentTurn. Role projection applies to every prompt message,
  including Pending/CurrentTurn; raw/writeback/persistence retain their roles.
  OutputPolicy applies to all outputs after shaping. No implicit raw sanitization.
- D06: DeduplicateByLayer replaces an entire incoming group, retaining all its
  messages and order. Layer namespace is the tuple (TemplateID, LayerID); empty
  TemplateID is its own namespace. Missing LayerID messages append unchanged.
- D07: nil and an entirely zero CurrentTurn mean absence. PromptSafe without Raw
  or an unknown Persistence value is invalid even with empty Raw; no fallback.
- D08: Compile with a configured StateStore requires a nonempty conversation ID
  before load or callbacks. CompileSnapshot is explicitly stateless.
- D16: ResourceCodec declares current custom `Codecs []CodecBinding`. Compare
  actual current topology and revisions with saved configuration before decoding.
  Never obtain current revisions from saved data. Built-in codec identities remain
  intrinsic. This is compatibility/binding validation, not authenticity proof.
- D24: DeltaRemoveArtifact uses `ArtifactIDs` / `artifact_ids`; MessageIDs belongs
  to DeltaRemoveMessages. Old artifact-removal payloads with message_ids are
  rejected by encode, decode and application. Migrate them explicitly offline;
  ordinary state/message wire formats remain unchanged by this field change.
- D39/F11: RenderView is a snapshot renderer. Its registered view names cannot
  collide with built-ins or duplicate another registration. Errors precede any
  rendering callback. CompileTarget.View selects built-in formats; it does not
  consult this registry. The APIs deliberately have different scopes.
- D40/D12: DropHead.MinMessages is the minimum size of an optional retained
  block, not protection of messages. Keep the field name and document this meaning;
  RetentionPolicy provides protection. Negative MinMessages/nontext weights fail;
  only documented zero defaults normalize. Effective config enters evidence.

## Identity, data and estimate boundaries

F03: durable logical event identity is independent of content and current history
length. Explicit IDs are preserved. Built-in durable assignment for Pending and
CurrentTurn uses unambiguous host TurnID + event kind + ordinal within that turn;
retry reproduces IDs after trimmed/compacted history. Historical/static messages
need explicit IDs or a host policy with sufficient event identity. Missing identity
fails explicitly. Positional/content fingerprints are observer diagnostics only;
MessageContentRef represents content revision. Prompt-safe/raw share logical ID.
Generated transform outputs use their explicit deterministic stage identity and
inputs, never a random suffix or collision retry.

F04: both compile entry points validate all deferred segments/policies, including
resource-aware blocks, before store/resolver effects, independent of recording.
Only empty segment=memory and policy=append are defaults. Unknown enum values fail.

F05: AST boundaries reject typed nil, canonicalize to owned value parts, and then
validate exactly the returned representation. Pointer/value behavior is equivalent
for helpers, CompileSnapshot and ResourceResolver. Materialization forbids tool
calls/results, validates media and returns no output on failure.

D10/D13/F06/F07: estimator callbacks receive owned inputs and returned weights are
owned. Cancellation is checked before and after callbacks. Totals and components
are nonnegative, per-message lengths match, addition/multiplication is checked,
and quotient/remainder ceiling division cannot overflow. Invalid arithmetic yields
a typed invalid-estimate error, never saturation or admission. Per-message weights
must sum to total for the same request (including any once-assigned overhead).
Eviction decisions for a candidate use its complete request estimate; cross-request
additivity is not assumed. The final aggregate budget check remains mandatory.
Raw text approximations reject unsupported media and binary tool payloads rather
than quietly counting zero. FixedEstimator is explicitly a configured structural
approximation, not provider tokenization. Reporter capability/coverage and strict
admission remain explicit; positive unknown-quality fallback is not measured cost.

F09: positive Redis TTL always produces a positive millisecond duration (ceil),
with overflow checked; zero alone means persistent. Expiry consumes OCC identity.
F10: memory mutation rechecks cancellation after acquiring the lock and before
publication, preserving state/version when cancellation was observed while waiting.
D28: Postgres clear uses version/OCC without decoding old payload. All stores keep
ABA tombstones and atomic batch publication.

## Ownership and concurrency matrix

| Boundary | Ownership / freeze timing | Concurrent use and reentrancy | Cancellation |
| --- | --- | --- | --- |
| Engine | Options configured once before use; built-in mutable option data copied at documented registration/entry boundaries | Compile/Render concurrent only with thread-safe shared host callbacks; engine configuration is not mutated after construction | Before/after host effects; no partial result on error |
| BudgetPipeline | Config slices/policies copied at construction; input/result and callback inputs are owned | Reusable with thread-safe estimator/summarizer/observer; custom strategy owns its synchronization | Before/after callback and final admission |
| Extension/Provenance registries | Registration locked; serializer/profile takes independent registry snapshot | Register/Decode synchronized; decoder runs outside registry lock and may use registry; callback itself must be safe for reuse | Synchronous standalone codec has no context; contextual wrappers check around decoder |
| ConversationState/Snapshot | Private state immutable; getters/With*/AllSegments produce owned deep copies | Safe read reuse; returned DTO slices are mutable caller-owned data | Synchronous transformations make no cancellation promise |
| Memory conversation store | State owned at load/commit boundaries; atomic OCC mutation under mutex | Operations serialize; codecs execute under mutex and must not reenter this store | Recheck after lock and before mutation (F10 gate) |
| Memory blob/resource stores | Own bytes/metadata; no automatic deletion of identity tombstones | Store/reader reuse protected by locks; host policies separate | Early ctx/known-size rejection and recheck before publication |
| ResourceResolver | Own read/materialization/projection/estimate outputs; registry/config snapshots bind evidence | Reader/projector/label/estimator callbacks must support concurrent reuse if resolver reused concurrently | Before/after every port/codec boundary |
| Hooks/formatters/policies | Callback gets owned data; returned data cloned/validated before transfer | No purity assumption, host synchronizes shared mutable state; no hidden callback-result cache | Checked before/after; cancellation does not undo host side effects |

This matrix specifies requirements and existing boundaries, not proof of race
freedom. Stage 6 attaches tests and measurements for claimed concurrent reuse.

## Feature-spec reconciliation

CTX-001: existing lineage/label/output/export contracts remain; F05/F12 strengthen
metadata/type integrity. CTX-002: existing recording/replay/compaction remain;
F03 and codec identity checks strengthen stable replay. CTX-003: blob ports/claims
remain host-controlled (D17–20); no implicit recovery archive. CTX-004: existing
resource ports remain outside discovery/auth/runtime (D21); D16 pins codecs.
CTX-005: F06–08 and D03/10/13 enforce ownership/arithmetic/aggregate accounting;
no provider SDK or measured usage is added. CTX-006: semantic prefix diagnostics
remain; wire/cache-hit confirmation belongs to the adapter (D35). These changes
close concrete contract gaps without duplicating the six existing features.

## Private stage 6 changes

Append may transfer privately owned message slices into immutable state without a
second clone. Caller-owned delta messages remain cloned, and returned snapshots
retain independent mutable getters. No host callback purity or address-based cache
is assumed; repeated estimates keep their final validation gates.

Blob memory Put checks cancellation and known byte length before cloning, hashing
or authorization, then retains cancellation/size checks before publication. This
changes invalid-input error precedence deliberately, without weakening admission.

A private compileSession groups execution context, recorder, resource state and
stage identity explicitly. Prepared/main/target stages retain separate bindings;
a private context bridge carries these dependencies through existing contextual
callbacks and helper paths. It does not add a public orchestrator or cache callbacks.

Durable event TurnID and identity prefix must be valid UTF-8. Invalid byte strings
are rejected with ErrMissingEventIdentity, because JSON replacement of malformed
UTF-8 would otherwise make distinct host strings share an ID.


Segment formatter registration requires one of System/History/Tools/Memory and
a nonnil callback. Unknown/empty segment or nil formatter is invalid compile
configuration on both entry points, with/without recording, before store/resolver
callbacks. Formatter options retain normal last-option-wins registration for the
same known segment. No unknown segment is silently skipped.
