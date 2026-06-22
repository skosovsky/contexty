package contexty

import (
	"context"
	"fmt"
	"slices"
)

// ManifestCompaction pins a proposed record, not a later host acceptance.
// It contains no saved bytes and grants no permission to fetch them.
type ManifestCompaction struct {
	Kind       ManifestOutputKind `json:"kind"`
	Target     string             `json:"target"`
	Record     ContentRef         `json:"record"`
	Invocation string             `json:"invocation"`
}

func linkCompileCompactions(ctx context.Context, manifest *CompileManifest, records []CompactionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, _ := ctx.Value(compactionCaptureKey{}).(*compactionCaptureState)
	if state == nil || len(state.channels) != len(records) {
		return ErrInvalidCompaction
	}
	for i, record := range records {
		if err := record.Validate(); err != nil {
			return err
		}
		edge, found := compactionSummaryEdge(record.Lineage, baseContentRef(record.Output))
		if !found {
			return ErrInvalidCoverage
		}
		channel := state.channels[i]
		manifest.Compactions = append(manifest.Compactions, ManifestCompaction{
			Kind: channel.kind, Target: channel.name,
			Record: ContentRef{ID: record.ID, Digest: record.Digest, Occurrence: ""}, Invocation: edge.ID,
		})
	}
	digest, err := manifest.contentDigest()
	if err != nil {
		return err
	}
	manifest.Digest = digest
	return manifest.Validate()
}

func validateManifestCompactions(manifest CompileManifest) error {
	required, err := manifestCompactionInvocations(manifest)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(manifest.Compactions))
	invocations := make(map[string]struct{}, len(manifest.Compactions))
	for _, link := range manifest.Compactions {
		if err := link.Record.Validate(); err != nil {
			return err
		}
		if link.Record.Occurrence != "" || link.Invocation == "" {
			return ErrInvalidCompaction
		}
		if _, duplicate := seen[link.Record.ID]; duplicate {
			return ErrInvalidCompaction
		}
		seen[link.Record.ID] = struct{}{}
		if _, duplicate := invocations[link.Invocation]; duplicate {
			return ErrInvalidCompaction
		}
		invocations[link.Invocation] = struct{}{}
		if slices.Contains(manifest.InheritedTransforms, link.Invocation) {
			return ErrInvalidCompaction
		}
		channel, exists := required[link.Invocation]
		if !exists || channel != (manifestChannelKey{kind: link.Kind, name: link.Target}) {
			return fmt.Errorf("%w: invocation %q does not belong to %s/%s", ErrInvalidCompaction,
				link.Invocation, link.Kind, link.Target)
		}
	}
	if len(invocations) != len(required) {
		return fmt.Errorf("%w: %d links for %d required invocations", ErrInvalidCompaction,
			len(invocations), len(required))
	}
	return nil
}

func manifestCompactionInvocations(manifest CompileManifest) (map[string]manifestChannelKey, error) {
	required := make(map[string]manifestChannelKey)
	mainIDs := manifestMainInvocationIDs(manifest.Outputs)
	for _, budget := range manifest.Budgets {
		if budget.Compaction == nil {
			continue
		}
		if err := validateManifestCompactionProfile(manifest, budget); err != nil {
			return nil, err
		}
		for _, output := range manifest.Outputs {
			if output.Kind != budget.Kind || output.Name != budget.Target {
				continue
			}
			if err := addManifestCompactionInvocations(
				required, manifest, output, mainIDs, budget.Compaction.Summarizer,
			); err != nil {
				return nil, err
			}
		}
	}
	return required, nil
}

func validateManifestCompactionProfile(manifest CompileManifest, budget ManifestBudget) error {
	profile := budget.Compaction
	if err := profile.validate(); err != nil {
		return err
	}
	if profile.Model != manifest.Profile.Model || profile.Encoding != manifest.Encoding ||
		manifest.Privacy == nil || profile.Privacy != *manifest.Privacy ||
		budget.Summarizer == nil || profile.Summarizer != *budget.Summarizer || budget.ReportProfile == nil {
		return fmt.Errorf("%w: capture profile mismatch for %s/%s", ErrInvalidCompaction, budget.Kind, budget.Target)
	}
	if profile.Estimator != budget.ReportProfile.Estimator || profile.Model != budget.ReportProfile.Model ||
		profile.Encoding != budget.ReportProfile.Encoding {
		return fmt.Errorf("%w: capture estimator mismatch for %s/%s", ErrInvalidCompaction, budget.Kind, budget.Target)
	}
	if budget.RollingSummary != nil && profile.Policy != budget.RollingSummary.Descriptor {
		return fmt.Errorf("%w: capture recipe mismatch for %s/%s", ErrInvalidCompaction, budget.Kind, budget.Target)
	}
	return nil
}

func addManifestCompactionInvocations(required map[string]manifestChannelKey, manifest CompileManifest,
	output ManifestOutput, mainIDs map[string]struct{}, summarizer Descriptor) error {
	for _, edge := range output.Lineage.Records {
		if !manifestOwnsSummary(manifest, output, edge, mainIDs) {
			continue
		}
		if len(edge.Inputs) == 0 || len(edge.Outputs) != 1 || edge.Transform != summarizer {
			return fmt.Errorf("%w: invalid summary invocation %q", ErrInvalidCompaction, edge.ID)
		}
		if _, duplicate := required[edge.ID]; duplicate {
			return fmt.Errorf("%w: duplicate required invocation %q", ErrInvalidCompaction, edge.ID)
		}
		required[edge.ID] = manifestChannelKey{kind: output.Kind, name: output.Name}
	}
	return nil
}

func manifestMainInvocationIDs(outputs []ManifestOutput) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, output := range outputs {
		if output.Kind != ManifestMainOutput {
			continue
		}
		for _, edge := range output.Lineage.Records {
			ids[edge.ID] = struct{}{}
		}
	}
	return ids
}

func manifestOwnsSummary(manifest CompileManifest, output ManifestOutput, edge LineageRecord,
	mainIDs map[string]struct{}) bool {
	// Input-only edges represent removals, not executions returning summaries.
	if edge.Stage != traceStageSummarize || len(edge.Outputs) == 0 ||
		slices.Contains(manifest.InheritedTransforms, edge.ID) {
		return false
	}
	_, inheritedMain := mainIDs[edge.ID]
	return output.Kind != ManifestTargetOutput || !inheritedMain
}
