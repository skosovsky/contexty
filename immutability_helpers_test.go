package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureOwnedCompile(t *testing.T) (contexty.CompileRequest, contexty.CompileResult, contexty.SavedCompileRecord) {
	t.Helper()
	input := fixtureRollingText("input", "safe")
	input.SourceRefs = []contexty.SourceRef{{ID: "source"}}
	request := contexty.CompileRequest{
		CompilationID: "owned",
		History:       []contexty.Message{input},
		Artifacts: []contexty.ContextArtifact{
			contexty.NewMemoryBlock("memory", contexty.TextPayload("memo")).ContextArtifact,
		},
		Targets: []contexty.CompileTarget{{Name: "first", Segments: []contexty.SegmentName{contexty.SegmentHistory}},
			{Name: "second", Segments: []contexty.SegmentName{contexty.SegmentHistory}}},
	}
	engine := fixtureEngine(
		contexty.WithTraceProfile(fixtureTraceProfile()),
		contexty.WithCompileRecording(fixtureRecordProfile("first", "second")),
		contexty.WithCompileContentCapture(
			contexty.Descriptor{ID: "privacy", Revision: "pinned"},
			fixtureContentPolicy(fixtureAllowContent),
		),
	)
	result, err := engine.CompileSnapshot(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result.Record)
	accepted, err := result.Record.Accept("host-accept")
	require.NoError(t, err)
	return request, result, accepted
}

func fixtureOwnedChannels(t *testing.T, request contexty.CompileRequest, result contexty.CompileResult,
	accepted contexty.SavedCompileRecord,
) map[string]string {
	t.Helper()
	values := map[string]any{
		"request":   []any{request.History, request.Artifacts},
		"main":      result.Payload,
		"source":    []any{result.Source.History, result.Source.Artifacts},
		"artifacts": result.Artifacts,
		"manifest":  result.Manifest,
		"record":    result.Record,
		"accepted":  accepted,
		"snapshot":  []any{result.NormalizedSnapshot.AllSegments(), result.NormalizedSnapshot.Artifacts()},
	}
	for _, name := range []string{"first", "second"} {
		projection := result.Projections[name]
		values[name] = []any{
			projection.Messages,
			projection.Source.History,
			projection.Source.Artifacts,
			projection.Transformations,
			projection.Lineage,
			projection.InputSnapshot.AllSegments(),
			projection.InputSnapshot.Artifacts(),
		}
	}
	encoded := make(map[string]string, len(values))
	for name, value := range values {
		wire, err := json.Marshal(value)
		require.NoError(t, err)
		encoded[name] = string(wire)
	}
	return encoded
}

func fixtureMutateChannel(channel string, request *contexty.CompileRequest, result *contexty.CompileResult,
	accepted *contexty.SavedCompileRecord,
) {
	switch channel {
	case "request":
		fixtureMutateMessage(request.History[0])
		request.Artifacts[0].Payload.Text = "mutated"
	case "main":
		fixtureMutateMessage(result.Payload.History[0])
	case "source":
		fixtureMutateMessage(result.Source.History[0])
		result.Source.Artifacts[0].Payload.Text = "mutated"
	case "first":
		projection := result.Projections[channel]
		fixtureMutateMessage(projection.Messages[0])
		fixtureMutateMessage(projection.Source.History[0])
		projection.Source.Artifacts[0].Payload.Text = "mutated"
		projection.Lineage.Records[0].Inputs[0].ID = "mutated"
		for id, chain := range projection.Transformations {
			chain[0].Reason = "mutated"
			projection.Transformations[id] = chain
		}
	case "artifacts":
		result.Artifacts[0].Payload.Text = "mutated"
	case "manifest":
		result.Manifest.Artifacts[0].ID = "mutated"
		result.Manifest.Stages["source"] = contexty.Descriptor{ID: "mutated", Revision: "mutated"}
	case "record":
		fixtureMutateSavedWire(result.Record.Content[0].Wire)
		result.Record.Manifest.Artifacts[0].ID = "mutated"
	case "accepted":
		fixtureMutateSavedWire(accepted.Content[0].Wire)
		accepted.Manifest.Artifacts[0].ID = "mutated"
	case "snapshot":
		fixtureMutateMessage(result.NormalizedSnapshot.Segment(contexty.SegmentHistory)[0])
		result.NormalizedSnapshot.Artifacts()[0].Payload.Text = "mutated"
	}
}

func fixtureMutateMessage(message contexty.Message) {
	message.Parts[0] = contexty.TextPart{Text: "mutated"}
	message.SourceRefs[0].ID = "mutated"
}

func fixtureMutateSavedWire(wire []byte) {
	for i, value := range wire {
		if value >= 'a' && value <= 'w' {
			wire[i] = 'x'
			return
		}
	}
}
