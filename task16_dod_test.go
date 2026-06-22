package contexty_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestDoD_Task16_CurrentTurnPromptProjectionAndPersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	raw := contexty.Message{
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "raw secret text"}},
	}
	promptSafe := contexty.Message{
		Role:  contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "safe text"}},
	}
	turn := contexty.NewCurrentTurn(raw).WithPromptSafe(promptSafe)

	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		TurnID: "turn-1",
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		CurrentTurn:            &turn,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)

	require.Len(t, result.Payload.History, 2)
	assert.Equal(t, "safe text", result.Payload.History[1].TextContent())
	require.NotNil(t, result.Source.CurrentTurn)
	assert.Equal(t, "raw secret text", result.Source.CurrentTurn.Raw.TextContent())
	assert.Equal(t, result.Source.CurrentTurn.Raw.ID, result.Payload.History[1].ID)
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionFormatted,
		Reason: contexty.ReasonCurrentTurnProjection,
	}, result.Transformations[result.Source.CurrentTurn.Raw.ID])

	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 2)
	assert.Equal(t, "h1", proj[0].ID)
	assert.Equal(t, "raw secret text", proj[1].TextContent())
	assert.NotContains(t, proj[1].TextContent(), "safe text")
	writebackHistory := result.Writeback.Snapshot.Segment(contexty.SegmentHistory)
	require.Len(t, writebackHistory, 2)
	assert.Equal(t, "raw secret text", writebackHistory[1].TextContent())
}

func TestDoD_Task16_CurrentTurnCanPersistPromptSafeText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	raw := contexty.TextMessage(contexty.RoleUser, "raw text")
	promptSafe := contexty.TextMessage(contexty.RoleUser, "redacted text")
	turn := contexty.NewCurrentTurn(raw).
		WithPromptSafe(promptSafe).
		WithPersistence(contexty.CurrentTurnPersistPromptSafe)

	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		CurrentTurn:            &turn,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)

	proj := result.DerivePersistenceProjection(contexty.SegmentHistory)
	require.Len(t, proj, 1)
	assert.Equal(t, "redacted text", proj[0].TextContent())
	writebackHistory := result.Writeback.Snapshot.Segment(contexty.SegmentHistory)
	require.Len(t, writebackHistory, 1)
	assert.Equal(t, "redacted text", writebackHistory[0].TextContent())
}

func TestDoD_Task16_CurrentTurnCanSkipPersistenceAndRejectInvalidPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	turn := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "raw text")).
		WithPersistence(contexty.CurrentTurnPersistNone)

	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		CurrentTurn:            &turn,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)
	assert.Empty(t, result.DerivePersistenceProjection(contexty.SegmentHistory))
	assert.Empty(t, result.Writeback.Snapshot.Segment(contexty.SegmentHistory))

	invalid := contexty.NewCurrentTurn(contexty.TextMessage(contexty.RoleUser, "raw text")).
		WithPersistence(contexty.CurrentTurnPersistencePolicy("persist_promt_safe"))
	_, err = contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		CurrentTurn:            &invalid,
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.ErrorIs(t, err, contexty.ErrInvalidCurrentTurnPersistencePolicy)
}

func TestDoD_Task16_DurableIdentityPolicyAndWritebackIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	msg := contexty.TextMessage(contexty.RoleUser, "needs durable id")

	_, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History:                []contexty.Message{msg},
		RequireDurableIdentity: true,
	})
	require.ErrorIs(t, err, contexty.ErrMissingIdentityPolicy)

	policy := contexty.NewStableMessageIdentityPolicy("stable")
	first, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History:                []contexty.Message{msg},
		IdentityPolicy:         policy,
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)
	second, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History:                []contexty.Message{msg},
		IdentityPolicy:         policy,
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)

	require.Len(t, first.Writeback.Messages, 1)
	assert.Equal(t, contexty.SegmentHistory, first.Writeback.Messages[0].Segment)
	assert.Equal(t, first.Payload.History[0].ID, first.Writeback.Messages[0].ID)
	assert.Equal(t, first.Payload.History[0].ID, first.NormalizedSnapshot.Segment(contexty.SegmentHistory)[0].ID)
	assert.Equal(t, first.Payload.History[0].ID, second.Payload.History[0].ID)
}

