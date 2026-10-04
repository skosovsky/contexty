package contexty_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/skosovsky/contexty"
)

type testExtension struct {
	Tenant string  `json:"tenant"`
	Score  float64 `json:"score"`
}

func (e testExtension) ExtensionType() string { return "test_extension" }

func (e testExtension) CloneExtension() contexty.Extension { return e }

func newTestExtensionRegistry() *contexty.ExtensionRegistry {
	reg := contexty.NewExtensionRegistry()
	reg.Register("test_extension", func(data []byte) (contexty.Extension, error) {
		var ext testExtension
		err := json.Unmarshal(data, &ext)
		return ext, err
	})
	return reg
}

func cloneMsgs(in []contexty.Message) []contexty.Message {
	out := make([]contexty.Message, len(in))
	for i := range in {
		out[i] = in[i].Clone()
	}
	return out
}

func messageIDsFromSlice(msgs []contexty.Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

func findTestMessageByID(msgs []contexty.Message, id string) *contexty.Message {
	for i := range msgs {
		if msgs[i].ID == id {
			return &msgs[i]
		}
	}
	return nil
}

func cloneTestMsgs(in []contexty.Message) []contexty.Message {
	out := make([]contexty.Message, len(in))
	for i, m := range in {
		out[i] = m.Clone()
	}
	return out
}

type replaceHistoryHook struct{}

func (replaceHistoryHook) Transform(
	_ context.Context,
	snap contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	return snap.WithSegment(contexty.SegmentHistory, []contexty.Message{{
		ID:    "hook-new",
		Role:  contexty.RoleAssistant,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "replacement"}},
	}}), nil
}

type partialReplaceHistoryHook struct{}

func (partialReplaceHistoryHook) Transform(
	_ context.Context,
	snap contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	history := snap.Segment(contexty.SegmentHistory)
	if len(history) == 0 {
		return snap, nil
	}
	return snap.WithSegment(contexty.SegmentHistory, []contexty.Message{
		history[0],
		{
			ID:    "hook-new",
			Role:  contexty.RoleAssistant,
			Parts: []contexty.ContentPart{contexty.TextPart{Text: "inserted"}},
		},
	}), nil
}

type reintroduceSystemMessageHook struct {
	id   string
	role contexty.Role
	text string
}

func (h reintroduceSystemMessageHook) Transform(
	_ context.Context,
	snap contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	system := append([]contexty.Message{}, snap.Segment(contexty.SegmentSystem)...)
	system = append(system, contexty.Message{
		ID:    h.id,
		Role:  h.role,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: h.text}},
	})
	return snap.WithSegment(contexty.SegmentSystem, system), nil
}

type formatterContextKey struct{}

func artifactIDs(artifacts []contexty.ContextArtifact) []string {
	ids := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		ids = append(ids, artifact.ID)
	}
	return ids
}

func flattenText(msgs []contexty.Message) string {
	var b strings.Builder
	for _, msg := range msgs {
		b.WriteString(msg.TextContent())
		b.WriteString("\n")
	}
	return b.String()
}

func messageIDs(msgs []contexty.Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		ids = append(ids, msg.ID)
	}
	return ids
}

func fixtureMessageIDs(msgs []contexty.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, msg.ID)
	}
	return out
}

type fixtureTransformHook struct {
	fn func(context.Context, contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error)
}

type fixtureSummarizer struct {
	summary contexty.Message
}

func (s fixtureSummarizer) Summarize(context.Context, contexty.SummaryRequest) (contexty.Message, error) {
	return s.summary, nil
}

type fixtureObserver struct{}

func (fixtureObserver) OnTokensEstimated(context.Context, string, int) {}

func (fixtureObserver) OnNodeEvicted(context.Context, string, contexty.EvictionReason) {}

func (fixtureObserver) OnContextSummarized(context.Context, float64) {}

func (fixtureObserver) OnPipelineCompiled(context.Context, int, time.Duration) {}

func (h fixtureTransformHook) Transform(
	ctx context.Context,
	snap contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	return h.fn(ctx, snap)
}
