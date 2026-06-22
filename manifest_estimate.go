package contexty

import "slices"

func validateManifestEstimateReports(manifest CompileManifest) error {
	seen := make(map[manifestChannelKey]bool)
	for _, estimate := range manifest.EstimateReports {
		key := manifestChannelKey{kind: estimate.Kind, name: estimate.Name}
		if seen[key] {
			return ErrInvalidEstimateReport
		}
		seen[key] = true
		if err := validateManifestEstimate(manifest, estimate); err != nil {
			return err
		}
	}
	for _, budget := range manifest.Budgets {
		if budget.ReportProfile != nil && !seen[manifestChannelKey{kind: budget.Kind, name: budget.Target}] {
			return ErrMissingEstimateReport
		}
	}
	return nil
}

func validateManifestEstimate(manifest CompileManifest, estimate ManifestEstimateReport) error {
	if err := estimate.Report.Validate(); err != nil {
		return err
	}
	if estimate.Report.ManifestRef != nil || estimate.Report.WireRef != nil ||
		estimate.Report.Profile.Model != manifest.Profile.Model || estimate.Report.Profile.Encoding != manifest.Encoding {
		return ErrStaleEstimate
	}
	budgetIndex := slices.IndexFunc(manifest.Budgets, func(budget ManifestBudget) bool {
		return budget.Kind == estimate.Kind && budget.Target == estimate.Name
	})
	if budgetIndex < 0 {
		return ErrInvalidEstimateReport
	}
	budget := manifest.Budgets[budgetIndex]
	if budget.ReportProfile == nil || estimate.Report.Budget != budget.Request ||
		estimate.Report.Total != budget.EstimatedTokens {
		return ErrStaleEstimate
	}
	profile, err := estimateDigest(*budget.ReportProfile)
	if err != nil || profile != estimate.Report.ProfileDigest {
		return ErrStaleEstimate
	}
	if estimate.Kind == ManifestMainOutput && estimate.Report.Profile.Estimator != manifest.Profile.Estimator {
		return ErrStaleEstimate
	}
	outputIndex := slices.IndexFunc(manifest.Outputs, func(output ManifestOutput) bool {
		return output.Kind == estimate.Kind && output.Name == estimate.Name
	})
	if outputIndex < 0 {
		return ErrInvalidEstimateReport
	}
	return validateEstimateOutputRefs(estimate, manifest.Outputs[outputIndex])
}

func validateEstimateOutputRefs(estimate ManifestEstimateReport, output ManifestOutput) error {
	expectedNames := []string{manifestMessagesSegment}
	if estimate.Kind == ManifestMainOutput {
		expectedNames = nil
		for _, segment := range viewSegmentOrder() {
			expectedNames = append(expectedNames, string(segment))
		}
	}
	if len(estimate.Report.Segments) != len(expectedNames) {
		return ErrStaleEstimate
	}
	for i, name := range expectedNames {
		segment := estimate.Report.Segments[i]
		index := slices.IndexFunc(output.Segments, func(item ManifestSegment) bool { return item.Name == name })
		if index < 0 || segment.Name != name || !slices.Equal(segment.Messages, output.Segments[index].Messages) {
			return ErrStaleEstimate
		}
	}
	return nil
}

func cloneManifestBudgets(budgets []ManifestBudget) []ManifestBudget {
	copyBudgets := slices.Clone(budgets)
	for i, budget := range copyBudgets {
		copyBudgets[i].RollingSummary = cloneRollingSummary(budget.RollingSummary)
		copyBudgets[i].Compaction = cloneCompactionProfile(budget.Compaction)
		copyBudgets[i].Truncation = budget.Truncation.clone()
		copyBudgets[i].Summarizer = cloneDescriptor(budget.Summarizer)
		copyBudgets[i].Estimator = budget.Estimator.clone()
		if budget.ReportProfile != nil {
			profile := budget.ReportProfile.clone()
			copyBudgets[i].ReportProfile = &profile
		}
	}
	return copyBudgets
}
