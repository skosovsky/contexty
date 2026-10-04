package contexty

import "slices"

// OutputPolicyDecision binds one final semantic output to a pinned host policy.
// References carry no payload; replay consumes accepted content without callbacks.
type OutputPolicyDecision struct {
	Policy  Descriptor        `json:"policy"`
	Inputs  []ManifestSegment `json:"inputs"`
	Outputs []ManifestSegment `json:"outputs"`
}

func (d *OutputPolicyDecision) clone() *OutputPolicyDecision {
	if d == nil {
		return nil
	}
	return &OutputPolicyDecision{
		Policy:  d.Policy,
		Inputs:  clonePolicySegments(d.Inputs),
		Outputs: clonePolicySegments(d.Outputs),
	}
}

func clonePolicySegments(segments []ManifestSegment) []ManifestSegment {
	result := make([]ManifestSegment, len(segments))
	for i, segment := range segments {
		result[i] = ManifestSegment{Name: segment.Name, Messages: slices.Clone(segment.Messages)}
	}
	return result
}

func (d *OutputPolicyDecision) Validate() error {
	if d == nil {
		return ErrInvalidOutputPolicy
	}
	if err := d.Policy.Validate(); err != nil {
		return err
	}
	if err := validatePolicySegments(d.Inputs); err != nil {
		return err
	}
	if err := validatePolicySegments(d.Outputs); err != nil {
		return err
	}
	for i, input := range d.Inputs {
		output := d.Outputs[i]
		if len(input.Messages) != len(output.Messages) {
			return ErrInvalidOutputPolicy
		}
		for j, ref := range input.Messages {
			if ref.ID != output.Messages[j].ID {
				return ErrInvalidOutputPolicy
			}
		}
	}
	return nil
}

func validatePolicySegments(segments []ManifestSegment) error {
	if len(segments) != len(snapshotSegmentOrder()) {
		return ErrInvalidOutputPolicy
	}
	if err := validateManifestSegments(segments); err != nil {
		return err
	}
	for i, name := range snapshotSegmentOrder() {
		if segments[i].Name != string(name) {
			return ErrInvalidOutputPolicy
		}
		for _, ref := range segments[i].Messages {
			if ref.Occurrence != "" {
				return ErrInvalidOutputPolicy
			}
		}
	}
	return nil
}

func validateManifestPolicyEvidence(manifest CompileManifest) error {
	for _, output := range manifest.Outputs {
		if err := validateOutputPolicyEvidence(manifest.CompileConfiguration.OutputPolicy, output); err != nil {
			return err
		}
		if err := validatePolicyRetention(manifest.Budgets, output); err != nil {
			return err
		}
	}
	return validateMaterializationEvidence(manifest)
}

func validateOutputPolicyEvidence(identity *Descriptor, output ManifestOutput) error {
	decision := output.OutputPolicy
	if identity == nil {
		if decision != nil {
			return ErrInvalidOutputPolicy
		}
		return nil
	}
	if decision == nil || decision.Policy != *identity {
		return ErrInvalidOutputPolicy
	}
	if err := decision.Validate(); err != nil {
		return err
	}
	if err := validatePolicyFinalRefs(
		output,
		opaqueAcceptedSegments(output.OpaqueState, decision.Outputs),
	); err != nil {
		return err
	}
	for i, segment := range decision.Inputs {
		for j, input := range segment.Messages {
			accepted := decision.Outputs[i].Messages[j]
			if input == accepted {
				continue
			}
			if !policyLineageBinding(output.Lineage, *identity, input, accepted) {
				return ErrInvalidLineage
			}
		}
	}
	return nil
}

func validatePolicyFinalRefs(output ManifestOutput, segments []ManifestSegment) error {
	if output.Kind == ManifestMainOutput {
		if len(output.Segments) != len(segments) {
			return ErrInvalidOutputPolicy
		}
		for i, segment := range segments {
			if output.Segments[i].Name != segment.Name ||
				!equalBaseRefs(output.Segments[i].Messages, segment.Messages) {
				return ErrInvalidOutputPolicy
			}
		}
		return nil
	}
	if output.View != "" {
		return validatePolicyRendering(output, segments)
	}
	var refs []ContentRef
	for _, segment := range segments {
		refs = append(refs, segment.Messages...)
	}
	if len(output.Segments) != 1 || !equalBaseRefs(output.Segments[0].Messages, refs) {
		return ErrInvalidOutputPolicy
	}
	return nil
}

func validatePolicyRendering(output ManifestOutput, segments []ManifestSegment) error {
	if output.Rendered == nil {
		return ErrInvalidOutputPolicy
	}
	var refs []ContentRef
	for _, name := range viewSegmentOrder() {
		for _, segment := range segments {
			if segment.Name == string(name) {
				refs = append(refs, segment.Messages...)
			}
		}
	}
	for _, record := range output.Lineage.Records {
		if record.Stage == "render" && slices.Contains(record.Outputs, *output.Rendered) &&
			equalBaseRefs(record.Inputs, refs) {
			return nil
		}
	}
	return ErrInvalidLineage
}

