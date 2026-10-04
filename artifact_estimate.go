package contexty

import (
	"context"
	"slices"
)

const artifactEstimateSegment = "artifact"

type ArtifactBudgetRequest struct {
	Input      ContentRef `json:"input"`
	TokenLimit int        `json:"token_limit"`
}

// ArtifactBudgetEstimate describes admission, not the final transformed output.
type ArtifactBudgetEstimate struct {
	Input      ContentRef      `json:"input"`
	Message    ContentRef      `json:"message"`
	TokenLimit int             `json:"token_limit"`
	Tokens     int             `json:"tokens"`
	Quality    EstimateQuality `json:"quality"`
	Report     *EstimateReport `json:"report,omitempty"`
}

func estimateArtifact(
	ctx context.Context,
	estimator TokenEstimator,
	artifact ContextArtifact,
	message Message,
) (ArtifactBudgetEstimate, error) {
	input, err := ArtifactContentRef(artifact)
	if err != nil {
		return ArtifactBudgetEstimate{}, err
	}
	ref, err := MessageContentRef(message, DefaultJSONSerializer())
	if err != nil {
		return ArtifactBudgetEstimate{}, err
	}
	estimate := ArtifactBudgetEstimate{Input: input, Message: ref, TokenLimit: artifact.Budget.TokenLimit,
		Tokens: 0, Quality: EstimateEstimated, Report: nil}
	if reporter, ok := estimator.(*EstimateReporter); ok {
		report, reportErr := reporter.Report(
			ctx,
			EstimateRequest{Segments: []EstimateSegment{{Name: artifactEstimateSegment, Messages: []Message{message}}},
				Budget: EffectiveInputBudget(artifact.Budget.TokenLimit), ManifestRef: nil, WireRef: nil},
		)
		if reportErr != nil {
			return ArtifactBudgetEstimate{}, reportErr
		}
		estimate.Tokens, estimate.Quality, estimate.Report = report.Total, report.Quality, &report
		return estimate, nil
	}
	estimate.Tokens, err = estimator.Estimate(ctx, []Message{message})
	if err != nil {
		return ArtifactBudgetEstimate{}, err
	}
	return estimate, nil
}

func compileArtifactEstimates(ctx context.Context, artifacts []ContextArtifact) ([]ArtifactBudgetEstimate, error) {
	evidence, _ := ctx.Value(artifactEstimatesKey{}).(map[ContentRef]ArtifactBudgetEstimate)
	var estimates []ArtifactBudgetEstimate
	seen := make(map[ContentRef]bool)
	for _, artifact := range artifacts {
		ref, err := ArtifactContentRef(artifact)
		if err != nil {
			return nil, err
		}
		if estimate, found := evidence[ref]; found && !seen[ref] {
			estimates = append(estimates, estimate)
			seen[ref] = true
		}
	}
	return cloneArtifactEstimates(estimates)
}

func cloneArtifactEstimates(estimates []ArtifactBudgetEstimate) ([]ArtifactBudgetEstimate, error) {
	result := slices.Clone(estimates)
	for i, estimate := range result {
		if estimate.Report == nil {
			continue
		}
		report, err := estimate.Report.Clone()
		if err != nil {
			return nil, err
		}
		result[i].Report = &report
	}
	return result, nil
}

func cloneValidArtifactEstimates(estimates []ArtifactBudgetEstimate) []ArtifactBudgetEstimate {
	result := slices.Clone(estimates)
	for i, estimate := range result {
		if estimate.Report != nil {
			report := cloneEstimateReportValue(*estimate.Report)
			result[i].Report = &report
		}
	}
	return result
}

// Cloning owns values without validating or changing their evidence. Validation
// belongs to the manifest boundary and must return an error rather than panic.
func cloneEstimateReportValue(report EstimateReport) EstimateReport {
	report.Profile = report.Profile.clone()
	report.ManifestRef = cloneContentRef(report.ManifestRef)
	report.WireRef = cloneContentRef(report.WireRef)
	report.Segments = slices.Clone(report.Segments)
	for i, segment := range report.Segments {
		segment.Messages = slices.Clone(segment.Messages)
		segment.PerMessage = slices.Clone(segment.PerMessage)
		segment.Coverage = slices.Clone(segment.Coverage)
		segment.PartKinds = slices.Clone(segment.PartKinds)
		for j, groups := range segment.PartKinds {
			segment.PartKinds[j] = slices.Clone(groups)
			for k, kinds := range groups {
				segment.PartKinds[j][k] = slices.Clone(kinds)
			}
		}
		segment.ExtensionTypes = slices.Clone(segment.ExtensionTypes)
		for j, types := range segment.ExtensionTypes {
			segment.ExtensionTypes[j] = slices.Clone(types)
		}
		report.Segments[i] = segment
	}
	return report
}

func artifactRequestsFromEstimates(estimates []ArtifactBudgetEstimate) []ArtifactBudgetRequest {
	var out []ArtifactBudgetRequest
	for _, estimate := range estimates {
		out = append(out, ArtifactBudgetRequest{Input: estimate.Input, TokenLimit: estimate.TokenLimit})
	}
	return out
}
