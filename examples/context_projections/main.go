// Independent answer and classifier consumers share preparation, not budget losses.
package main

import (
	"context"
	"fmt"

	"github.com/skosovsky/contexty"
)

func message(id, text string) contexty.Message {
	value := contexty.TextMessage(contexty.RoleUser, text)
	value.ID = id
	return value
}

func budget(limit int) *contexty.BudgetPipeline {
	//nolint:exhaustruct_v5 // This example configures only an effective input limit.
	return contexty.NewBudgetPipeline(contexty.BudgetConfig{
		Budget: contexty.EffectiveInputBudget(limit),
	}, &contexty.FixedEstimator{TokensPerMessage: 1, TokensPerContentPart: 0, TokensPerToolCall: 0})
}

func prioritizeRecent(_ context.Context, candidates []contexty.ContextCandidate) ([]contexty.SelectionChoice, error) {
	choices := make([]contexty.SelectionChoice, 0, len(candidates))
	for _, candidate := range candidates {
		// Priority is a host decision. Core still renders admitted units chronologically.
		choices = append(choices, contexty.SelectionChoice{Ref: candidate.Ref, Priority: candidate.Ordinal})
	}
	return choices, nil
}

func main() {
	const mainLimit, answerLimit, classifierLimit = 3, 5, 2
	ctx := context.Background()
	turn := contexty.NewCurrentTurn(message("current", "Choose the next step."))
	artifact := contexty.NewRetrievalDocument(
		"guide",
		contexty.TextPayload("Read-only operating guide."),
	).ContextArtifact.WithTurn(
		"turn-1",
	)
	artifactRef, err := contexty.ArtifactContentRef(artifact)
	if err != nil {
		panic(err)
	}
	engine := contexty.NewEngine(contexty.WithBudgetPipeline(contexty.SegmentHistory, budget(mainLimit)))
	//nolint:exhaustruct_v5 // Explicit composition; unrelated compile features remain disabled.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		TurnID: "turn-1",
		History: []contexty.Message{message("old", "First question."), message("middle", "Earlier evidence."),
			message("recent", "Latest evidence.")},
		Artifacts: []contexty.ContextArtifact{artifact}, CurrentTurn: &turn,
		Targets: []contexty.CompileTarget{
			{
				Name:     "answer",
				Segments: []contexty.SegmentName{contexty.SegmentHistory},
				ArtifactRefs: []contexty.ContentRef{
					artifactRef,
				},
				IncludeCurrentTurn: true,
				Budget:             budget(answerLimit),
			},
			{Name: "classifier", Segments: []contexty.SegmentName{contexty.SegmentHistory},
				IncludeCurrentTurn: true, Budget: budget(classifierLimit), Selection: &contexty.SelectionPolicy{
					Identity: contexty.Descriptor{ID: "host/recent-first", Revision: "1"}, Select: prioritizeRecent,
				}},
		},
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("prepared history=%d main history=%d answer=%d classifier=%d\n",
		len(result.PreparedSnapshot.Segment(contexty.SegmentHistory)), len(result.Payload.History),
		len(result.Projections["answer"].Messages), len(result.Projections["classifier"].Messages))

	// The host chooses main for durability and restores its compile-only changes.
	// Answer/classifier summaries or prompt snapshots are not merged into this state.
	chosen := contexty.EmptyState().WithSegment(contexty.SegmentHistory,
		result.DerivePersistenceProjection(contexty.SegmentHistory)).WithArtifacts(result.Artifacts)
	checkpoint, err := contexty.ProjectCheckpoint(chosen)
	if err != nil {
		panic(err)
	}
	store := contexty.NewMemoryConversationStateStore()
	loaded, err := store.LoadState(ctx, "example")
	if err != nil {
		panic(err)
	}
	//nolint:exhaustruct_v5 // One explicit history replacement suffices for this checkpoint example.
	err = store.CommitState(ctx, "example", loaded.Version(), contexty.ConversationDelta{
		Operation: contexty.DeltaReplaceSegment, Segment: contexty.SegmentHistory,
		Messages: checkpoint.Segment(contexty.SegmentHistory),
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("checkpoint history=%d\n", len(checkpoint.Segment(contexty.SegmentHistory)))
}
