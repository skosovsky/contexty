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

func artifactBudgetRequests(turnID string, artifacts []ContextArtifact) ([]ArtifactBudgetRequest, error) {
	var requests []ArtifactBudgetRequest
	seen := make(map[ContentRef]bool)
	for _, artifact := range artifacts {
		if artifact.Budget == nil || !artifactVisibleInTurn(turnID, artifact) {
			continue
		}
		ref, err := ArtifactContentRef(artifact)
		if err != nil {
			return nil, err
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		requests = append(requests, ArtifactBudgetRequest{Input: ref, TokenLimit: artifact.Budget.TokenLimit})
	}
	return requests, nil
}