func TestDoD_Task16_PublicNormalizeFailsClosedAndReturnsWritebacks(t *testing.T) {
	t.Parallel()
	msg := contexty.TextMessage(contexty.RoleUser, "needs durable id")

	_, _, err := (contexty.CompileRequest{
		History:                []contexty.Message{msg},
		RequireDurableIdentity: true,
	}).Normalize()
	require.ErrorIs(t, err, contexty.ErrMissingIdentityPolicy)

	normalized, writebacks, err := (contexty.CompileRequest{
		TurnID:                 "turn-1",
		History:                []contexty.Message{msg},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	}).Normalize()
	require.NoError(t, err)
	require.Len(t, normalized.History, 1)
	require.NotEmpty(t, normalized.History[0].ID)
	require.Len(t, writebacks, 1)
	assert.Equal(t, normalized.History[0].ID, writebacks[0].ID)
	assert.Equal(t, contexty.SegmentHistory, writebacks[0].Segment)
}

func TestDoD_Task16_DurableIdentityUsesHistoryOffsetForPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	msg := contexty.TextMessage(contexty.RoleUser, "same content")

	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History:                []contexty.Message{msg},
		Pending:                []contexty.Message{msg},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)
	require.Len(t, result.Payload.History, 2)
	assert.NotEqual(t, result.Payload.History[0].ID, result.Payload.History[1].ID)
	require.Len(t, result.Writeback.Messages, 2)
	assert.Equal(t, 0, result.Writeback.Messages[0].Index)
	assert.Equal(t, 1, result.Writeback.Messages[1].Index)
}

func TestDoD_Task16_HookIntroducedIDsRespectDurableIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	hook := task16TransformHook{
		fn: func(
			_ context.Context,
			snap contexty.ConversationSnapshot,
		) (contexty.ConversationSnapshot, error) {
			history := snap.Segment(contexty.SegmentHistory)
			history = append(history, contexty.TextMessage(contexty.RoleUser, "hook introduced"))
			return snap.WithSegment(contexty.SegmentHistory, history), nil
		},
	}

	_, err := contexty.NewEngine(contexty.WithTransformHooks(hook)).CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		RequireDurableIdentity: true,
	})
	require.ErrorIs(t, err, contexty.ErrMissingIdentityPolicy)

	first, err := contexty.NewEngine(contexty.WithTransformHooks(hook)).CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)
	second, err := contexty.NewEngine(contexty.WithTransformHooks(hook)).CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
	})
	require.NoError(t, err)
	require.Len(t, first.Payload.History, 2)
	require.NotEmpty(t, first.Payload.History[1].ID)
	assert.Equal(t, first.Payload.History[1].ID, second.Payload.History[1].ID)
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionFormatted,
		Reason: contexty.ReasonTransformHook,
	}, first.Transformations[first.Payload.History[1].ID])
}

func TestDoD_Task16_TypedArtifactCodecRoundTrip(t *testing.T) {
	t.Parallel()
	type retrievedFact struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	desc := contexty.ArtifactCodecDescriptor[retrievedFact]{
		TypeID:      "test.retrieved_fact",
		Kind:        contexty.ArtifactKindRetrievalDocument,
		Lifecycle:   contexty.ArtifactLifecyclePersistent,
		BoundTurnID: "",
		OwnerRef: &contexty.SourceRef{
			Namespace:    "owner",
			Kind:         "workspace",
			ID:           "workspace-1",
			CheckpointID: "",
			URI:          "",
		},
		SourceRefs: []contexty.SourceRef{{
			Namespace:    "source",
			Kind:         "document",
			ID:           "doc-1",
			CheckpointID: "doc-1:v1",
			URI:          "",
		}},
		MergePolicy: contexty.PolicyReplaceByOrigin,
		Budget: &contexty.ArtifactBudgetPolicy{
			Group:      "retrieval",
			TokenLimit: 200,
		},
		Persistence: contexty.ArtifactPersistenceStore,
		Render: func(v retrievedFact) string {
			return v.Title + ": " + v.Body
		},
	}
	artifact, err := contexty.NewTypedArtifact(
		"fact-1",
		desc,
		retrievedFact{Title: "Contract", Body: "typed codec"},
	)
	require.NoError(t, err)
	require.NotNil(t, artifact.OwnerRef)
	assert.Equal(t, "workspace-1", artifact.OwnerRef.ID)
	require.Len(t, artifact.SourceRefs, 1)
	assert.Equal(t, "doc-1", artifact.SourceRefs[0].ID)
	assert.Equal(t, contexty.ArtifactLifecyclePersistent, artifact.Lifecycle)
	assert.Equal(t, contexty.PolicyReplaceByOrigin, artifact.MergePolicy)
	require.NotNil(t, artifact.Budget)
	assert.Equal(t, 200, artifact.Budget.TokenLimit)
	assert.Equal(t, contexty.ArtifactPersistenceStore, artifact.Persistence)

	codec := contexty.ConversationCodec{}
	data, err := codec.Encode(contexty.EmptySnapshot().WithArtifacts([]contexty.ContextArtifact{artifact}))
	require.NoError(t, err)
	decodedSnapshot, err := codec.Decode(data)
	require.NoError(t, err)
	require.Len(t, decodedSnapshot.Artifacts(), 1)
	decodedArtifact := decodedSnapshot.Artifacts()[0]
	assert.Equal(t, "test.retrieved_fact", decodedArtifact.ArtifactType)
	require.NotNil(t, decodedArtifact.OwnerRef)
	assert.Equal(t, "workspace-1", decodedArtifact.OwnerRef.ID)
	require.Len(t, decodedArtifact.SourceRefs, 1)
	assert.Equal(t, "doc-1", decodedArtifact.SourceRefs[0].ID)
	assert.Equal(t, contexty.PolicyReplaceByOrigin, decodedArtifact.MergePolicy)
	require.NotNil(t, decodedArtifact.Budget)
	assert.Equal(t, "retrieval", decodedArtifact.Budget.Group)

	value, err := contexty.DecodeTypedArtifact[retrievedFact](decodedArtifact, desc)
	require.NoError(t, err)
	assert.Equal(t, "Contract", value.Title)

	registry := contexty.NewArtifactCodecRegistry()
	registry.Register(contexty.NewTypedArtifactCodec(desc))
	decodedAny, err := registry.Decode(decodedArtifact)
	require.NoError(t, err)
	assert.Equal(t, retrievedFact{Title: "Contract", Body: "typed codec"}, decodedAny)
}

