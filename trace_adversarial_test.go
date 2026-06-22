package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestTrace_MappingOccurrence(t *testing.T) {
	// Arrange: a mapping names an occurrence that was never available at this stage.
	profile := fixtureTraceProfile()
	profile.Mapping = func(_ context.Context, stage string, inputs, _ []contexty.Message) (map[string][]contexty.ContentRef, error) {
		if stage != "format" {
			return map[string][]contexty.ContentRef{}, nil
		}
		ref, err := contexty.MessageContentRef(inputs[0], profile.Codec)
		ref.Occurrence = "forged-unavailable-producer"
		return map[string][]contexty.ContentRef{"derived": {ref}}, err
	}
	engine := contexty.NewEngine(contexty.WithTraceProfile(profile),
		contexty.WithSegmentFormatter(contexty.SegmentHistory,
			func(_ context.Context, inputs []contexty.Message) ([]contexty.Message, error) {
				inputs[0].ID = "derived"
				return inputs, nil
			}))
	input := contexty.TextMessage(contexty.RoleUser, "original")
	input.ID = "input"
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
		CompilationID: "occurrence", History: []contexty.Message{input},
	})
	// Assert: exact occurrence refs cannot silently fall back to any matching bytes.
	require.ErrorIs(t, err, contexty.ErrMissingLineage)
	require.Zero(t, result)
}

func TestTrace_MappingRemovalCancellation(t *testing.T) {
	// Arrange: cancellation occurs in a mapping for an output-free removal stage.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	profile := fixtureTraceProfile()
	profile.Mapping = func(_ context.Context, stage string, _, _ []contexty.Message) (map[string][]contexty.ContentRef, error) {
		if stage == "format" {
			cancel()
		}
		return map[string][]contexty.ContentRef{}, nil
	}
	engine := contexty.NewEngine(contexty.WithTraceProfile(profile),
		contexty.WithSegmentFormatter(contexty.SegmentHistory,
			func(context.Context, []contexty.Message) ([]contexty.Message, error) {
				return nil, nil
			}))
	input := contexty.TextMessage(contexty.RoleUser, "removed")
	input.ID = "input"
	// Act.
	result, err := engine.CompileSnapshot(ctx, contexty.CompileRequest{
		CompilationID: "removal", History: []contexty.Message{input},
	})
	// Assert: no successful empty output after the caller cancels.
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
}

func TestTrace_StageDescriptors(t *testing.T) {
	// Arrange: exercise every intrinsic stage in one actual mixed-source pipeline.
	stages := []string{"source", "deferred", "merge", "hook", "role", "format", "summarize",
		"budget", "prompt", "patch", "project", "render", "prompt-template", "artifact"}
	request, options := fixtureTraceStageFixture()
	profile := fixtureTraceProfile()
	engine := contexty.NewEngine(append(options, contexty.WithTraceProfile(profile))...)
	// Act.
	result, err := engine.CompileSnapshot(context.Background(), request)
	// Assert: baseline actually reaches every stage; no vacuous failure cases.
	require.NoError(t, err)
	seen := make(map[string]bool)
	graphs := []contexty.Lineage{result.Lineage}
	for _, projection := range result.Projections {
		graphs = append(graphs, projection.Lineage)
	}
	for _, graph := range graphs {
		require.NoError(t, graph.Validate())
		for _, record := range graph.Records {
			seen[record.Stage] = true
		}
	}
	for _, stage := range stages {
		require.True(t, seen[stage], "baseline must reach %s", stage)
		t.Run(stage, func(t *testing.T) {
			// Arrange: remove one necessary descriptor, retaining all execution stages.
			broken := fixtureTraceProfile()
			delete(broken.Stages, stage)
			brokenEngine := contexty.NewEngine(append(options, contexty.WithTraceProfile(broken))...)
			// Act.
			partial, stageErr := brokenEngine.CompileSnapshot(context.Background(), request)
			// Assert: all main/target stages fail atomically without pinned identity.
			require.ErrorIs(t, stageErr, contexty.ErrInvalidDescriptor)
			require.Zero(t, partial)
		})
	}
}

