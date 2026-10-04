package evaluation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestCorpusExpectationsReferToOriginalSources(t *testing.T) {
	// Arrange: task-owned answers are independent of strategy outputs.
	expected := map[string]map[string]string{
		"corpus.distracting-tools":     {"deployment.region": "ap-southeast-1"},
		"corpus.repeated-compaction":   {"recovery.key": "invoice-742"},
		"corpus.version-conflict":      {"deployment.region": "ap-southeast-1"},
		"corpus.retrieved-instruction": {"runbook.owner": "ops-team"},
		"corpus.protected-constraint":  {"disclosure.constraint": "no-private-export"},
		"corpus.partial-round":         {"lookup.a": "present"},
		"corpus.resource-access":       {"authorized.chunk": "runbook-17"},
		"corpus.blob-access":           {"authorized.blob": "trace-17"},
		"corpus.consumer-windows":      {"deployment.region": "ap-southeast-1", "incident.status": "mitigated"},
		"corpus.opaque-invalidation":   {"dependency.version": "v1"},
	}

	// Act.
	corpus := Corpus()

	// Assert: no answer is invented without an exact original event binding.
	require.Len(t, corpus, len(expected))
	seen := make(map[string]bool)
	for _, fixture := range corpus {
		require.NoError(t, fixture.Identity.Validate())
		require.False(t, seen[fixture.Identity.ID])
		seen[fixture.Identity.ID] = true
		require.Equal(t, expected[fixture.Identity.ID], fixture.ExpectedFacts)
		messages := make(map[string]contexty.Message)
		for _, message := range fixture.Messages {
			require.NotEmpty(t, message.ID)
			_, duplicate := messages[message.ID]
			require.False(t, duplicate)
			messages[message.ID] = message
		}
		for key, value := range fixture.ExpectedFacts {
			require.NotEmpty(t, fixture.FactVersions[key])
			require.NotEmpty(t, fixture.ExpectedSources[key])
			for _, ref := range fixture.ExpectedSources[key] {
				message, exists := messages[ref.ID]
				require.True(t, exists, "%s source %s", fixture.Identity.ID, ref.ID)
				require.Contains(t, message.SourceRefs, ref)
				require.Contains(t, strings.Split(originalText(message), "\n"), "FACT "+key+"="+value)
			}
		}
		for _, id := range append(append([]string(nil), fixture.RequiredIDs...), fixture.QuerySourceIDs...) {
			_, exists := messages[id]
			require.True(t, exists, "%s binding %s", fixture.Identity.ID, id)
		}
	}
}

func TestCorpusAdversarialStructure(t *testing.T) {
	// Arrange.
	fixtures := make(map[string]Fixture)
	for _, fixture := range Corpus() {
		fixtures[fixture.Identity.ID] = fixture
	}

	// Act.
	long := fixtures["corpus.distracting-tools"]
	conflict := fixtures["corpus.version-conflict"]
	partial := fixtures["corpus.partial-round"]
	instruction := fixtures["corpus.retrieved-instruction"]

	// Assert: these are genuine adversarial inputs, not named happy-path aliases.
	require.Greater(t, len(originalText(long.Messages[2])), 6000)
	require.True(t, long.Messages[1].HasToolCalls())
	_, resultPart := long.Messages[2].Parts[0].(contexty.ToolResultPart)
	require.True(t, resultPart)
	require.GreaterOrEqual(t, len(fixtures["corpus.repeated-compaction"].Messages), 20)
	require.Contains(t, conflict.Messages[0].TextContent(), "FACT deployment.region=eu-west-1")
	require.Equal(t, "v2", conflict.FactVersions["deployment.region"])
	require.Equal(t, "region-v2", conflict.ExpectedSources["deployment.region"][0].ID)
	require.Len(t, partial.Messages[0].Parts, 2)
	require.Len(t, partial.Messages[1].Parts, 1)
	require.Equal(t, "a", partial.Messages[1].Parts[0].(contexty.ToolResultPart).ToolCallID)
	require.Len(t, instruction.ForbiddenInstructions, 1)
	require.Contains(t, instruction.Messages[0].TextContent(), instruction.ForbiddenInstructions[0])
	require.Equal(t, []string{"constraint-v1"}, fixtures["corpus.protected-constraint"].RequiredIDs)
	require.Len(t, fixtures["corpus.consumer-windows"].ExpectedFacts, 2)
	for _, kind := range []string{"partial-round", "resource-access", "blob-access", "consumers", "opaque-invalidation"} {
		found := false
		for _, fixture := range fixtures {
			found = found || fixture.CaseKind == kind
		}
		require.True(t, found, "runner dispatch missing %s", kind)
	}
}

func TestCorpusOwnsEveryReturnedValue(t *testing.T) {
	// Arrange.
	first := Corpus()

	// Act: a strategy is allowed to mutate its copy, never another run's dataset.
	first[0].Messages[0].Parts[0] = contexty.TextPart{Text: "changed"}
	first[0].Messages[0].SourceRefs[0].ID = "changed"
	first[0].ExpectedFacts["deployment.region"] = "changed"
	first[0].ExpectedSources["deployment.region"][0].ID = "changed"
	first[0].FactVersions["deployment.region"] = "changed"
	first[0].QuerySourceIDs[0] = "changed"
	first[3].ForbiddenInstructions[0] = "changed"
	first[4].RequiredIDs[0] = "changed"
	second := Corpus()

	// Assert.
	require.Equal(t, "FACT deployment.region=ap-southeast-1", second[0].Messages[0].TextContent())
	require.Equal(t, "region-v1", second[0].Messages[0].SourceRefs[0].ID)
	require.Equal(t, "ap-southeast-1", second[0].ExpectedFacts["deployment.region"])
	require.Equal(t, "region-v1", second[0].ExpectedSources["deployment.region"][0].ID)
	require.Equal(t, "v1", second[0].FactVersions["deployment.region"])
	require.Equal(t, "region-v1", second[0].QuerySourceIDs[0])
	require.Contains(t, second[3].ForbiddenInstructions[0], "PRIVATE ARCHIVE")
	require.Equal(t, "constraint-v1", second[4].RequiredIDs[0])
}

func originalText(message contexty.Message) string {
	var result strings.Builder
	for _, part := range message.Parts {
		switch body := part.(type) {
		case contexty.TextPart:
			result.WriteString(body.Text)
		case contexty.ToolResultPart:
			result.WriteString(body.Payload.PlainText())
		}
	}
	return result.String()
}