func TestDoD_Task16_TypedArtifactReplaceByOriginUsesSourceMetadata(t *testing.T) {
	t.Parallel()
	type retrievedFact struct {
		Body string `json:"body"`
	}
	desc := contexty.ArtifactCodecDescriptor[retrievedFact]{
		TypeID:    "test.retrieved_fact",
		Kind:      contexty.ArtifactKindRetrievalDocument,
		Lifecycle: contexty.ArtifactLifecyclePersistent,
		OwnerRef: &contexty.SourceRef{
			Namespace: "owner",
			Kind:      "workspace",
			ID:        "workspace-1",
		},
		SourceRefs: []contexty.SourceRef{{
			Namespace: "source",
			Kind:      "document",
			ID:        "doc-1",
		}},
		MergePolicy: contexty.PolicyReplaceByOrigin,
		Render: func(v retrievedFact) string {
			return v.Body
		},
	}
	oldArtifact, err := contexty.NewTypedArtifact("fact-old", desc, retrievedFact{Body: "old"})
	require.NoError(t, err)
	newArtifact, err := contexty.NewTypedArtifact("fact-new", desc, retrievedFact{Body: "new"})
	require.NoError(t, err)

	state := contexty.EmptySnapshot().WithArtifact(oldArtifact).WithArtifact(newArtifact)
	artifacts := state.Artifacts()
	require.Len(t, artifacts, 1)
	assert.Equal(t, "fact-new", artifacts[0].ID)
	assert.Equal(t, "new", artifacts[0].Payload.PlainText())

	result, err := contexty.NewEngine().CompileSnapshot(context.Background(), contexty.CompileRequest{
		Artifacts: []contexty.ContextArtifact{oldArtifact, newArtifact},
	})
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 1)
	assert.Equal(t, "fact-new", result.Artifacts[0].ID)
	writebackArtifacts := result.Writeback.Snapshot.Artifacts()
	require.Len(t, writebackArtifacts, 1)
	assert.Equal(t, "fact-new", writebackArtifacts[0].ID)
	normalizedArtifacts := result.NormalizedSnapshot.Artifacts()
	require.Len(t, normalizedArtifacts, 1)
	assert.Equal(t, "fact-new", normalizedArtifacts[0].ID)
}

