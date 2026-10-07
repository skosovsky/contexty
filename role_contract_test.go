package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestAcceptance_DeveloperIdentityPolicy(t *testing.T) {
	// Arrange.
	message := contexty.TextMessage(contexty.Role("developer"), "host instructions")
	message.ID = "instructions"
	policy := contexty.OutputPolicy{Identity: contexty.Descriptor{ID: "identity", Revision: "fixed"},
		Project: func(_ context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
			return input.Payload, nil
		}}
	for _, options := range [][]contexty.EngineOption{nil, {contexty.WithOutputPolicy(policy)}} {
		// Act.
		result, err := contexty.NewEngine(options...).
			CompileSnapshot(context.Background(), contexty.CompileRequest{System: []contexty.Message{message}})
		// Assert.
		require.NoError(t, err)
		require.Equal(t, message.Role, result.Payload.System[0].Role)
	}
}

func TestAcceptance_RoleBoundaries(t *testing.T) {
	for _, role := range []contexty.Role{contexty.RoleSystem, contexty.RoleDeveloper, contexty.RoleUser, contexty.RoleAssistant, contexty.RoleTool} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			message := contexty.TextMessage(role, "content")
			message.ID = "message"
			policy := contexty.OutputPolicy{Identity: contexty.Descriptor{ID: "identity", Revision: "fixed"},
				Project: func(_ context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
					return input.Payload, nil
				}}
			history := []contexty.Message{message}
			if role == contexty.RoleTool {
				message.Parts = []contexty.ContentPart{
					contexty.ToolResultPart{ToolCallID: "call", Name: "tool", Payload: contexty.TextPayload("content")},
				}
				history = []contexty.Message{
					{
						ID:   "caller",
						Role: contexty.RoleAssistant,
						Parts: []contexty.ContentPart{
							contexty.ToolCallPart{ID: "call", Name: "tool", Arguments: contexty.JSONPayload("{}")},
						},
					},
					message,
				}
			}
			engine := contexty.NewEngine(
				contexty.WithOutputPolicy(policy),
				contexty.WithTraceProfile(fixtureTraceProfile()),
				contexty.WithCompileRecording(fixtureRecordProfile("native")),
				contexty.WithCompileContentCapture(
					contexty.Descriptor{ID: "capture", Revision: "fixed"}, fixtureContentPolicy(fixtureAllowContent)),
			)
			// Act.
			compiled, err := engine.CompileSnapshot(
				context.Background(),
				contexty.CompileRequest{
					CompilationID: "roles",
					History:       history,
					Targets: []contexty.CompileTarget{
						{Name: "native", Segments: []contexty.SegmentName{contexty.SegmentHistory}},
					},
				},
			)
			// Assert.
			require.NoError(t, err)
			require.Equal(t, role, compiled.Payload.History[len(history)-1].Role)
			projection, found := compiled.Projections["native"]
			require.True(t, found)
			require.Equal(t, role, projection.Messages[len(history)-1].Role)
			wire, err := (contexty.ConversationCodec{}).Encode(
				contexty.EmptySnapshot().WithSegment(contexty.SegmentHistory, []contexty.Message{message}),
			)
			require.NoError(t, err)
			restored, err := (contexty.ConversationCodec{}).Decode(wire)
			require.NoError(t, err)
			require.Equal(t, role, restored.Segment(contexty.SegmentHistory)[0].Role)
			envelope, err := contexty.ExportProjection(
				projection,
				contexty.ExportSelection{MessageIDs: []string{"message"}},
				contexty.DefaultJSONSerializer(),
			)
			require.NoError(t, err)
			var exported contexty.Message
			require.NoError(t, contexty.DefaultJSONSerializer().Unmarshal(envelope.Messages[0], &exported))
			require.Equal(t, role, exported.Role)
			accepted, err := compiled.Record.Accept("host")
			require.NoError(t, err)
			expected, err := contexty.ReplayExpectationFor(*compiled.Manifest)
			require.NoError(t, err)
			replayed, err := contexty.Replay(context.Background(), accepted, expected, contexty.DefaultJSONSerializer())
			require.NoError(t, err)
			require.Equal(t, role, replayed.Outputs[0].Segments["history"][len(history)-1].Role)
		})
	}
}

