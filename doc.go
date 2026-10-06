// Package contexty is a semantic context engine for LLM applications.
//
// Core concepts:
//   - Typed message AST (ContentPart, Actor, SourceRef, Provenance)
//   - CurrentTurn for prompt-safe active input with explicit persistence policy
//   - MessageIdentityPolicy and CompileWritebackIntent for durable normalization
//   - Typed tool payloads and canonical ToolRound validation
//   - ContextArtifact lifecycle, ownership, and typed artifact codecs
//   - ArtifactMaterializationPolicy for explicit provider role and typed parts
//   - OutputPolicy for the complete final prompt-only projection/validation boundary
//   - ConversationState and ConversationDelta for immutable transitions
//   - ConversationStateStore with optimistic concurrency
//   - CompileTarget and CompileProjection for independent explicit outputs from shared preparation
//   - CompileRequest.Options for resolve vars and low-level compile options
//   - CompileResult.Source + NormalizedSnapshot + Writeback + Projections + Introduced
//   - CompileProjection.Source + InputSnapshot for local preparation traceability
//   - SelectionPolicy for exact atomic candidate admission with host priorities
//   - Fallible DerivePersistenceState and ProjectCheckpoint for explicit persistence
//   - RetentionPolicy, budget-aware SummaryRequest and explicit BudgetDecision
//   - CompactionPolicy for early compression with a soft target
//   - BudgetPipeline (retention with summary or eviction) and Engine.Compile → CompileResult
//   - BudgetRequest for effective input capacity or a window with reservations
//   - TransformChain and Lineage for ordered transitions and full content identity
//   - OpaqueState envelopes with host codecs, placement and exact context dependencies
//   - Host LabelProjection and codecs for Bring Your Own Types metadata
//   - CompileManifest and accepted SavedCompileRecord for exact replay without execution
//   - CompactionRecord, explicit tool-round state and opt-in rolling summaries
//   - ExportEnvelope for allowlisted isolated consumers, without local snapshots
//   - BlobOffloader/BlobResolver with host authorization, retention and cleanup
//   - Selected ResourceDescriptor/ResourceResolver through typed DeferredResult
//   - EstimateReporter for actual count quality, coverage and separate wire evidence
//   - PrefixRecipe for opt-in semantic prefix diagnostics after final admission
//   - PrefixWireConfirmation for separate adapter-owned wire identity, not cache hits
//   - Executable host recipes for archives, offload, summary, retrieval and evaluation
//
// Clear/recreate and payload expiry preserve monotonic OCC identity. Reload the
// empty state and use its current token before writing; old tokens remain stale.
// Source, prompt-safe output and persistence projections are separate owned
// snapshots. Host ports receive owned values and cancellation stops compilation.
// No configured OutputPolicy implies no sanitization. Artifact messages require
// an explicit host materialization policy; provider roles do not establish trust.
// Raw capture and persistence are separate from prompt acceptance.
// Remote execution, discovery, permissions, durable record/blob storage and
// deletion policy are application responsibilities, not inferred from content.
// Working checkpoints and summaries are not transcript archives. Offline strategy
// fixtures verify mechanics, not real-model quality or provider usage/cost. Host
// recipes and evaluation boundaries are documented in docs/context-strategies.md;
// model SDKs, external search and durable retention backends remain outside core.
//
// Example:
//
//	store := contexty.NewMemoryConversationStateStore()
//	_ = store.CommitState(ctx, "chat-1", 0, contexty.ConversationDelta{
//	    Operation: contexty.DeltaAppendMessages,
//	    Segment:   contexty.SegmentHistory,
//	    Messages:  historyMsgs,
//	})
//	engine := contexty.NewEngine(
//	    contexty.WithConversationID("chat-1"),
//	    contexty.WithStateStore(store),
//	)
//	result, err := engine.Compile(ctx, contexty.CompileRequest{
//	    TurnID:                 "turn-1",
//	    CurrentTurn:            &currentTurn,
//	    IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("chat"),
//	    RequireDurableIdentity: true,
//	})
//	_, _ = result.Payload, err
package contexty