func TestDoD_Task16_CompileMergesStoreAndRequestArtifactsBeforeValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	type retrievedFact struct {
		Body string `json:"body"`
	}
	desc := contexty.ArtifactCodecDescriptor[retrievedFact]{
		TypeID:    "test.retrieved_fact",
		Kind:      contexty.ArtifactKindRetrievalDocument,
		Lifecycle: contexty.ArtifactLifecyclePersistent,
		SourceRefs: []contexty.SourceRef{{
			Namespace: "source",
			Kind:      "document",
			ID:        "doc-1",
		}},
		MergePolicy: contexty.PolicyReplaceByOrigin,
		Render: func(v retrievedFact) string {
			return v.Body
		},
	}
	stored, err := contexty.NewTypedArtifact("fact-1", desc, retrievedFact{Body: "old"})
	require.NoError(t, err)
	incoming, err := contexty.NewTypedArtifact("fact-1", desc, retrievedFact{Body: "new"})
	require.NoError(t, err)
	store := contexty.NewMemoryConversationStateStore()
	require.NoError(t, store.ApplyDelta(ctx, "chat-1", 0, contexty.ConversationDelta{
		Operation: contexty.DeltaUpsertArtifact,
		Artifact:  &stored,
	}))

	result, err := contexty.NewEngine(
		contexty.WithStateStore(store),
		contexty.WithConversationID("chat-1"),
	).Compile(ctx, contexty.CompileRequest{
		Artifacts: []contexty.ContextArtifact{incoming},
	})
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 1)
	assert.Equal(t, "fact-1", result.Artifacts[0].ID)
	assert.Equal(t, "new", result.Artifacts[0].Payload.PlainText())
	writebackArtifacts := result.Writeback.Snapshot.Artifacts()
	require.Len(t, writebackArtifacts, 1)
	assert.Equal(t, "new", writebackArtifacts[0].Payload.PlainText())
}

func TestDoD_Task16_MultiTargetCompileOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history message"}},
		}},
		Memory: []contexty.Message{{
			ID:    "m1",
			Role:  contexty.RoleSystem,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "memory message"}},
		}},
		Targets: []contexty.CompileTarget{
			{
				Name:          "classifier_history",
				View:          "",
				SourceSegment: contexty.SegmentHistory,
				Budget:        nil,
				Formatter: func(_ context.Context, msgs []contexty.Message) ([]contexty.Message, error) {
					out := make([]contexty.Message, len(msgs))
					for i, msg := range msgs {
						out[i] = msg.Clone()
						out[i].Parts = []contexty.ContentPart{
							contexty.TextPart{Text: strings.ToUpper(msg.TextContent())},
						}
					}
					return out, nil
				},
			},
			{
				Name:          "memory_plain",
				View:          "",
				SourceSegment: contexty.SegmentMemory,
				Budget:        nil,
				Formatter:     nil,
			},
		},
	})
	require.NoError(t, err)

	classifier := result.Projections["classifier_history"]
	assert.Equal(t, "HISTORY MESSAGE", classifier.Text)
	require.Len(t, classifier.Messages, 1)
	assert.Equal(t, "h1", classifier.Messages[0].ID)
	assert.Equal(t, "h1", classifier.Source.History[0].ID)
	assert.Contains(t, classifier.Transformations, "h1")

	memory := result.Projections["memory_plain"]
	assert.Equal(t, "memory message", memory.Text)
	require.Len(t, memory.Messages, 1)
	assert.Equal(t, "m1", memory.Messages[0].ID)
}

func TestDoD_Task16_ProjectionInputSnapshotUsesCompiledState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	hook := task16TransformHook{
		fn: func(
			_ context.Context,
			snap contexty.ConversationSnapshot,
		) (contexty.ConversationSnapshot, error) {
			history := snap.Segment(contexty.SegmentHistory)
			history = append(history, contexty.Message{
				ID:    "hooked",
				Role:  contexty.RoleAssistant,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "hooked"}},
			})
			return snap.WithSegment(contexty.SegmentHistory, history), nil
		},
	}

	result, err := contexty.NewEngine(contexty.WithTransformHooks(hook)).CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		Targets: []contexty.CompileTarget{{
			Name:          "history_target",
			View:          "",
			SourceSegment: contexty.SegmentHistory,
			Budget:        nil,
			Formatter:     nil,
		}},
	})
	require.NoError(t, err)

	projection := result.Projections["history_target"]
	assert.Equal(t, []string{"h1"}, task16MessageIDs(projection.Source.History))
	assert.Equal(
		t,
		[]string{"h1", "hooked"},
		task16MessageIDs(projection.InputSnapshot.Segment(contexty.SegmentHistory)),
	)
	assert.Equal(t, []string{"h1", "hooked"}, task16MessageIDs(projection.Messages))
}

