package contexty

import "slices"

func validateArtifactEstimates(manifest CompileManifest) error {
	var inputs []ContentRef
	for _, segment := range manifest.Inputs {
		if segment.Name == manifestArtifactsSegment {
			inputs = segment.Messages
		}
	}
	inputs = append(inputs, manifestDerivedArtifactRefs(manifest)...)
	requests := make(map[ContentRef]ArtifactBudgetRequest)
	for _, request := range manifest.ArtifactBudgets {
		if _, duplicate := requests[request.Input]; duplicate || request.TokenLimit < 0 ||
			!slices.Contains(inputs, request.Input) {
			return ErrInvalidEstimateReport
		}
		requests[request.Input] = request
	}
	seen := make(map[ContentRef]bool)
	for _, estimate := range manifest.ArtifactEstimates {
		request, found := requests[estimate.Input]
		if !found || seen[estimate.Input] || estimate.TokenLimit != request.TokenLimit {
			return ErrInvalidEstimateReport
		}
		seen[estimate.Input] = true
		if err := validateArtifactEstimate(manifest, estimate); err != nil {
			return err
		}
	}
	if len(seen) != len(requests) {
		return ErrMissingEstimateReport
	}
	return nil
}

func validateArtifactEstimate(manifest CompileManifest, estimate ArtifactBudgetEstimate) error {
	if estimate.Tokens < 0 || !validEstimateQuality(estimate.Quality) ||
		estimate.Message.ID != "artifact:"+estimate.Input.ID || estimate.Message.Occurrence != "" {
		return ErrInvalidEstimateReport
	}
	if err := estimate.Message.Validate(); err != nil {
		return err
	}
	reason := ""
	for _, excluded := range manifest.ExcludedArtifacts {
		if excluded.Input == estimate.Input {
			reason = excluded.Reason
		}
	}
	if estimate.Tokens > estimate.TokenLimit {
		if reason != ReasonTokenBudgetExceeded {
			return ErrInvalidEstimateReport
		}
	} else if reason == ReasonTokenBudgetExceeded || reason == artifactInactiveReason {
		return ErrInvalidEstimateReport
	}
	var profile *EstimateProfile
	for _, budget := range manifest.Budgets {
		if budget.Kind == ManifestMainOutput {
			profile = budget.ReportProfile
		}
	}
	if profile == nil {
		if estimate.Report != nil || estimate.Quality != EstimateEstimated {
			return ErrInvalidEstimateReport
		}
		return nil
	}
	return validateArtifactReport(estimate, *profile)
}

func validateArtifactReport(estimate ArtifactBudgetEstimate, profile EstimateProfile) error {
	if estimate.Report == nil {
		return ErrMissingEstimateReport
	}
	report := estimate.Report
	if err := report.Validate(); err != nil {
		return err
	}
	profileDigest, err := estimateDigest(profile)
	if err != nil || profileDigest != report.ProfileDigest || report.Total != estimate.Tokens ||
		report.Quality != estimate.Quality || report.Budget != EffectiveInputBudget(estimate.TokenLimit) ||
		report.ManifestRef != nil || report.WireRef != nil {
		return ErrStaleEstimate
	}
	if len(report.Segments) != 1 || report.Segments[0].Name != artifactEstimateSegment ||
		!slices.Equal(report.Segments[0].Messages, []ContentRef{estimate.Message}) {
		return ErrStaleEstimate
	}
	return nil
}
