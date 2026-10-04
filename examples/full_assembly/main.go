// Full-assembly example: Engine + ConversationStateStore + budget pipeline + Compile.
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
	exampleTokenLimit  = 260
	conversationMinMsg = 2
)

func main() {
	ctx := context.Background()
	result, err := buildPrompt(ctx)
	if err != nil {
		log.Fatal(err)
	}
	msgs := result.Payload.FlattenMessages()
	fmt.Printf("Compiled %d messages\n", len(msgs))
	for i, m := range msgs {
		fmt.Printf("  [%d] %s: %q\n", i, m.Role, m.TextContent())
	}
	logCompileArtifacts(result)
}

func buildPrompt(ctx context.Context) (contexty.CompileResult, error) {
	store := contexty.NewMemoryConversationStateStore()
	if err := seedAssemblyCheckpoint(ctx, store); err != nil {
		return contexty.CompileResult{}, err
	}

	pipe := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{ //nolint:exhaustruct_v5 // optional Summarizer/TruncateStrategy omitted
			Budget:   contexty.EffectiveInputBudget(exampleTokenLimit),
			DropHead: contexty.DropHeadConfig{MinMessages: conversationMinMsg},
		}, &contexty.FixedEstimator{TokensPerMessage: fixedTokensPerMsg})

	engine := contexty.NewEngine(
		contexty.WithArtifactMaterialization(*hostMaterialization()),
		contexty.WithConversationID("demo"),
		contexty.WithStateStore(store),
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipe),
		contexty.WithObserver(compileObserver{}),
		contexty.WithOutputPolicy(*hostEmailPolicy()),
		contexty.WithDeferredBlocks(
			contexty.DeferredBlock{
				Resources:     nil,
				ResourceCodec: contexty.ResourceCodec{Messages: contexty.DefaultJSONSerializer(), Labels: nil},
				Name:          "session_hint",
				Segment:       contexty.SegmentSystem,
				MergePolicy:   contexty.PolicyReplaceByOrigin,
				Resolve: func(ctx context.Context) (contexty.DeferredResult, error) {
					vars := contexty.CompileResolveVarFromContext(ctx)
					locale := "en-US"
					if vars != nil && vars["locale"] != "" {
						locale = vars["locale"]
					}
					return contexty.DeferredResult{Resources: nil, Messages: []contexty.Message{
						withOrigin(
							contexty.TextMessage(contexty.RoleSystem, "Request locale: "+locale),
							"context/defaults", "session",
						),
					}}, nil
				},
			},
		),
	)
	retrieved := contexty.NewRetrievalDocument(
		"decision-note",
		contexty.TextPayload("Current decision: preserve public contracts at package boundaries."),
	).ContextArtifact.WithTurn("turn-1")
	turn := contexty.NewCurrentTurn(
		contexty.TextMessage(contexty.RoleUser, "Summarize the design boundary including sensitive host details."),
	).WithPromptSafe(
		contexty.TextMessage(contexty.RoleUser, "Summarize the design boundary."),
	)
	result, err := engine.Compile(ctx, contexty.CompileRequest{ //nolint:exhaustruct_v5 // optional fields omitted
		TurnID:                 "turn-1",
		Artifacts:              []contexty.ContextArtifact{retrieved},
		CurrentTurn:            &turn,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("demo"),
		RequireDurableIdentity: true,
		Targets: []contexty.CompileTarget{
			{
				Name:         "flat_classifier",
				View:         "",
				Segments:     []contexty.SegmentName{contexty.SegmentHistory},
				ArtifactRefs: nil, IncludeCurrentTurn: false, IncludeArtifacts: false, Selection: nil,
				Budget:    nil,
				Formatter: nil,
			},
		},
		Options: []contexty.CompileOption{
			contexty.WithResolveVar("locale", "en-US"),
		},
	})
	return result, err
}