func TestDoD_Task16_TargetTraceabilityAndIdentityPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "drop",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "drop me"}},
			},
			{
				ID:    "keep",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "keep me"}},
			},
		},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
		Targets: []contexty.CompileTarget{
			{
				Name:          "budgeted",
				View:          "",
				SourceSegment: contexty.SegmentHistory,
				Budget: contexty.NewBudgetPipeline(
					contexty.BudgetConfig{TokenLimit: 1},
					&contexty.FixedEstimator{TokensPerMessage: 1},
				),
				Formatter: nil,
			},
			{
				Name:          "derived",
				View:          "",
				SourceSegment: contexty.SegmentHistory,
				Budget:        nil,
				Formatter: func(_ context.Context, _ []contexty.Message) ([]contexty.Message, error) {
					return []contexty.Message{contexty.TextMessage(contexty.RoleUser, "derived")}, nil
				},
			},
		},
	}
	first, err := contexty.NewEngine().CompileSnapshot(ctx, req)
	require.NoError(t, err)
	second, err := contexty.NewEngine().CompileSnapshot(ctx, req)
	require.NoError(t, err)

	budgeted := first.Projections["budgeted"]
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionTruncated,
		Reason: contexty.ReasonTokenBudgetExceeded,
	}, budgeted.Transformations["drop"])

	derived := first.Projections["derived"]
	require.Len(t, derived.Messages, 1)
	require.NotEmpty(t, derived.Messages[0].ID)
	assert.Equal(t, derived.Messages[0].ID, second.Projections["derived"].Messages[0].ID)
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionFormatted,
		Reason: contexty.ReasonReplacedByFormatter,
	}, derived.Transformations["drop"])
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionFormatted,
		Reason: contexty.ReasonSegmentFormatter,
	}, derived.Transformations[derived.Messages[0].ID])
}

func TestDoD_Task16_BudgetSummaryIdentityIgnoresObserverState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	segments := make([]contexty.SegmentName, 0, 2)
	policy := contexty.MessageIdentityFunc(
		func(idCtx contexty.MessageIdentityContext, msg contexty.Message) (string, error) {
			segments = append(segments, idCtx.Segment)
			return fmt.Sprintf("id:%s:%d:%s", idCtx.Segment, idCtx.Index, msg.TextContent()), nil
		},
	)
	summarizer := task16Summarizer{summary: contexty.TextMessage(contexty.RoleAssistant, "summary")}
	req := contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "h1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "one"}},
			},
			{
				ID:    "h2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "two"}},
			},
		},
		IdentityPolicy:         policy,
		RequireDurableIdentity: true,
	}
	pipeWithoutObserver := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 1, Summarizer: summarizer},
		&contexty.FixedEstimator{TokensPerMessage: 1},
	)
	withoutObserver, err := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipeWithoutObserver),
	).CompileSnapshot(ctx, req)
	require.NoError(t, err)

	pipeWithObserver := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{TokenLimit: 1, Summarizer: summarizer},
		&contexty.FixedEstimator{TokensPerMessage: 1},
		contexty.WithBudgetObserver(task16Observer{}),
	)
	withObserver, err := contexty.NewEngine(
		contexty.WithBudgetPipeline(contexty.SegmentHistory, pipeWithObserver),
	).CompileSnapshot(ctx, req)
	require.NoError(t, err)

	require.Len(t, withoutObserver.Payload.History, 1)
	require.Len(t, withObserver.Payload.History, 1)
	assert.Equal(t, withoutObserver.Payload.History[0].ID, withObserver.Payload.History[0].ID)
	assert.Contains(t, segments, contexty.SegmentHistory)
}

func TestDoD_Task16_TargetBudgetSummaryTraceability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	result, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{
			{
				ID:    "h1",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "one"}},
			},
			{
				ID:    "h2",
				Role:  contexty.RoleUser,
				Parts: []contexty.ContentPart{contexty.TextPart{Text: "two"}},
			},
		},
		IdentityPolicy:         contexty.NewStableMessageIdentityPolicy("stable"),
		RequireDurableIdentity: true,
		Targets: []contexty.CompileTarget{
			{
				Name:          "summary_target",
				View:          "",
				SourceSegment: contexty.SegmentHistory,
				Budget: contexty.NewBudgetPipeline(
					contexty.BudgetConfig{
						TokenLimit: 1,
						Summarizer: task16Summarizer{summary: contexty.TextMessage(contexty.RoleAssistant, "summary")},
					},
					&contexty.FixedEstimator{TokensPerMessage: 1},
				),
				Formatter: nil,
			},
		},
	})
	require.NoError(t, err)

	projection := result.Projections["summary_target"]
	require.Len(t, projection.Messages, 1)
	summaryID := projection.Messages[0].ID
	require.NotEmpty(t, summaryID)
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionTruncated,
		Reason: contexty.ReasonTokenBudgetExceeded,
	}, projection.Transformations["h1"])
	assert.Equal(t, contexty.TransformRecord{
		Action: contexty.ActionTruncated,
		Reason: contexty.ReasonTokenBudgetExceeded,
	}, projection.Transformations["h2"])
	assert.Equal(
		t,
		contexty.TransformRecord{Action: contexty.ActionPassed, Reason: ""},
		projection.Transformations[summaryID],
	)
}