func equalBaseRefs(a, b []ContentRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i, ref := range a {
		if baseContentRef(ref) != baseContentRef(b[i]) {
			return false
		}
	}
	return true
}

func policyLineageBinding(graph Lineage, identity Descriptor, input, output ContentRef) bool {
	for _, record := range graph.Records {
		if record.Stage == "output-policy" && record.Transform == identity &&
			len(record.Inputs) == 1 && len(record.Outputs) == 1 &&
			baseContentRef(record.Inputs[0]) == input && baseContentRef(record.Outputs[0]) == output {
			return true
		}
	}
	return false
}

func validatePolicyRetention(budgets []ManifestBudget, output ManifestOutput) error {
	for _, budget := range budgets {
		if budget.Kind != output.Kind || budget.Target != output.Name || budget.Decision == nil {
			continue
		}
		if err := validateRequiredPolicyRefs(budget.Decision.Required, output); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredPolicyRefs(required []ContentRef, output ManifestOutput) error {
	var refs []ContentRef
	for _, segment := range output.Segments {
		refs = append(refs, segment.Messages...)
	}
	previous := -1
	for _, ref := range required {
		accepted := opaqueAcceptedRef(output.OpaqueState, policyAcceptedRef(output.OutputPolicy, ref))
		position := slices.IndexFunc(
			refs,
			func(candidate ContentRef) bool { return baseContentRef(candidate) == accepted },
		)
		if position <= previous {
			return ErrInvalidRetention
		}
		previous = position
	}
	return nil
}

func policyAcceptedRef(decision *OutputPolicyDecision, ref ContentRef) ContentRef {
	if decision == nil {
		return baseContentRef(ref)
	}
	for i, segment := range decision.Inputs {
		for j, input := range segment.Messages {
			if input == baseContentRef(ref) {
				return decision.Outputs[i].Messages[j]
			}
		}
	}
	return baseContentRef(ref)
}

func validateMaterializationEvidence(manifest CompileManifest) error {
	identity := manifest.CompileConfiguration.Materialization
	if identity == nil && len(manifest.Materializations) != 0 {
		return ErrInvalidArtifactMaterialization
	}
	available := materializationArtifactInputs(manifest)
	seen := make(map[ContentRef]bool)
	for _, decision := range manifest.Materializations {
		if err := decision.Validate(); err != nil {
			return err
		}
		if identity == nil || decision.Policy != *identity || !available[decision.Artifact] || seen[decision.Artifact] {
			return ErrInvalidArtifactMaterialization
		}
		seen[decision.Artifact] = true
		if materializedMessageUsed(manifest, decision.Message) && !materializationLineageBinding(manifest, decision) {
			return ErrInvalidLineage
		}
	}
	for _, resource := range manifest.Resources {
		if resource.Merge != nil && (identity == nil || resource.Merge.Lineage.Records[1].Transform != *identity) {
			return ErrInvalidArtifactMaterialization
		}
	}
	return validatePreparedMaterializations(manifest, available)
}

func validatePreparedMaterializations(manifest CompileManifest, available map[ContentRef]bool) error {
	ids := make(map[string]bool)
	for ref := range available {
		ids["artifact:"+ref.ID] = true
	}
	for _, segment := range manifest.PreparedInputs {
		for _, ref := range segment.Messages {
			if !ids[ref.ID] {
				continue
			}
			if !slices.ContainsFunc(
				manifest.Materializations,
				func(d ArtifactMaterializationDecision) bool { return d.Message == ref },
			) {
				return ErrInvalidArtifactMaterialization
			}
		}
	}
	return nil
}

func materializationArtifactInputs(manifest CompileManifest) map[ContentRef]bool {
	available := make(map[ContentRef]bool)
	for _, segment := range manifest.Inputs {
		if segment.Name == manifestArtifactsSegment {
			for _, ref := range segment.Messages {
				available[baseContentRef(ref)] = true
			}
		}
	}
	for _, resource := range manifest.Resources {
		available[resource.Artifact] = true
		available[resource.Projected] = true
		if resource.Merge != nil {
			available[resource.Merge.Artifact] = true
		}
	}
	return available
}

func materializedMessageUsed(manifest CompileManifest, ref ContentRef) bool {
	for _, segment := range manifest.PreparedInputs {
		if slices.Contains(segment.Messages, ref) {
			return true
		}
	}
	return false
}

func materializationLineageBinding(manifest CompileManifest, decision ArtifactMaterializationDecision) bool {
	for _, output := range manifest.Outputs {
		for _, record := range output.Lineage.Records {
			if !isMaterializationStage(record.Stage) || record.Transform != decision.Policy ||
				len(record.Inputs) != 1 ||
				len(record.Outputs) != 1 {
				continue
			}
			if baseContentRef(record.Inputs[0]) == decision.Artifact &&
				baseContentRef(record.Outputs[0]) == decision.Message {
				return true
			}
		}
	}
	return false
}

func isMaterializationStage(stage string) bool {
	switch stage {
	case traceStageArtifact, traceStageResourceMaterialize, resourceAppendMaterializeStage:
		return true
	default:
		return false
	}
}
