package contexty

import (
	"context"
	"fmt"
)

type sharedPreparationKey struct{}
type preparedOutputKey struct{}
type preparedOutput struct {
	pending []Message
	options compileOptions
}

type artifactExclusionsKey struct{}
type artifactEstimatesKey struct{}

const artifactInactiveReason = "artifact_inactive"

// selectArtifacts prepares lifecycle-visible revisions once. During shared
// preparation no budget admission occurs; each output evaluates its own caps.
func (e *Engine) selectArtifacts(
	ctx context.Context,
	turnID string,
	artifacts []ContextArtifact,
) ([]ContextArtifact, error) {
	estimator := TokenEstimator(CharTokenEstimator{})
	if e.budget != nil {
		estimator = e.budget.estimator
	}
	decisions, _ := ctx.Value(artifactExclusionsKey{}).(map[ContentRef]string)
	var active []ContextArtifact
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reason, err := artifactSelectionReason(ctx, estimator, turnID, artifact)
		if err != nil {
			return nil, err
		}
		if reason == "" {
			active = upsertArtifact(active, artifact)
			continue
		}
		if decisions != nil {
			ref, err := ArtifactContentRef(artifact)
			if err != nil {
				return nil, err
			}
			decisions[ref] = reason
		}
	}
	return active, nil
}

func artifactSelectionReason(
	ctx context.Context,
	estimator TokenEstimator,
	turnID string,
	artifact ContextArtifact,
) (string, error) {
	if !artifactVisibleInTurn(turnID, artifact) {
		return artifactInactiveReason, nil
	}
	message, err := artifactMessage(artifact)
	if err != nil {
		return "", err
	}
	if shared, _ := ctx.Value(sharedPreparationKey{}).(bool); shared || artifact.Budget == nil {
		return "", nil
	}
	if artifact.Budget.TokenLimit < 0 {
		return "", ErrInvalidBudgetRequest
	}
	estimate, err := estimateArtifact(ctx, estimator, artifact, message)
	if canceled := ctx.Err(); canceled != nil {
		return "", canceled
	}
	if err != nil {
		return "", fmt.Errorf("contexty: artifact estimate: %w: %w", ErrTokenCountFailed, err)
	}
	if estimate.Tokens < 0 {
		return "", ErrTokenCountFailed
	}
	if estimates, _ := ctx.Value(artifactEstimatesKey{}).(map[ContentRef]ArtifactBudgetEstimate); estimates != nil {
		estimates[estimate.Input] = estimate
	}
	if estimate.Tokens > artifact.Budget.TokenLimit {
		return ReasonTokenBudgetExceeded, nil
	}
	return "", nil
}