func TestTrace_InvalidInputs(t *testing.T) {
	// Arrange: malformed graph metadata must fail before transform callbacks.
	a := fixtureRef(t, "a", "a")
	b := fixtureRef(t, "b", "b")
	c := fixtureRef(t, "c", "c")
	record := contexty.LineageRecord{ID: "prior", Transform: contexty.Descriptor{ID: "prior", Revision: "pinned"},
		Inputs: []contexty.ContentRef{a}, Outputs: []contexty.ContentRef{b}}
	cases := []struct {
		name   string
		change func(*contexty.TraceProfile, *contexty.CompileRequest)
		want   error
	}{
		{name: "duplicate-invocation", want: contexty.ErrDuplicateTransform,
			change: func(_ *contexty.TraceProfile, req *contexty.CompileRequest) {
				req.Lineage.Records = []contexty.LineageRecord{record, record}
			}},
		{name: "multiple-producers", want: contexty.ErrInvalidLineage,
			change: func(_ *contexty.TraceProfile, req *contexty.CompileRequest) {
				other := record
				other.ID = "other"
				other.Inputs = []contexty.ContentRef{c}
				req.Lineage.Records = []contexty.LineageRecord{record, other}
			}},
		{name: "cycle", want: contexty.ErrInvalidLineage,
			change: func(_ *contexty.TraceProfile, req *contexty.CompileRequest) {
				other := record
				other.ID = "reverse"
				other.Inputs, other.Outputs = []contexty.ContentRef{b}, []contexty.ContentRef{a}
				req.Lineage.Records = []contexty.LineageRecord{record, other}
			}},
		{name: "invalid-origin", want: contexty.ErrInvalidContentRef,
			change: func(_ *contexty.TraceProfile, req *contexty.CompileRequest) {
				req.Origins = []contexty.ContentRef{{ID: "input", Digest: "not-a-digest"}}
			}},
		{name: "wrong-origin-revision", want: contexty.ErrMissingLineage,
			change: func(profile *contexty.TraceProfile, req *contexty.CompileRequest) {
				profile.RequireOrigins = true
				wrong := a
				wrong.ID = "input"
				req.Origins = []contexty.ContentRef{wrong}
			}},
		{name: "required-codec", want: contexty.ErrMissingLabelCodec,
			change: func(profile *contexty.TraceProfile, _ *contexty.CompileRequest) {
				profile.Labels.RequiredTypes = []string{"host-required"}
			}},
		{name: "unknown-input-codec", want: contexty.ErrMissingLabelCodec,
			change: func(profile *contexty.TraceProfile, req *contexty.CompileRequest) {
				profile.Labels.Registry = contexty.NewExtensionRegistry()
				req.History[0].Extensions = []contexty.Extension{fixtureWireExtension{wire: `{}`}}
			}},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange.
			profile := fixtureTraceProfile()
			input := contexty.TextMessage(contexty.RoleUser, "input")
			input.ID = "input"
			req := contexty.CompileRequest{CompilationID: "invalid-input", History: []contexty.Message{input}}
			scenario.change(&profile, &req)
			callbacks := 0
			engine := contexty.NewEngine(contexty.WithTraceProfile(profile), contexty.WithTransformHooks(
				contexty.RedactionHook{Replacer: func(text string) string { callbacks++; return text }},
			))
			// Act.
			result, err := engine.CompileSnapshot(context.Background(), req)
			// Assert: invalid metadata cannot become a partial successful compile.
			require.ErrorIs(t, err, scenario.want)
			require.Zero(t, result)
			require.Zero(t, callbacks)
		})
	}
}

func TestTrace_GeneratedRoot(t *testing.T) {
	for _, strict := range []bool{true, false} {
		t.Run(map[bool]string{true: "strict", false: "permissive"}[strict], func(t *testing.T) {
			// Arrange: a hook produces content from an empty snapshot without declared origin.
			profile := fixtureTraceProfile()
			profile.RequireOrigins = strict
			generated := contexty.TextMessage(contexty.RoleSystem, "host-generated")
			generated.ID = "generated"
			engine := contexty.NewEngine(contexty.WithTraceProfile(profile), contexty.WithTransformHooks(
				fixtureTransformHook{
					fn: func(_ context.Context, snapshot contexty.ConversationSnapshot) (contexty.ConversationSnapshot, error) {
						return snapshot.WithSegment(contexty.SegmentSystem, []contexty.Message{generated}), nil
					},
				},
			))
			// Act.
			result, err := engine.CompileSnapshot(
				context.Background(),
				contexty.CompileRequest{CompilationID: "generated"},
			)
			// Assert: empty input is not permission to fabricate fully resolved provenance.
			if strict {
				require.ErrorIs(t, err, contexty.ErrMissingLineage)
				require.Zero(t, result)
				return
			}
			require.NoError(t, err)
			ref, refErr := contexty.MessageContentRef(generated, profile.Codec)
			require.NoError(t, refErr)
			require.Contains(t, result.Lineage.Unresolved, ref)
			require.Equal(t, []contexty.ContentRef{ref}, result.Lineage.Records[0].Inputs)
		})
	}
}

func TestTrace_TargetStageFailures(t *testing.T) {
	for _, stage := range []string{"format", "budget", "project", "render"} {
		t.Run(stage, func(t *testing.T) {
			// Arrange: these stages run only in targets, not in the shared/main pass.
			profile := fixtureTraceProfile()
			delete(profile.Stages, stage)
			input := contexty.TextMessage(contexty.RoleUser, "x")
			input.ID = "input"
			target := contexty.CompileTarget{Name: "branch", SourceSegment: contexty.SegmentHistory,
				Budget: contexty.NewBudgetPipeline(contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(10)},
					contexty.CharTokenEstimator{}),
				Formatter: func(_ context.Context, messages []contexty.Message) ([]contexty.Message, error) {
					return messages, nil
				}}
			if stage == "render" {
				target = contexty.CompileTarget{Name: "branch", View: string(contexty.ViewLLMXML)}
			}
			engine := contexty.NewEngine(contexty.WithTraceProfile(profile))
			// Act.
			result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
				CompilationID: "target-stages",
				History:       []contexty.Message{input},
				Targets:       []contexty.CompileTarget{target},
			})
			// Assert: a failed branch cannot expose a partially successful main result.
			require.ErrorIs(t, err, contexty.ErrInvalidDescriptor)
			require.Zero(t, result)
		})
	}
}