func TestDoD_Task16_TargetGeneratedIDsFailClosedWithoutPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		RequireDurableIdentity: true,
		Targets: []contexty.CompileTarget{
			{
				Name:          "derived",
				View:          "",
				SourceSegment: contexty.SegmentHistory,
				Budget:        nil,
				Formatter: func(_ context.Context, _ []contexty.Message) ([]contexty.Message, error) {
					return []contexty.Message{contexty.TextMessage(contexty.RoleUser, "derived")}, nil
				},
			},
		},
	})
	require.ErrorIs(t, err, contexty.ErrMissingIdentityPolicy)
}

func TestDoD_Task16_TargetFormatterDuplicateIDsFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		History: []contexty.Message{{
			ID:    "h1",
			Role:  contexty.RoleUser,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "history"}},
		}},
		Targets: []contexty.CompileTarget{
			{
				Name:          "duplicate_target",
				View:          "",
				SourceSegment: contexty.SegmentHistory,
				Budget:        nil,
				Formatter: func(_ context.Context, _ []contexty.Message) ([]contexty.Message, error) {
					return []contexty.Message{
						{
							ID:    "dup",
							Role:  contexty.RoleUser,
							Parts: []contexty.ContentPart{contexty.TextPart{Text: "one"}},
						},
						{
							ID:    "dup",
							Role:  contexty.RoleAssistant,
							Parts: []contexty.ContentPart{contexty.TextPart{Text: "two"}},
						},
					}, nil
				},
			},
		},
	})
	require.ErrorIs(t, err, contexty.ErrDuplicateMessageID)
}

func TestDoD_Task16_CompileTargetValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{
				Name:          "dup",
				View:          "",
				SourceSegment: "",
				Budget:        nil,
				Formatter:     nil,
			},
			{
				Name:          "dup",
				View:          "",
				SourceSegment: "",
				Budget:        nil,
				Formatter:     nil,
			},
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, contexty.ErrDuplicateCompileTarget)

	_, err = contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{
				Name:          "unknown_view",
				View:          "clasifier",
				SourceSegment: "",
				Budget:        nil,
				Formatter:     nil,
			},
		},
	})
	require.ErrorIs(t, err, contexty.ErrUnknownCompileTargetView)

	_, err = contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{
				Name:          "bad_segment",
				View:          "",
				SourceSegment: contexty.SegmentName("scratch"),
				Budget:        nil,
				Formatter:     nil,
			},
		},
	})
	require.ErrorIs(t, err, contexty.ErrInvalidCompileTargetSegment)

	_, err = contexty.NewEngine().CompileSnapshot(ctx, contexty.CompileRequest{
		Targets: []contexty.CompileTarget{
			{
				Name:          "view_conflict",
				View:          string(contexty.ViewLLMXML),
				SourceSegment: contexty.SegmentHistory,
				Budget:        nil,
				Formatter:     nil,
			},
		},
	})
	require.ErrorIs(t, err, contexty.ErrConflictingCompileTargetFields)
}

func task16MessageIDs(msgs []contexty.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, msg.ID)
	}
	return out
}

type task16TransformHook struct {
	fn func(context.Context, contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error)
}

type task16Summarizer struct {
	summary contexty.Message
}

func (s task16Summarizer) Summarize(context.Context, []contexty.Message) (contexty.Message, error) {
	return s.summary, nil
}

type task16Observer struct{}

func (task16Observer) OnTokensEstimated(context.Context, string, int) {}

func (task16Observer) OnNodeEvicted(context.Context, string, contexty.EvictionReason) {}

func (task16Observer) OnContextSummarized(context.Context, float64) {}

func (task16Observer) OnPipelineCompiled(context.Context, int, time.Duration) {}

func (h task16TransformHook) Transform(
	ctx context.Context,
	snap contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	return h.fn(ctx, snap)
}
