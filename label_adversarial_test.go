package contexty_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func TestLabel_RequiredTypesSnapshot(t *testing.T) {
	// Arrange: a callback retains and mutates the caller's required-type slice.
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(data)}, nil
	})
	required := []string{"required-host-label"}
	projection := contexty.LabelProjection{Registry: registry, RequiredTypes: required,
		Policy: fixtureLabelPolicy(func(context.Context, []contexty.Message, contexty.Message,
			contexty.Descriptor) (contexty.LabelDecision, error) {
			required[0] = "fixture-label"
			return contexty.LabelDecision{Extensions: []contexty.Extension{fixtureWireExtension{wire: `{}`}}}, nil
		})}
	// Act.
	message, decision, err := projection.Project(context.Background(), nil,
		contexty.TextMessage(contexty.RoleSystem, "output"), contexty.Descriptor{ID: "role", Revision: "pinned"})
	// Assert: the original requirement cannot be relaxed mid-operation.
	require.ErrorIs(t, err, contexty.ErrMissingLabelCodec)
	require.Zero(t, message)
	require.Empty(t, decision)
}

func TestLabel_RoleProjection(t *testing.T) {
	for _, scenario := range []string{"preserve", "upgrade", "missing-decision", "conflict"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: role promotion is distinct from host metadata authorization.
			registry := contexty.NewExtensionRegistry()
			registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
				return fixtureWireExtension{wire: string(data)}, nil
			})
			profile := fixtureTraceProfile()
			profile.Codec.Extensions = registry
			roleCalls := 0
			profile.Labels = contexty.LabelProjection{Registry: registry, RequiredTypes: []string{"fixture-label"},
				Policy: fixtureRoleLabelPolicy(t, scenario, &roleCalls)}
			input := contexty.TextMessage(contexty.RoleUser, "external input")
			input.ID = "external"
			input.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"host":"external"}`}}
			engine := contexty.NewEngine(
				contexty.WithTraceProfile(profile),
				contexty.WithRoleProjectionPolicy(
					contexty.RoleProjectionFunc(func(contexty.Message) (contexty.Role, error) {
						return contexty.RoleSystem, nil
					}),
				),
			)
			// Act.
			result, err := engine.CompileSnapshot(context.Background(), contexty.CompileRequest{
				CompilationID: "role-labels", History: []contexty.Message{input},
			})
			// Assert: conflicts/unauthorized upgrades yield no partially compiled result.
			require.Equal(t, 1, roleCalls)
			if scenario == "conflict" || scenario == "missing-decision" {
				want := contexty.ErrLabelConflict
				if scenario == "missing-decision" {
					want = contexty.ErrInvalidTrustUpgrade
				}
				require.ErrorIs(t, err, want)
				require.Zero(t, result)
				return
			}
			require.NoError(t, err)
			require.Equal(t, contexty.RoleSystem, result.Payload.History[0].Role)
			wantLabel, wantDecision := input.Extensions, ""
			if scenario == "upgrade" {
				wantLabel = []contexty.Extension{fixtureWireExtension{wire: `{"host":"approved"}`}}
				wantDecision = "host-authorization"
			}
			require.Equal(t, wantLabel, result.Payload.History[0].Extensions)
			for _, record := range result.Lineage.Records {
				if record.Stage == "role" {
					require.Equal(t, wantDecision, record.DecisionRef)
				}
			}
			require.Equal(t, contexty.RoleUser, input.Role)
			require.JSONEq(t, `{"host":"external"}`, input.Extensions[0].(fixtureWireExtension).wire)
		})
	}
}

func TestLabel_WhitespaceUpgrade(t *testing.T) {
	// Arrange: whitespace cannot identify a host authorization decision.
	projection := contexty.LabelProjection{Policy: fixtureLabelPolicy(func(context.Context, []contexty.Message,
		contexty.Message, contexty.Descriptor) (contexty.LabelDecision, error) {
		return contexty.LabelDecision{Upgrade: true, DecisionRef: " \t\n"}, nil
	})}
	// Act.
	message, decision, err := projection.Project(context.Background(), nil,
		contexty.TextMessage(contexty.RoleSystem, "output"), contexty.Descriptor{ID: "role", Revision: "pinned"})
	// Assert.
	require.ErrorIs(t, err, contexty.ErrInvalidTrustUpgrade)
	require.Zero(t, message)
	require.Empty(t, decision)
}

func TestLabel_MissingPolicy(t *testing.T) {
	for _, scenario := range []string{"input-label", "output-label", "required", "unlabeled"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: metadata transport requires an explicit host policy.
			registry := contexty.NewExtensionRegistry()
			registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
				return fixtureWireExtension{wire: string(data)}, nil
			})
			projection := contexty.LabelProjection{Registry: registry}
			input := contexty.TextMessage(contexty.RoleUser, "input")
			output := contexty.TextMessage(contexty.RoleSystem, "output")
			label := []contexty.Extension{fixtureWireExtension{wire: `{}`}}
			switch scenario {
			case "input-label":
				input.Extensions = label
			case "output-label":
				output.Extensions = label
			case "required":
				projection.RequiredTypes = []string{"fixture-label"}
			}
			// Act.
			message, decision, err := projection.Project(context.Background(), []contexty.Message{input},
				output, contexty.Descriptor{ID: "role", Revision: "pinned"})
			// Assert: only metadata-free projection can omit the policy.
			if scenario == "unlabeled" {
				require.NoError(t, err)
				require.Equal(t, output, message)
			} else {
				require.ErrorIs(t, err, contexty.ErrMissingLabelPolicy)
				require.Zero(t, message)
			}
			require.Empty(t, decision)
		})
	}
}

func TestLabel_LossyCodec(t *testing.T) {
	// Arrange: matching type ID alone cannot prove faithful transport of host labels.
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func([]byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: `{"trust":"changed"}`}, nil
	})
	calls := 0
	projection := contexty.LabelProjection{Registry: registry,
		Policy: fixtureLabelPolicy(func(context.Context, []contexty.Message, contexty.Message,
			contexty.Descriptor) (contexty.LabelDecision, error) {
			calls++
			return contexty.LabelDecision{}, nil
		})}
	input := contexty.TextMessage(contexty.RoleUser, "input")
	input.Extensions = []contexty.Extension{fixtureWireExtension{wire: `{"trust":"external"}`}}
	// Act.
	message, decision, err := projection.Project(context.Background(), []contexty.Message{input},
		contexty.TextMessage(contexty.RoleSystem, "output"), contexty.Descriptor{ID: "role", Revision: "pinned"})
	// Assert: reject before host policy execution, with no partial output.
	require.ErrorIs(t, err, contexty.ErrMissingLabelCodec)
	require.Zero(t, message)
	require.Empty(t, decision)
	require.Zero(t, calls)
}

func TestLabel_DecisionCodec(t *testing.T) {
	for _, scenario := range []string{"lossy", "canonical"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: output labels need faithful transport, not byte-identical JSON formatting.
			registry := contexty.NewExtensionRegistry()
			registry.Register("fixture-label", func([]byte) (contexty.Extension, error) {
				wire := `{"b":2,"a":1}`
				if scenario == "lossy" {
					wire = `{"a":1,"b":3}`
				}
				return fixtureWireExtension{wire: wire}, nil
			})
			label := fixtureWireExtension{wire: `{"a": 1, "b": 2}`}
			projection := contexty.LabelProjection{Registry: registry, RequiredTypes: []string{"fixture-label"},
				Policy: fixtureLabelPolicy(func(context.Context, []contexty.Message, contexty.Message,
					contexty.Descriptor) (contexty.LabelDecision, error) {
					return contexty.LabelDecision{
						Extensions:  []contexty.Extension{label},
						DecisionRef: "host-choice",
					}, nil
				})}
			// Act.
			message, decision, err := projection.Project(
				context.Background(),
				nil,
				contexty.TextMessage(
					contexty.RoleSystem,
					"output",
				),
				contexty.Descriptor{ID: "role", Revision: "pinned"},
			)
			// Assert: reject changed payload, but permit canonical-equivalent encoding.
			if scenario == "lossy" {
				require.ErrorIs(t, err, contexty.ErrMissingLabelCodec)
				require.Zero(t, message)
				require.Empty(t, decision)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []contexty.Extension{label}, message.Extensions)
			require.Equal(t, "host-choice", decision)
		})
	}
}

func TestLabel_CodecCancellation(t *testing.T) {
	for _, scenario := range []string{"input", "decision"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: a decoder can observe/cancel the caller context during validation.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			registry := contexty.NewExtensionRegistry()
			decodes, policies := 0, 0
			registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
				decodes++
				if scenario == "input" || decodes == 2 {
					cancel()
				}
				return fixtureWireExtension{wire: string(data)}, nil
			})
			label := fixtureWireExtension{wire: `{"host":"label"}`}
			projection := contexty.LabelProjection{Registry: registry,
				Policy: fixtureLabelPolicy(func(context.Context, []contexty.Message, contexty.Message,
					contexty.Descriptor) (contexty.LabelDecision, error) {
					policies++
					return contexty.LabelDecision{Extensions: []contexty.Extension{label}}, nil
				})}
			input := contexty.TextMessage(contexty.RoleUser, "input")
			input.Extensions = []contexty.Extension{label}
			// Act.
			message, decision, err := projection.Project(
				ctx,
				[]contexty.Message{input},
				contexty.TextMessage(
					contexty.RoleSystem,
					"output",
				),
				contexty.Descriptor{ID: "role", Revision: "pinned"},
			)
			// Assert: cancellation stops subsequent callbacks and discards all output.
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, message)
			require.Empty(t, decision)
			if scenario == "input" {
				require.Zero(t, policies)
			} else {
				require.Equal(t, 1, policies)
			}
		})
	}
}
