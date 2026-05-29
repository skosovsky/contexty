// Package contexty is a semantic context engine for LLM applications.
//
// Core concepts:
//   - Typed message AST (ContentPart, Annotations, Provenance)
//   - ConversationStore with named segments and optimistic concurrency
//   - Non-mutating Render(ViewType) projections
//   - BudgetPipeline (summarize + truncate) and Engine.Compile → AbstractPayload
//
// Example:
//
//	store := contexty.NewMemoryConversationStore()
//	engine := contexty.NewEngine(
//	    contexty.WithConversationID("chat-1"),
//	    contexty.WithStore(store),
//	)
//	payload, err := engine.Compile(ctx)
//	_, _ = payload, err
package contexty
