package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func outputPolicyFixture() AbstractPayload {
	return AbstractPayload{
		System: []Message{{ID: "instructions", Role: RoleSystem, Parts: []ContentPart{TextPart{Text: "secret"}}}},
		History: []Message{
			{
				ID:   "call",
				Role: RoleAssistant,
				Parts: []ContentPart{
					ToolCallPart{
						ID:        "lookup",
						Name:      "search",
						Arguments: ToolPayload{Data: json.RawMessage(`{"secret":"argument"}`)},
					},
				},
			},
			{
				ID:   "result",
				Role: RoleTool,
				Parts: []ContentPart{
					ToolResultPart{
						ToolCallID: "lookup",
						Name:       "search",
						Payload:    ToolPayload{Data: json.RawMessage(`{"secret":"result"}`)},
					},
				},
			},
			{ID: "pending", Role: RoleUser, Parts: []ContentPart{TextPart{Text: "secret"}}},
		},
		Tools:  []Message{{ID: "tool-context", Role: RoleUser, Parts: []ContentPart{TextPart{Text: "secret"}}}},
		Memory: []Message{{ID: "artifact:retrieval", Role: RoleUser, Parts: []ContentPart{TextPart{Text: "secret"}}}},
	}
}

func TestOutputPolicyOwnedTypedProjection(t *testing.T) {
	t.Parallel()
	// Arrange.
	before := outputPolicyFixture()
	var returned AbstractPayload
	policy := OutputPolicy{Identity: Descriptor{ID: "redactor", Revision: "1"},
		Project: func(_ context.Context, input OutputPolicyInput) (AbstractPayload, error) {
			require.Equal(t, ManifestMainOutput, input.Kind)
			require.Equal(t, "main", input.Name)
			for _, messages := range outputPolicySegments(input.Payload) {
				for i := range messages {
					for j, part := range messages[i].Parts {
						switch value := part.(type) {
						case TextPart:
							messages[i].Parts[j] = TextPart{Text: "redacted"}
						case ToolCallPart:
							value.Arguments.Data = json.RawMessage(`{"safe":true}`)
							messages[i].Parts[j] = value
						case ToolResultPart:
							value.Payload.Data = json.RawMessage(`{"safe":true}`)
							messages[i].Parts[j] = value
						}
					}
				}
			}
			returned = input.Payload
			return returned, nil
		}}
	// Act.
	accepted, err := applyOutputPolicy(
		t.Context(),
		&policy,
		OutputPolicyInput{Kind: ManifestMainOutput, Name: "main", Payload: before},
	)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, outputPolicyFixture(), before)
	require.Equal(t, "redacted", accepted.History[2].TextContent())
	require.JSONEq(t, `{"safe":true}`, string(accepted.History[0].ToolCallParts()[0].Arguments.Data))
	require.JSONEq(t, `{"safe":true}`, string(accepted.History[1].ToolResultParts()[0].Payload.Data))
	returned.Memory[0].Parts[0] = TextPart{Text: "changed later"}
	require.Equal(t, "redacted", accepted.Memory[0].TextContent())
}

func TestOutputPolicyRejectsStructuralChanges(t *testing.T) {
	t.Parallel()
	scenarios := []struct {
		name   string
		mutate func(*AbstractPayload)
	}{
		{name: "remove pending", mutate: func(p *AbstractPayload) { p.History = p.History[:2] }},
		{name: "reorder", mutate: func(p *AbstractPayload) { p.History[0], p.History[1] = p.History[1], p.History[0] }},
		{
			name:   "move segment",
			mutate: func(p *AbstractPayload) { p.Tools = append(p.Tools, p.Memory[0]); p.Memory = nil },
		},
		{name: "change identity", mutate: func(p *AbstractPayload) { p.Memory[0].ID = "foreign" }},
		{name: "unknown role", mutate: func(p *AbstractPayload) { p.Memory[0].Role = "developer" }},
		{
			name:   "remove call",
			mutate: func(p *AbstractPayload) { p.History[0].Parts = []ContentPart{TextPart{Text: "hidden"}} },
		},
		{
			name:   "duplicate call",
			mutate: func(p *AbstractPayload) { p.History[0].Parts = append(p.History[0].Parts, p.History[0].Parts[0]) },
		},
		{name: "rename call", mutate: func(p *AbstractPayload) {
			v := p.History[0].ToolCallParts()[0]
			v.Name = "execute"
			p.History[0].Parts[0] = v
		}},
		{name: "change result binding", mutate: func(p *AbstractPayload) {
			v := p.History[1].ToolResultParts()[0]
			v.ToolCallID = "foreign"
			p.History[1].Parts[0] = v
		}},
		{name: "change result status", mutate: func(p *AbstractPayload) {
			v := p.History[1].ToolResultParts()[0]
			v.IsError = !v.IsError
			p.History[1].Parts[0] = v
		}},
		{name: "change tool role", mutate: func(p *AbstractPayload) { p.History[1].Role = RoleUser }},
		{name: "move tool part", mutate: func(p *AbstractPayload) {
			p.History[0].Parts = append([]ContentPart{TextPart{Text: "prefix"}}, p.History[0].Parts...)
		}},
		{name: "nil part", mutate: func(p *AbstractPayload) { p.Tools[0].Parts[0] = nil }},
		{name: "invalid json", mutate: func(p *AbstractPayload) {
			v := p.History[0].ToolCallParts()[0]
			v.Arguments.Data = json.RawMessage(`{`)
			p.History[0].Parts[0] = v
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			// Arrange.
			policy := OutputPolicy{
				Identity: Descriptor{ID: "policy", Revision: "1"},
				Project: func(_ context.Context, input OutputPolicyInput) (AbstractPayload, error) {
					scenario.mutate(&input.Payload)
					return input.Payload, nil
				},
			}
			// Act.
			accepted, err := applyOutputPolicy(
				t.Context(),
				&policy,
				OutputPolicyInput{Kind: ManifestMainOutput, Name: "main", Payload: outputPolicyFixture()},
			)
			// Assert.
			require.ErrorIs(t, err, ErrInvalidOutputPolicy)
			require.Equal(t, AbstractPayload{}, accepted)
		})
	}
}