func TestAcceptance_UnknownRoleFailsClosed(t *testing.T) {
	for _, role := range []contexty.Role{"", "provider-custom"} {
		t.Run(string(role), func(t *testing.T) {
			// Arrange.
			message := contexty.TextMessage(role, "invalid")
			message.ID = "invalid"
			calls := 0
			policy := contexty.OutputPolicy{Identity: contexty.Descriptor{ID: "policy", Revision: "fixed"},
				Project: func(_ context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
					calls++
					return input.Payload, nil
				}}
			// Act / Assert.
			for _, options := range [][]contexty.EngineOption{nil, {contexty.WithOutputPolicy(policy)}} {
				result, err := contexty.NewEngine(options...).
					CompileSnapshot(context.Background(), contexty.CompileRequest{History: []contexty.Message{message}})
				require.ErrorIs(t, err, contexty.ErrInvalidRole)
				require.Zero(t, result)
			}
			require.Zero(t, calls)
			codec := contexty.DefaultJSONSerializer()
			_, err := codec.Marshal(message)
			require.ErrorIs(t, err, contexty.ErrInvalidRole)
			var restored contexty.Message
			err = codec.Unmarshal([]byte(`{"role":"`+string(role)+`","parts":[]}`), &restored)
			require.ErrorIs(t, err, contexty.ErrInvalidRole)
			require.Zero(t, restored)
			projection := contexty.CompileProjection{Messages: []contexty.Message{message}}
			_, err = contexty.ExportProjection(
				projection,
				contexty.ExportSelection{MessageIDs: []string{message.ID}},
				codec,
			)
			require.ErrorIs(t, err, contexty.ErrInvalidRole)
			store := contexty.NewMemoryConversationStateStore()
			err = store.CommitState(
				context.Background(),
				"invalid",
				0,
				contexty.ConversationDelta{
					Operation: contexty.DeltaAppendMessages,
					Segment:   contexty.SegmentHistory,
					Messages:  []contexty.Message{message},
				},
			)
			require.ErrorIs(t, err, contexty.ErrInvalidRole)
			state, err := store.LoadState(context.Background(), "invalid")
			require.NoError(t, err)
			require.Zero(t, state.Version())
			require.Empty(t, state.Segment(contexty.SegmentHistory))
		})
	}
}

func TestAcceptance_DeveloperMaterializationAndRetention(t *testing.T) {
	// Arrange.
	artifact := contexty.NewMemoryBlock("rules", contexty.TextPayload("rules")).ContextArtifact
	engine := contexty.NewEngine(contexty.WithArtifactMaterialization(contexty.ArtifactMaterializationPolicy{
		Identity: contexty.Descriptor{ID: "host", Revision: "fixed"},
		Materialize: func(_ context.Context, input contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
			parts, err := contexty.ArtifactContentParts(input)
			return contexty.ArtifactRepresentation{Role: contexty.RoleDeveloper, Parts: parts}, err
		},
	}))
	// Act.
	result, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{Artifacts: []contexty.ContextArtifact{artifact}},
	)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, contexty.RoleDeveloper, result.Payload.Memory[0].Role)
	// Arrange.
	messages := []contexty.Message{{ID: "rule", Role: contexty.RoleDeveloper}, {ID: "old", Role: contexty.RoleUser}}
	pipeline := contexty.NewBudgetPipeline(
		contexty.BudgetConfig{
			Budget:           contexty.EffectiveInputBudget(5),
			Retention:        contexty.RetentionPolicy{Roles: []contexty.Role{contexty.RoleDeveloper}},
			TruncateStrategy: contexty.NewDropStrategy(),
		},
		&contexty.FixedEstimator{TokensPerMessage: 5},
	)
	// Act.
	budgeted, err := pipeline.Apply(context.Background(), messages)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, []contexty.Message{messages[0]}, budgeted.Messages)
}

func TestAcceptance_TransformedUnknownRoleRejected(t *testing.T) {
	// Arrange.
	message := contexty.TextMessage(contexty.RoleDeveloper, "valid")
	message.ID = "valid"
	policy := contexty.OutputPolicy{
		Identity: contexty.Descriptor{ID: "host", Revision: "fixed"},
		Project: func(_ context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
			input.Payload.History[0].Role = "alien"
			return input.Payload, nil
		},
	}
	engine := contexty.NewEngine(contexty.WithOutputPolicy(policy))
	// Act.
	result, err := engine.CompileSnapshot(
		context.Background(),
		contexty.CompileRequest{History: []contexty.Message{message}},
	)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrInvalidRole)
	require.Zero(t, result)
}
