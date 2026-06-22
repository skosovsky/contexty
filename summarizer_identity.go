package contexty

import "context"

// WithSummarizerDescriptor pins the caller-owned summarizer for this pipeline.
func WithSummarizerDescriptor(descriptor Descriptor) BudgetPipelineOption {
	return func(p *BudgetPipeline) { copyDescriptor := descriptor; p.summarizer = &copyDescriptor }
}

func cloneDescriptor(descriptor *Descriptor) *Descriptor {
	if descriptor == nil {
		return nil
	}
	copyDescriptor := *descriptor
	return &copyDescriptor
}

func (p *BudgetPipeline) summarizerDescriptor() (*Descriptor, error) {
	if p.cfg.Summarizer == nil {
		if p.summarizer != nil || p.compaction != nil {
			return nil, ErrInvalidRecordingComponent
		}
		return nil, nil //nolint:nilnil // No summarizer is a valid explicitly disabled capability.
	}
	descriptor := p.summarizer
	if p.compaction != nil {
		if descriptor != nil && *descriptor != p.compaction.Summarizer {
			return nil, ErrInvalidCompaction
		}
		descriptor = &p.compaction.Summarizer
	}
	if descriptor == nil {
		return nil, ErrInvalidRecordingComponent
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return cloneDescriptor(descriptor), nil
}

func (p *BudgetPipeline) withSummarizerIdentity(ctx context.Context) context.Context {
	descriptor, err := p.summarizerDescriptor()
	if err != nil || descriptor == nil {
		return ctx
	}
	return context.WithValue(ctx, recordingStageComponentKey{},
		recordingStageComponent{stage: traceStageSummarize, descriptor: *descriptor})
}

func (p *BudgetPipeline) validateRecordingBudget() error {
	if _, err := p.estimatorIdentity(); err != nil {
		return err
	}
	if _, err := p.truncationProfile(); err != nil {
		return err
	}
	_, err := p.summarizerDescriptor()
	return err
}

func validateManifestSummarizers(manifest CompileManifest) error {
	mainIDs := manifestMainInvocationIDs(manifest.Outputs)
	for _, output := range manifest.Outputs {
		var descriptor *Descriptor
		for _, budget := range manifest.Budgets {
			if budget.Kind == output.Kind && budget.Target == output.Name {
				descriptor = budget.Summarizer
			}
		}
		for _, edge := range output.Lineage.Records {
			if !manifestOwnsSummary(manifest, output, edge, mainIDs) {
				continue
			}
			if descriptor == nil || edge.Transform != *descriptor {
				return ErrInvalidRecordingComponent
			}
		}
	}
	return nil
}