func TestOutputPolicyConfigurationAndCancellation(t *testing.T) {
	t.Parallel()
	// Arrange.
	ctx, cancel := context.WithCancel(t.Context())
	callbackCalls := 0
	policy := OutputPolicy{
		Identity: Descriptor{ID: "policy", Revision: "1"},
		Project: func(_ context.Context, input OutputPolicyInput) (AbstractPayload, error) {
			callbackCalls++
			cancel()
			return input.Payload, nil
		},
	}
	input := OutputPolicyInput{Kind: ManifestMainOutput, Name: "main", Payload: outputPolicyFixture()}
	// Act.
	_, err := applyOutputPolicy(ctx, &policy, input)
	_, againErr := applyOutputPolicy(ctx, &policy, input)
	missingErr := validateOutputPolicy(&OutputPolicy{Identity: policy.Identity})
	invalidErr := validateOutputPolicy(&OutputPolicy{Project: policy.Project})
	// Assert.
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, againErr, context.Canceled)
	require.Equal(t, 1, callbackCalls)
	require.ErrorIs(t, missingErr, ErrMissingOutputPolicy)
	require.ErrorIs(t, invalidErr, ErrInvalidOutputPolicy)
	require.ErrorIs(t, invalidErr, ErrInvalidDescriptor)
}

func TestOutputPolicyRejectAndNoPolicy(t *testing.T) {
	t.Parallel()
	// Arrange.
	input := OutputPolicyInput{Kind: ManifestMainOutput, Name: "main", Payload: outputPolicyFixture()}
	rejected := errors.New("host rejects output")
	policy := OutputPolicy{
		Identity: Descriptor{ID: "policy", Revision: "1"},
		Project:  func(context.Context, OutputPolicyInput) (AbstractPayload, error) { return AbstractPayload{}, rejected },
	}
	// Act.
	accepted, err := applyOutputPolicy(t.Context(), nil, input)
	_, rejectErr := applyOutputPolicy(t.Context(), &policy, input)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, input.Payload, accepted)
	accepted.System[0].Parts[0] = TextPart{Text: "mutation"}
	require.Equal(t, "secret", input.Payload.System[0].TextContent())
	require.ErrorIs(t, rejectErr, rejected)
}

type outputPolicyExtension struct {
	Secret string `json:"secret"`
}

func (e outputPolicyExtension) ExtensionType() string { return "output-policy-test" }

func (e outputPolicyExtension) CloneExtension() Extension { return e }

func TestOutputPolicyTypedCodecContract(t *testing.T) {
	t.Parallel()
	// Arrange.
	input := OutputPolicyInput{Kind: ManifestMainOutput, Name: "main", Payload: outputPolicyFixture()}
	input.Payload.Memory[0].Extensions = []Extension{outputPolicyExtension{Secret: "private"}}
	policy := OutputPolicy{
		Identity: Descriptor{ID: "host", Revision: "1"},
		Project: func(_ context.Context, input OutputPolicyInput) (AbstractPayload, error) {
			input.Payload.Memory[0].Extensions = []Extension{outputPolicyExtension{Secret: "redacted"}}
			return input.Payload, nil
		},
	}
	registry := NewExtensionRegistry()
	registry.Register("output-policy-test", func(data []byte) (Extension, error) {
		var ext outputPolicyExtension
		err := json.Unmarshal(data, &ext)
		return ext, err
	})
	trace := &compileTrace{
		profile: TraceProfile{
			Encoding:       Descriptor{},
			Codec:          JSONSerializer{Provenance: DefaultProvenanceRegistry(), Extensions: registry},
			Stages:         nil,
			Mapping:        nil,
			Labels:         LabelProjection{Policy: nil, Registry: nil, RequiredTypes: nil},
			RequireOrigins: false,
			Codecs:         nil,
		},
		graph: Lineage{Records: nil}, latest: nil, summaries: nil, ordinal: 0, prefix: "",
	}
	ctx := context.WithValue(t.Context(), compileTraceKey{}, trace)
	// Act.
	accepted, err := applyOutputPolicy(ctx, &policy, input)
	_, missingErr := applyOutputPolicy(t.Context(), &policy, input)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, outputPolicyExtension{Secret: "redacted"}, accepted.Memory[0].Extensions[0])
	require.Equal(t, outputPolicyExtension{Secret: "private"}, input.Payload.Memory[0].Extensions[0])
	require.ErrorIs(t, missingErr, ErrInvalidOutputPolicy)
}

func TestOutputPolicyNilDoesNotRestrictRole(t *testing.T) {
	t.Parallel()
	// Arrange.
	input := OutputPolicyInput{Kind: ManifestMainOutput, Name: "main", Payload: outputPolicyFixture()}
	input.Payload.Memory[0].Role = "host-role"
	// Act.
	accepted, err := applyOutputPolicy(t.Context(), nil, input)
	// Assert.
	require.NoError(t, err)
	require.Equal(t, input.Payload, accepted)
}
