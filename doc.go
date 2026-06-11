// Package contexty is a semantic context engine for LLM applications.
//
// Core concepts:
//   - Typed message AST (ContentPart, Annotations, Provenance)
//   - ConversationStore with named segments and optimistic concurrency
//   - Non-mutating Render(ViewType) and Engine.RenderView named projections
//   - CompileRequest.Options (ephemeral patches, resolve vars)
//   - CompileResult.Source + Introduced + DerivePersistenceProjection for persistence
//   - BudgetPipeline (summarize + truncate) and Engine.Compile → CompileResult
//
// Example:
//
//	store := contexty.NewMemoryConversationStore()
//	engine := contexty.NewEngine(
//	    contexty.WithConversationID("chat-1"),
//	    contexty.WithStore(store),
//	)
//	result, err := engine.Compile(ctx, contexty.CompileRequest{
//	    History: historyMsgs,
//	    Pending: pendingTurn,
//	})
//	_, _ = result.Payload, err
package contexty
