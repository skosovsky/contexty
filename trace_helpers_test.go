package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureTraceStageFixture() (contexty.CompileRequest, []contexty.EngineOption) {
	a := contexty.TextMessage(
		contexty.RoleUser,
		"a long document that must be summarized to fit the tiny history budget",
	)
	a.ID = "a"
	b := contexty.TextMessage(
		contexty.RoleAssistant,
		"another long document that must be summarized to fit the tiny history budget",
	)
	b.ID = "b"
	instruction := contexty.TextMessage(contexty.RoleSystem, "s")
	instruction.ID = "instruction"
	raw := contexty.TextMessage(contexty.RoleUser, "r")
	raw.ID = "turn"
	safe := raw.Clone()
	safe.Parts = []contexty.ContentPart{contexty.TextPart{Text: "p"}}
	turn := contexty.NewCurrentTurn(raw).WithPromptSafe(safe)
	request := contexty.CompileRequest{
		CompilationID: "stages",
		System:        []contexty.Message{instruction},
		History:       []contexty.Message{a, b},
		CurrentTurn:   &turn,
		Artifacts: []contexty.ContextArtifact{
			contexty.NewMemoryBlock("artifact", contexty.TextPayload("m")).ContextArtifact,
		},
		Options: []contexty.CompileOption{contexty.WithTextReplacement(contexty.TextReplacement{
			Segment: contexty.SegmentSystem, MessageID: "instruction", Text: "s",
		})},
		Targets: []contexty.CompileTarget{
			{Segments: []contexty.SegmentName{contexty.SegmentHistory}, Name: "structured"},
			{Name: "text", View: string(contexty.ViewLLMXML)},
		},
	}
	formatter := func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
		return messages, nil
	}
	options := []contexty.EngineOption{
		contexty.WithDeferredBlocks(contexty.DeferredBlock{Name: "resource", Segment: contexty.SegmentMemory,
			Resolve: func(context.Context) (contexty.DeferredResult, error) {
				message := contexty.TextMessage(contexty.RoleUser, "d")
				message.ID = "deferred"
				return contexty.DeferredResult{Messages: []contexty.Message{message}}, nil
			}}),
		contexty.WithTransformHooks(fixtureTextTransform{Replacer: func(text string) string { return text + "!" }}),
		contexty.WithRoleProjectionPolicy(contexty.RoleProjectionFunc(func(contexty.Message) (contexty.Role, error) {
			return contexty.RoleUser, nil
		})),
		contexty.WithSegmentFormatter(contexty.SegmentHistory, formatter),
		contexty.WithBudgetPipeline(contexty.NewBudgetPipeline(contexty.BudgetConfig{
			Budget: contexty.EffectiveInputBudget(20),
			Summarizer: stubSummarizer(func(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
				message := contexty.TextMessage(contexty.RoleAssistant, "s")
				message.ID = "summary"
				return message, nil
			}),
		}, contexty.CharTokenEstimator{})),
	}
	return request, options
}

func fixtureCodecBinding(kind contexty.CodecRegistryKind, typeID string) contexty.CodecBinding {
	return contexty.CodecBinding{Kind: kind, Type: typeID,
		Descriptor: contexty.Descriptor{ID: "host/" + string(kind) + "/" + typeID, Revision: "pinned"}}
}

func fixtureTraceProfile() contexty.TraceProfile {
	stages := make(map[string]contexty.Descriptor)
	for _, name := range []string{"source", "deferred", "merge", "hook", "role", "format", "summarize", "budget", "prompt", "patch", "project", "render", "prompt-template", "artifact"} {
		stages[name] = contexty.Descriptor{ID: name, Revision: "pinned"}
	}
	return contexty.TraceProfile{
		Encoding: contexty.Descriptor{ID: "json", Revision: "pinned"},
		Codec:    contexty.DefaultJSONSerializer(), Stages: stages,
	}
}

func fixtureRefForMessage(t *testing.T, msg contexty.Message) contexty.ContentRef {
	t.Helper()
	ref, err := contexty.MessageContentRef(msg, contexty.DefaultJSONSerializer())
	require.NoError(t, err)
	return ref
}
