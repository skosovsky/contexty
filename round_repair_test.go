package contexty_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestInterrupted_Repair(t *testing.T) {
	// Arrange: partial round followed by an unrelated user message.
	messages := append(fixtureRoundMessages(), contexty.Message{ID: "next", Role: contexty.RoleUser,
		Parts: []contexty.ContentPart{contexty.TextPart{Text: "next request"}}})
	codec := contexty.DefaultJSONSerializer()
	before, err := json.Marshal(messages)
	require.NoError(t, err)
	declarations := map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundInterrupted}
	// Act: identical input and policy produce identical projection bytes and graph.
	projection, err := contexty.RepairInterruptedToolRounds(
		context.Background(),
		messages,
		declarations,
		fixtureRepairPolicy(),
		codec,
	)
	require.NoError(t, err)
	second, err := contexty.RepairInterruptedToolRounds(
		context.Background(),
		messages,
		declarations,
		fixtureRepairPolicy(),
		codec,
	)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, projection, second)
	require.Len(t, projection.Messages, 4)
	require.Equal(t, messages[2], projection.Messages[3])
	require.Len(t, projection.Repairs, 1)
	require.Equal(t, []string{"second"}, projection.Repairs[0].MissingCallIDs)
	require.NoError(t, projection.Lineage.Validate())
	require.Len(t, projection.Lineage.Records[0].Inputs, 2)
	require.Equal(t, "host-decision", projection.Lineage.Records[0].DecisionRef)
	require.Equal(t, projection.Repairs[0].Output, projection.Lineage.Records[0].Outputs[0])
	synthetic := projection.Messages[2]
	parts := synthetic.ToolResultParts()
	require.Len(t, parts, 1)
	require.Equal(t, "second", parts[0].ToolCallID)
	require.False(t, parts[0].IsError)
	require.Nil(t, parts[0].Payload.Error)
	require.Nil(t, parts[0].Payload.Control)
	require.Contains(t, parts[0].Payload.Text, "external outcome is unknown")
	require.Contains(t, string(parts[0].Payload.Data), `"kind":"interrupted_projection"`)
	wire, err := codec.Marshal(synthetic)
	require.NoError(t, err)
	var decoded contexty.Message
	require.NoError(t, codec.Unmarshal(wire, &decoded))
	require.Equal(t, synthetic, decoded)
	ref, err := contexty.MessageContentRef(decoded, codec)
	require.NoError(t, err)
	require.Equal(t, projection.Repairs[0].Output, ref)
	_, err = contexty.ToolRoundFromMessages(projection.Messages, 0)
	require.NoError(t, err)
	after, err := json.Marshal(messages)
	require.NoError(t, err)
	require.Equal(t, before, after)
	projection.Messages[0].Parts = nil
	projection.Repairs[0].MissingCallIDs[0] = "mutated"
	projection.Lineage.Records[0].Inputs[0].ID = "mutated"
	require.NotEmpty(t, messages[0].Parts)
	require.Equal(t, "second", second.Repairs[0].MissingCallIDs[0])
	require.Equal(t, "assistant", second.Lineage.Records[0].Inputs[0].ID)
}

func TestRepair_PreservesPending(t *testing.T) {
	// Arrange: no interrupted declaration, hence no authority to synthesize results.
	messages := fixtureRoundMessages()
	policy := fixtureRepairPolicy()
	policy.Decisions = nil
	// Act.
	projection, err := contexty.RepairInterruptedToolRounds(
		context.Background(),
		messages,
		nil,
		policy,
		contexty.DefaultJSONSerializer(),
	)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, messages, projection.Messages)
	require.Empty(t, projection.Repairs)
	require.Empty(t, projection.Lineage.Records)
	observations, err := contexty.InspectToolRoundStates(projection.Messages, nil)
	require.NoError(t, err)
	require.Equal(t, contexty.ToolRoundPending, observations[0].State)
}

func TestRepair_IdentityAndOrder(t *testing.T) {
	// Arrange: two missing calls, in original call order.
	messages := fixtureRoundMessages()[:1]
	decl := map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundInterrupted}
	codec := contexty.DefaultJSONSerializer()
	// Act.
	projection, err := contexty.RepairInterruptedToolRounds(
		context.Background(),
		messages,
		decl,
		fixtureRepairPolicy(),
		codec,
	)
	// Assert.
	require.NoError(t, err)
	parts := projection.Messages[1].ToolResultParts()
	require.Equal(t, "first", parts[0].ToolCallID)
	require.Equal(t, "second", parts[1].ToolCallID)
	policy := fixtureRepairPolicy()
	policy.Decisions["assistant"] = "another-decision"
	changed, err := contexty.RepairInterruptedToolRounds(context.Background(), messages, decl, policy, codec)
	require.NoError(t, err)
	require.NotEqual(t, projection.Repairs[0].Output, changed.Repairs[0].Output)
	policy = fixtureRepairPolicy()
	policy.Descriptor.Revision = "another-policy"
	changed, err = contexty.RepairInterruptedToolRounds(context.Background(), messages, decl, policy, codec)
	require.NoError(t, err)
	require.NotEqual(t, projection.Repairs[0].Output, changed.Repairs[0].Output)
}