func logCompileArtifacts(result contexty.CompileResult) {
	if view, ok := result.Projections["flat_classifier"]; ok {
		log.Printf("flat classifier projection:\n%s", view.Text)
	}
	for _, seg := range []contexty.SegmentName{
		contexty.SegmentSystem, contexty.SegmentHistory, contexty.SegmentMemory,
	} {
		proj := result.DerivePersistenceProjection(seg)
		log.Printf("persistence projection %s: %d messages", seg, len(proj))
	}
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
	hostQuestion := contexty.TextMessage(contexty.RoleUser, "Can this package import our app types?")
	hostQuestion.Actor = &contexty.Actor{
		Kind:        "workspace-user",
		ID:          "user-42",
		DisplayName: "Workspace user",
		SourceRefs: []contexty.SourceRef{{
			Namespace:    "host",
			Kind:         "ticket",
			ID:           "ticket-17",
			CheckpointID: "ticket-17:v1",
			URI:          "",
		}},
	}
	hostQuestion.SourceRefs = []contexty.SourceRef{{
		Namespace:    "host",
		Kind:         "message",
		ID:           "msg-1",
		CheckpointID: "msg-1:v1",
		URI:          "",
	}}
	return []contexty.Message{
		hostQuestion,
		contexty.TextMessage(contexty.RoleAssistant, "No. Keep host identity in SourceRefs and Extensions."),
		contexty.TextMessage(contexty.RoleUser, "Where should retrieval snippets live?"),
		contexty.TextMessage(contexty.RoleAssistant, "Use ContextArtifact with an explicit lifecycle."),
		contexty.TextMessage(contexty.RoleUser, "Thanks."),
		contexty.TextMessage(contexty.RoleAssistant, "Persist the resulting state through deltas."),
	}
}

func projectLookupRound() (contexty.ToolRound, error) {
	args, err := contexty.StructuredPayload(struct {
		Query string `json:"query"`
	}{Query: "boundary decision"})
	if err != nil {
		return contexty.ToolRound{}, err
	}
	payload, err := contexty.StructuredPayload(struct {
		Decision string `json:"decision"`
		Scope    string `json:"scope"`
	}{
		Decision: "keep host types outside the package contract",
		Scope:    "source refs and artifacts only",
	})
	if err != nil {
		return contexty.ToolRound{}, err
	}
	return contexty.ToolRound{
		Assistant: contexty.Message{
			ID:   "assistant-tool-call",
			Role: contexty.RoleAssistant,
			Parts: []contexty.ContentPart{
				contexty.ToolCallPart{
					ID:            "lookup-1",
					Name:          "lookup_project_note",
					Arguments:     args,
					ArgumentsBlob: nil,
				},
			},
		},
		Results: []contexty.Message{
			{
				ID:   "tool-lookup-result",
				Role: contexty.RoleTool,
				Parts: []contexty.ContentPart{
					contexty.ToolResultPart{
						ToolCallID: "lookup-1",
						Name:       "lookup_project_note",
						Payload:    payload,
						IsError:    false,
					},
				},
			},
		},
	}, nil
}

func withOrigin(msg contexty.Message, templateID, layerID string) contexty.Message {
	msg.Origin = &contexty.MessageOrigin{TemplateID: templateID, LayerID: layerID}
	return msg
}

func seedAssemblyCheckpoint(ctx context.Context, store contexty.ConversationStateStore) error {
	initial, err := store.LoadState(ctx, "demo")
	if err != nil {
		return err
	}
	toolRound, err := projectLookupRound()
	if err != nil {
		return err
	}
	//nolint:exhaustruct_v5 // Each delta initializes only its operation-specific fields.
	err = store.CommitState(
		ctx,
		"demo",
		initial.Version(),
		contexty.ConversationDelta{
			Operation: contexty.DeltaReplaceSegment,
			Segment:   contexty.SegmentSystem,
			Messages: []contexty.Message{
				withOrigin(
					contexty.TextMessage(contexty.RoleSystem, "Assemble concise project context."),
					"context/defaults",
					"base",
				),
			},
		},
		contexty.ConversationDelta{
			Operation: contexty.DeltaReplaceSegment,
			Segment:   contexty.SegmentMemory,
			Messages: []contexty.Message{
				contexty.TextMessage(contexty.RoleSystem, "Project boundary: keep the library universal."),
			},
		},
		contexty.ConversationDelta{
			Operation: contexty.DeltaReplaceSegment,
			Segment:   contexty.SegmentHistory,
			Messages:  fetchConversation(),
		},
		contexty.ConversationDelta{Operation: contexty.DeltaAppendToolRound, ToolRound: &toolRound},
	)
	if err != nil {
		return err
	}

	return nil
}
