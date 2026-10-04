package evaluation

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/skosovsky/contexty"
)

type factValue struct {
	value   string
	sources []contexty.SourceRef
}

type countedSummarizer struct {
	inner     contexty.Summarizer
	callbacks *Callbacks
}

func (s countedSummarizer) Summarize(ctx context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
	s.callbacks.Summaries++
	if s.inner != nil {
		return s.inner.Summarize(ctx, request)
	}
	facts := extractFacts(request.Messages)
	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var lines []string
	var sources []contexty.SourceRef
	for _, key := range keys {
		lines = append(lines, "FACT "+key+"="+facts[key].value)
		for _, source := range facts[key].sources {
			if !slices.Contains(sources, source) {
				sources = append(sources, source)
			}
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "Fixture distractors omitted.")
	}
	message := contexty.TextMessage(contexty.RoleUser, strings.Join(lines, "\n"))
	message.ID = fmt.Sprintf("fixture-summary-%d", s.callbacks.Summaries)
	message.SourceRefs = sources
	return message, nil
}

func messageText(message contexty.Message) string {
	var texts []string
	for _, part := range message.Parts {
		switch value := part.(type) {
		case contexty.TextPart:
			texts = append(texts, value.Text)
		case contexty.ToolResultPart:
			texts = append(texts, value.Payload.PlainText())
		default: // Calls and other parts are not interpreted as fact assertions.
		}
	}
	return strings.Join(texts, "\n")
}

func extractFacts(messages []contexty.Message) map[string]factValue {
	facts := make(map[string]factValue)
	for _, message := range messages {
		for line := range strings.SplitSeq(messageText(message), "\n") {
			if !strings.HasPrefix(line, "FACT ") {
				continue
			}
			key, value, ok := strings.Cut(strings.TrimPrefix(line, "FACT "), "=")
			if ok && key != "" {
				facts[key] = factValue{value: value, sources: slices.Clone(message.SourceRefs)}
			}
		}
	}
	return facts
}

func evaluateFixture(fixture Fixture, messages []contexty.Message) []Check {
	facts := extractFacts(messages)
	var checks []Check
	keys := make([]string, 0, len(fixture.ExpectedFacts))
	for key := range fixture.ExpectedFacts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		actual, exists := facts[key]
		sourcesOK := exists
		for _, source := range fixture.ExpectedSources[key] {
			sourcesOK = sourcesOK && slices.Contains(actual.sources, source)
		}
		checks = append(checks, Check{
			Kind:   "fact:" + key,
			Passed: exists && actual.value == fixture.ExpectedFacts[key] && sourcesOK,
			Detail: "Expected value=" + fixture.ExpectedFacts[key] + "; version=" + fixture.FactVersions[key] + "; exact source attribution checked",
		})
	}
	for _, id := range fixture.RequiredIDs {
		original := findMessage(fixture.Messages, id)
		actual := findMessage(messages, id)
		before, errBefore := contexty.MessageContentRef(original, contexty.DefaultJSONSerializer())
		after, errAfter := contexty.MessageContentRef(actual, contexty.DefaultJSONSerializer())
		checks = append(
			checks,
			Check{Kind: "required:" + id, Passed: errBefore == nil && errAfter == nil && before == after,
				Detail: "Protected original content reference must remain exact"},
		)
	}
	for _, instruction := range fixture.ForbiddenInstructions {
		dataRole := true
		for _, message := range messages {
			if strings.Contains(messageText(message), instruction) && message.Role == contexty.RoleSystem {
				dataRole = false
			}
		}
		checks = append(checks, Check{Kind: "retrieved-instruction-data", Passed: dataRole,
			Detail: "Retrieved instruction has no system-role promotion; LLM obedience is not measured"})
	}
	return checks
}

func findMessage(messages []contexty.Message, id string) contexty.Message {
	for _, message := range messages {
		if message.ID == id {
			return message
		}
	}
	return contexty.Message{}
}
