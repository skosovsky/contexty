// Full-assembly example: Engine + ConversationStore + budget pipeline + Compile.
// Run with: go run .
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/skosovsky/contexty"
)

const (
	fixedTokensPerMsg  = 25
	exampleTokenLimit  = 200
	conversationMinMsg = 2
)

func main() {
	ctx := context.Background()
	result, engine, err := buildPrompt(ctx)
	if err != nil {
		log.Fatal(err)
	}
	msgs := result.Payload.FlattenMessages()
	fmt.Printf("Compiled %d messages\n", len(msgs))
	for i, m := range msgs {
		fmt.Printf("  [%d] %s: %q\n", i, m.Role, m.TextContent())
	}
	if err := logCompileArtifacts(ctx, engine, result); err != nil {
		log.Fatal(err)
	}
}

func buildPrompt(ctx context.Context) (contexty.CompileResult, *contexty.Engine, error) {
	store := contexty.NewMemoryConversationStore()
	s0, _ := store.Load(ctx, "demo")
	_ = store.UpdateSegment(ctx, "demo", s0.Version(), contexty.SegmentSystem, []contexty.Message{
		withOrigin(contexty.TextMessage(contexty.RoleSystem, "You are a medical assistant."), "agents/medical", "base"),
	})
	_ = store.UpdateSegment(ctx, "demo", 1, contexty.SegmentMemory, []contexty.Message{
		contexty.TextMessage(contexty.RoleSystem, "Patient Name: Anna. Age: 30."),
	})
	_ = store.UpdateSegment(ctx, "demo", 2, contexty.SegmentHistory, fetchConversation())

	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{ //nolint:exhaustruct // optional Summarizer/TruncateStrategy omitted
			TokenLimit: exampleTokenLimit,
			DropHead:   contexty.DropHeadConfig{MinMessages: conversationMinMsg},
		}, &contexty.FixedEstimator{TokensPerMessage: fixedTokensPerMsg})

	engine := contexty.NewEngine(
		contexty.WithConversationID("demo"),
		contexty.WithStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(compileObserver{}),
		contexty.WithTransformHooks(contexty.NewRedactionHook()),
		contexty.WithDeferredBlocks(
			contexty.DeferredBlock{
				Name:        "session_hint",
				Segment:     contexty.SegmentSystem,
				MergePolicy: contexty.PolicyReplaceByOrigin,
				Resolve: func(ctx context.Context) ([]contexty.Message, error) {
					vars := contexty.CompileResolveVarFromContext(ctx)
					locale := "en-US"
					if vars != nil && vars["locale"] != "" {
						locale = vars["locale"]
					}
					return []contexty.Message{
						withOrigin(
							contexty.TextMessage(contexty.RoleSystem, "Session locale: "+locale),
							"agents/medical", "session",
						),
					}, nil
				},
			},
		),
	)
	result, err := engine.Compile(ctx, contexty.CompileRequest{ //nolint:exhaustruct // only Pending for this example
		Pending: []contexty.Message{
			contexty.TextMessage(contexty.RoleUser, "What should I recommend for Anna?"),
		},
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("locale", "en-US"),
			contexty.WithEphemeralPatch(contexty.MessageSelector{
				Segment:  contexty.SegmentHistory,
				Role:     contexty.RoleUser,
				Position: contexty.PositionLast,
			}, "REDACTED"),
		},
	})
	return result, engine, err
}

func logCompileArtifacts(ctx context.Context, engine *contexty.Engine, result contexty.CompileResult) error {
	snap := contexty.EmptySnapshot().
		WithSegment(contexty.SegmentSystem, result.Payload.System).
		WithSegment(contexty.SegmentHistory, result.Payload.History).
		WithSegment(contexty.SegmentTools, result.Payload.Tools).
		WithSegment(contexty.SegmentMemory, result.Payload.Memory)
	view, err := engine.RenderView(ctx, snap, string(contexty.ViewFlatClassifier))
	if err != nil {
		return err
	}
	log.Printf("flat classifier view:\n%s", view)
	for _, seg := range []contexty.SegmentName{
		contexty.SegmentSystem, contexty.SegmentHistory, contexty.SegmentMemory,
	} {
		proj := result.DerivePersistenceProjection(seg)
		log.Printf("persistence projection %s: %d messages", seg, len(proj))
	}
	return nil
}

type compileObserver struct{}

func (compileObserver) OnTokensEstimated(_ context.Context, blockID string, count int) {
	log.Printf("tokens estimated: block=%s count=%d", blockID, count)
}

func (compileObserver) OnNodeEvicted(_ context.Context, nodeID string, reason contexty.EvictionReason) {
	log.Printf("node evicted: id=%s reason=%s", nodeID, reason)
}

func (compileObserver) OnContextSummarized(_ context.Context, ratio float64) {
	log.Printf("context summarized: ratio=%.2f", ratio)
}

func (compileObserver) OnPipelineCompiled(_ context.Context, totalCost int, duration time.Duration) {
	log.Printf("pipeline compiled: cost=%d duration=%s", totalCost, duration)
}

func fetchConversation() []contexty.Message {
	return []contexty.Message{
		contexty.TextMessage(contexty.RoleUser, "What supplements should I take?"),
		contexty.TextMessage(contexty.RoleAssistant, "Consider vitamin D and calcium based on your profile."),
		contexty.TextMessage(contexty.RoleUser, "Any side effects?"),
		contexty.TextMessage(contexty.RoleAssistant, "Generally well tolerated. Discuss with your doctor."),
		contexty.TextMessage(contexty.RoleUser, "Thanks."),
		contexty.TextMessage(contexty.RoleAssistant, "You're welcome."),
	}
}

func withOrigin(msg contexty.Message, templateID, layerID string) contexty.Message {
	msg.Origin = &contexty.MessageOrigin{TemplateID: templateID, LayerID: layerID}
	return msg
}
