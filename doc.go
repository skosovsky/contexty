// Package contexty is a semantic context engine for LLM applications.
//
// Core concepts:
//   - Typed message AST (ContentPart, Actor, SourceRef, Provenance)
//   - CurrentTurn for prompt-safe active input with explicit persistence policy
//   - MessageIdentityPolicy and CompileWritebackIntent for durable normalization
//   - Typed tool payloads and canonical ToolRound validation
//   - ContextArtifact lifecycle, ownership, and typed artifact codecs
//   - ConversationState and ConversationDelta for immutable transitions
//   - ConversationStateStore with optimistic concurrency
//   - CompileTarget and CompileProjection for named outputs from one compile pass
//   - CompileRequest.Options for resolve vars and low-level compile options
//   - CompileResult.Source + NormalizedSnapshot + Writeback + Projections + Introduced
//   - CompileProjection.Source + InputSnapshot for target traceability
//   - DerivePersistenceProjection for persistence
//   - BudgetPipeline (summarize + truncate) and Engine.Compile → CompileResult
//
// Example:
//
//	store := contexty.NewMemoryConversationStateStore()
//	_ = store.ApplyDelta(ctx, "chat-1", 0, contexty.ConversationDelta{
//	    Operation: contexty.DeltaAppendMessages,
//	    Segment:   contexty.SegmentHistory,
//	    Messages:  historyMsgs,
//	})
//	engine := contexty.NewEngine(
//	    contexty.WithConversationID("chat-1"),
//	    contexty.WithStateStore(store),
//	)
//	result, err := engine.Compile(ctx, contexty.CompileRequest{
//	    CurrentTurn:            &currentTurn,
//	    IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("chat"),
//	    RequireDurableIdentity: true,
//	})
//	_, _ = result.Payload, err
package contexty
