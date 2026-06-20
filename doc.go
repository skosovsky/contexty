// Package contexty is a semantic context engine for LLM applications.
//
// Core concepts:
//   - Typed message AST (ContentPart, Actor, SourceRef, Provenance)
//   - Typed tool payloads and canonical ToolRound validation
//   - ContextArtifact lifecycle and ownership for retrieval and memory context
//   - ConversationState and ConversationDelta for immutable transitions
//   - ConversationStateStore with optimistic concurrency
//   - Non-mutating Render(ViewType) and Engine.RenderView named projections
//   - CompileRequest.Options (ephemeral patches, resolve vars)
//   - CompileResult.Source + Introduced + DerivePersistenceProjection for persistence
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
//	    Pending: pendingTurn,
//	})
//	_, _ = result.Payload, err
package contexty
