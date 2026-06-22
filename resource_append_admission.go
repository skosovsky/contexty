package contexty

func validateResourceAppendAdmission(manifest CompileManifest, merge *ResourceArtifactMerge) error {
	if merge == nil {
		return nil
	}
	excludedForBudget := false
	for _, exclusion := range manifest.ExcludedArtifacts {
		if exclusion.Input == merge.Artifact && exclusion.Reason == ReasonTokenBudgetExceeded {
			excludedForBudget = true
		}
	}
	if merge.Admitted == excludedForBudget {
		return ErrInvalidEstimateReport
	}
	for _, estimate := range manifest.ArtifactEstimates {
		if estimate.Input == merge.Artifact {
			if merge.Admitted != (estimate.Tokens <= estimate.TokenLimit) {
				return ErrInvalidEstimateReport
			}
			return nil
		}
	}
	if !merge.Admitted {
		return ErrMissingEstimateReport
	}
	return nil
}
