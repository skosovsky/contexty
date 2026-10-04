package contexty

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func policyEvidenceFixture(t *testing.T) CompileManifest {
	t.Helper()
	before := TextMessage(RoleUser, "private")
	before.ID = "message"
	after := TextMessage(RoleUser, "accepted")
	after.ID = before.ID
	input, err := MessageContentRef(before, DefaultJSONSerializer())
	require.NoError(t, err)
	output, err := MessageContentRef(after, DefaultJSONSerializer())
	require.NoError(t, err)
	identity := Descriptor{ID: "host/output", Revision: "1"}
	segments := func(ref ContentRef) []ManifestSegment {
		return []ManifestSegment{
			{Name: string(SegmentSystem), Messages: nil},
			{Name: string(SegmentHistory), Messages: []ContentRef{ref}},
			{Name: string(SegmentMemory), Messages: nil},
			{Name: string(SegmentTools), Messages: nil},
		}
	}
	return CompileManifest{
		CompileConfiguration: CompileConfiguration{
			Materialization: nil,
			OutputPolicy:    &identity,
			Options:         ContentRef{},
			Deferred:        nil,
			Outputs:         nil,
		},
		Outputs: []ManifestOutput{
			{
				Kind:     ManifestMainOutput,
				Name:     "main",
				Segments: segments(output),
				OutputPolicy: &OutputPolicyDecision{
					Policy:  identity,
					Inputs:  segments(input),
					Outputs: segments(output),
				},
				Lineage: Lineage{
					Unresolved: []ContentRef{input},
					Records: []LineageRecord{
						{
							ID:        "output-policy/1",
							Transform: identity,
							Inputs: []ContentRef{
								input,
							},
							Outputs:     []ContentRef{output},
							Stage:       "output-policy",
							DecisionRef: "",
						},
					},
				},
			},
		},
		Budgets: []ManifestBudget{{
			Kind: ManifestMainOutput, Target: "main", Decision: &BudgetDecision{Required: []ContentRef{input}},
		}},
	}
}

func TestPolicyEvidenceRequiredMapsOnlyThroughPinnedDecision(t *testing.T) {
	// Arrange.
	manifest := policyEvidenceFixture(t)
	// Act.
	err := validateManifestPolicyEvidence(manifest)
	// Assert.
	require.NoError(t, err)
}

func TestPolicyEvidenceRejectsStaleBindings(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CompileManifest)
	}{
		{name: "missing decision", mutate: func(m *CompileManifest) { m.Outputs[0].OutputPolicy = nil }},
		{name: "unconfigured decision", mutate: func(m *CompileManifest) { m.CompileConfiguration.OutputPolicy = nil }},
		{
			name:   "different descriptor",
			mutate: func(m *CompileManifest) { m.Outputs[0].OutputPolicy.Policy.Revision = "2" },
		},
		{name: "missing lineage", mutate: func(m *CompileManifest) { m.Outputs[0].Lineage.Records = nil }},
		{
			name:   "different lineage descriptor",
			mutate: func(m *CompileManifest) { m.Outputs[0].Lineage.Records[0].Transform.Revision = "2" },
		},
		{name: "changed final refs", mutate: func(m *CompileManifest) {
			m.Outputs[0].Segments[1].Messages[0] = m.Outputs[0].OutputPolicy.Inputs[1].Messages[0]
		}},
		{
			name:   "required refs unrelated",
			mutate: func(m *CompileManifest) { m.Budgets[0].Decision.Required[0].ID = "missing" },
		},
		{name: "reordered segment", mutate: func(m *CompileManifest) {
			m.Outputs[0].OutputPolicy.Outputs[0], m.Outputs[0].OutputPolicy.Outputs[1] = m.Outputs[0].OutputPolicy.Outputs[1], m.Outputs[0].OutputPolicy.Outputs[0]
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			manifest := policyEvidenceFixture(t)
			test.mutate(&manifest)
			// Act.
			err := validateManifestPolicyEvidence(manifest)
			// Assert.
			require.Error(t, err)
		})
	}
}

func TestOutputPolicyDecisionCloneOwnsRefs(t *testing.T) {
	// Arrange.
	manifest := policyEvidenceFixture(t)
	original := manifest.Outputs[0].OutputPolicy
	// Act.
	owned := original.clone()
	owned.Inputs[1].Messages[0].ID = "changed"
	owned.Outputs[1].Messages[0].ID = "changed"
	// Assert.
	require.Equal(t, "message", original.Inputs[1].Messages[0].ID)
	require.Equal(t, "message", original.Outputs[1].Messages[0].ID)
}

func TestMaterializationEvidenceRequiresPreparedRepresentationBinding(t *testing.T) {
	// Arrange.
	manifest := policyEvidenceFixture(t)
	artifact := ContentRef{
		ID:         "retrieval",
		Digest:     manifest.Outputs[0].OutputPolicy.Inputs[1].Messages[0].Digest,
		Occurrence: "",
	}
	message := manifest.Outputs[0].OutputPolicy.Inputs[1].Messages[0]
	message.ID = "artifact:" + artifact.ID
	identity := Descriptor{ID: "host/artifact", Revision: "1"}
	manifest.CompileConfiguration.Materialization = &identity
	manifest.Inputs = []ManifestSegment{{Name: manifestArtifactsSegment, Messages: []ContentRef{artifact}}}
	manifest.PreparedInputs = []ManifestSegment{{Name: string(SegmentMemory), Messages: []ContentRef{message}}}
	manifest.Materializations = []ArtifactMaterializationDecision{
		{Policy: identity, Artifact: artifact, Message: message},
	}
	manifest.Outputs[0].Lineage.Records = append(manifest.Outputs[0].Lineage.Records, LineageRecord{
		ID:          "artifact/1",
		Transform:   identity,
		Inputs:      []ContentRef{artifact},
		Outputs:     []ContentRef{message},
		DecisionRef: "",
		Stage:       "artifact",
	})
	// Act.
	err := validateManifestPolicyEvidence(manifest)
	// Assert.
	require.NoError(t, err)

	// Arrange.
	manifest.Materializations = nil
	// Act.
	err = validateManifestPolicyEvidence(manifest)
	// Assert.
	require.ErrorIs(t, err, ErrInvalidArtifactMaterialization)
}
