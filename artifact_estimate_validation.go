package contexty

import "slices"

func validateArtifactEstimates(manifest CompileManifest) error {
	for _, output := range manifest.Outputs {
		requests, estimates, exclusions := output.ArtifactBudgets, output.ArtifactEstimates, output.ExcludedArtifacts
		if output.Kind == ManifestMainOutput {
			if len(requests) != 0 {
				return ErrInvalidEstimateReport
			}
			requests, estimates, exclusions = manifest.ArtifactBudgets, manifest.ArtifactEstimates, manifest.ExcludedArtifacts
		}
		if err := validateOutputArtifactEstimates(manifest, output, requests, estimates, exclusions); err != nil {
			return err
		}
	}
	return nil
}

func validateOutputArtifactEstimates(manifest CompileManifest, output ManifestOutput,
	budgets []ArtifactBudgetRequest, estimates []ArtifactBudgetEstimate, exclusions []ArtifactExclusion,
) error {
	inputs := manifestArtifactInputs(manifest)
	requests := make(map[ContentRef]ArtifactBudgetRequest)
	for _, request := range budgets {
		if _, duplicate := requests[request.Input]; duplicate || request.TokenLimit < 0 ||
			!slices.Contains(inputs, request.Input) {
			return ErrInvalidEstimateReport
		}
		requests[request.Input] = request
	}
	seen := make(map[ContentRef]bool)
	for _, estimate := range estimates {
		request, found := requests[estimate.Input]
		if !found || seen[estimate.Input] || estimate.TokenLimit != request.TokenLimit {
			return ErrInvalidEstimateReport
		}
		seen[estimate.Input] = true
		if err := validateOutputArtifactEstimate(manifest, output, exclusions, estimate); err != nil {
			return err
		}
	}
	if len(seen) != len(requests) {
		return ErrMissingEstimateReport
	}
	return nil
}

func validateOutputArtifactEstimate(manifest CompileManifest, output ManifestOutput,
	exclusions []ArtifactExclusion, estimate ArtifactBudgetEstimate,
) error {
	if estimate.Tokens < 0 || !validEstimateQuality(estimate.Quality) ||
		estimate.Message.ID != "artifact:"+estimate.Input.ID || estimate.Message.Occurrence != "" {
		return ErrInvalidEstimateReport
	}
	if err := estimate.Message.Validate(); err != nil {
		return err
	}
	if err := validateArtifactAdmission(output.Selection, exclusions, estimate); err != nil {
		return err
	}
	var profile *EstimateProfile
	for _, budget := range manifest.Budgets {
		if budget.Kind == output.Kind && budget.Target == output.Name {
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

func validateArtifactAdmission(selection *SelectionDecision, exclusions []ArtifactExclusion,
	estimate ArtifactBudgetEstimate,
) error {
	reason := ""
	for _, excluded := range exclusions {
		if excluded.Input == estimate.Input {
			reason = excluded.Reason
		}
	}
	if estimate.Tokens > estimate.TokenLimit {
		if reason != ReasonTokenBudgetExceeded {
			return ErrInvalidEstimateReport
		}
		return nil
	}
	if reason == artifactInactiveReason || (reason == ReasonTokenBudgetExceeded &&
		(!selectionExcludedForBudget(selection, estimate.Input) ||
			(estimate.Report != nil && estimate.Report.OverflowReason == ReasonTokenBudgetExceeded))) {
		return ErrInvalidEstimateReport
	}
	return nil
}

func selectionExcludedForBudget(selection *SelectionDecision, ref ContentRef) bool {
	if selection == nil {
		return false
	}
	return slices.ContainsFunc(selection.Candidates, func(candidate CandidateDecision) bool {
		return candidate.Ref == ref && !candidate.Selected && candidate.Reason == ReasonTokenBudgetExceeded
	})
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