func TestRepair_Failures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*contexty.InterruptedRoundRepairPolicy, *[]contexty.Message)
	}{
		{name: "missing descriptor", mutate: func(p *contexty.InterruptedRoundRepairPolicy, _ *[]contexty.Message) {
			p.Descriptor = contexty.Descriptor{}
		}},
		{name: "missing encoding", mutate: func(p *contexty.InterruptedRoundRepairPolicy, _ *[]contexty.Message) {
			p.Encoding = contexty.Descriptor{}
		}},
		{name: "missing decision", mutate: func(p *contexty.InterruptedRoundRepairPolicy, _ *[]contexty.Message) { p.Decisions = nil }},
		{name: "unknown decision", mutate: func(p *contexty.InterruptedRoundRepairPolicy, _ *[]contexty.Message) {
			p.Decisions["unknown"] = "decision"
		}},
		{name: "missing identity", mutate: func(_ *contexty.InterruptedRoundRepairPolicy, m *[]contexty.Message) { (*m)[1].ID = "" }},
		{name: "missing codec", mutate: func(_ *contexty.InterruptedRoundRepairPolicy, m *[]contexty.Message) {
			(*m)[0].Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"label":"untrusted"}`}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange.
			policy, messages := fixtureRepairPolicy(), fixtureRoundMessages()
			tc.mutate(&policy, &messages)
			// Act.
			projection, err := contexty.RepairInterruptedToolRounds(
				context.Background(),
				messages,
				map[string]contexty.ToolRoundState{
					"assistant": contexty.ToolRoundInterrupted,
				},
				policy,
				contexty.DefaultJSONSerializer(),
			)
			// Assert: atomic failure, without partial repaired history.
			require.Error(t, err)
			require.Equal(t, contexty.InterruptedRoundProjection{}, projection)
		})
	}
	// Arrange: generated ID collides with an existing unrelated message.
	messages := fixtureRoundMessages()
	decl := map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundInterrupted}
	projection, err := contexty.RepairInterruptedToolRounds(
		context.Background(),
		messages,
		decl,
		fixtureRepairPolicy(),
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	messages = append(messages, contexty.Message{ID: projection.Repairs[0].Output.ID, Role: contexty.RoleUser})
	// Act.
	failed, err := contexty.RepairInterruptedToolRounds(
		context.Background(),
		messages,
		decl,
		fixtureRepairPolicy(),
		contexty.DefaultJSONSerializer(),
	)
	// Assert.
	require.ErrorIs(t, err, contexty.ErrInvalidRoundRepair)
	require.Equal(t, contexty.InterruptedRoundProjection{}, failed)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failed, err = contexty.RepairInterruptedToolRounds(
		ctx,
		messages,
		decl,
		fixtureRepairPolicy(),
		contexty.DefaultJSONSerializer(),
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, contexty.InterruptedRoundProjection{}, failed)
}

func TestRepair_HostMetadataAndCancellation(t *testing.T) {
	// Arrange: metadata stays host-owned, including on a synthetic tool role.
	messages := fixtureRoundMessages()
	messages[0].Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"label":"untrusted"}`}}
	decl := map[string]contexty.ToolRoundState{"assistant": contexty.ToolRoundInterrupted}
	policy := fixtureRepairPolicy()
	codec := fixtureExtensionEstimateCodec()
	// Act.
	projection, err := contexty.RepairInterruptedToolRounds(context.Background(), messages, decl, policy, codec)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, messages[0].Extensions, projection.Messages[2].Extensions)
	require.Equal(t, "host-decision", projection.Repairs[0].DecisionRef)
	// Arrange: a codec callback mutates caller-owned policy state during validation.
	codec = contexty.DefaultJSONSerializer()
	codec.Extensions.Register("fixture-label", func(body []byte) (contexty.Extension, error) {
		policy.Decisions["assistant"] = "changed-by-callback"
		return fixtureWireExtension{wire: string(body)}, nil
	})
	// Act.
	projection, err = contexty.RepairInterruptedToolRounds(context.Background(), messages, decl, policy, codec)
	// Assert: policy was frozen before callbacks, not read again from host memory.
	require.NoError(t, err)
	require.Equal(t, "host-decision", projection.Repairs[0].DecisionRef)
	// Arrange: cancellation arrives from a decoder, after initial preflight.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	codec = contexty.DefaultJSONSerializer()
	codec.Extensions.Register("fixture-label", func(body []byte) (contexty.Extension, error) {
		cancel()
		return fixtureWireExtension{wire: string(body)}, nil
	})
	// Act.
	projection, err = contexty.RepairInterruptedToolRounds(ctx, messages, decl, fixtureRepairPolicy(), codec)
	// Assert.
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, contexty.InterruptedRoundProjection{}, projection)
}
