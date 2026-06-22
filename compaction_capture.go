package contexty

import (
	"context"
	"slices"
)

type compactionCaptureKey struct{}
type compactionCaptureState struct {
	records  []CompactionRecord
	channels []manifestChannelKey
}

// WithCompactionCapture returns proposals; host owns acceptance and storage.
// It requires compile tracing, content privacy capture and an EstimateReporter.
func WithCompactionCapture(profile CompactionProfile) BudgetPipelineOption {
	return func(p *BudgetPipeline) { copyProfile := profile; p.compaction = &copyProfile }
}

func (e *Engine) validateCompactionConfiguration(targets []CompileTarget) error {
	if err := e.validateCompactionPipeline(e.budget); err != nil {
		return err
	}
	for _, target := range targets {
		if err := e.validateCompactionPipeline(target.Budget); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) validateCompactionPipeline(p *BudgetPipeline) error {
	if p == nil || p.compaction == nil {
		return nil
	}
	if e.capture == nil || e.recording == nil {
		return ErrMissingRecordPolicy
	}
	return p.validateCompactionProfile(e.trace, e.capture.descriptor)
}

func (p *BudgetPipeline) validateCompactionProfile(trace *TraceProfile, privacy Descriptor) error {
	if p.compaction == nil {
		return nil
	}
	if trace == nil {
		return ErrMissingLineage
	}
	profile := p.compaction
	for _, descriptor := range []Descriptor{profile.Model, profile.Summarizer, profile.Estimator, profile.Policy, profile.Encoding, profile.Privacy} {
		if err := descriptor.Validate(); err != nil {
			return err
		}
	}
	summarizer, err := p.summarizerDescriptor()
	if err != nil {
		return err
	}
	if profile.Encoding != trace.Encoding || summarizer == nil || profile.Summarizer != *summarizer ||
		profile.Privacy != privacy {
		return ErrInvalidCompaction
	}
	reporter, ok := p.estimator.(*EstimateReporter)
	if !ok || reporter == nil || p.cfg.Summarizer == nil {
		return ErrInvalidCompaction
	}
	if reporter.profile.Model != profile.Model || reporter.profile.Estimator != profile.Estimator ||
		reporter.profile.Encoding != profile.Encoding {
		return ErrStaleEstimate
	}
	return nil
}

func (p *BudgetPipeline) validateCompactionContext(ctx context.Context) error {
	if p.compaction == nil {
		return nil
	}
	trace, capture := traceFromContext(ctx), contentCaptureFrom(ctx)
	if trace == nil {
		return ErrMissingLineage
	}
	if capture == nil {
		return ErrMissingRecordPolicy
	}
	return p.validateCompactionProfile(&trace.profile, capture.options.descriptor)
}

func (p *BudgetPipeline) estimateSummary(ctx context.Context, summary Message, limit int) (int, error) {
	if p.compaction == nil {
		return p.estimateBudgetMessages(ctx, []Message{summary})
	}
	reporter, ok := p.estimator.(*EstimateReporter)
	if !ok || reporter == nil {
		return 0, ErrInvalidCompaction
	}
	report, err := reporter.Report(
		ctx,
		EstimateRequest{Segments: []EstimateSegment{{Name: "summary", Messages: []Message{summary}}},
			Budget: EffectiveInputBudget(limit), ManifestRef: nil, WireRef: nil},
	)
	if err != nil {
		return 0, err
	}
	trace := traceFromContext(ctx)
	ref, err := MessageContentRef(summary, trace.profile.Codec)
	if err != nil {
		return 0, err
	}
	edge, found := compactionSummaryEdge(trace.graph, ref)
	if !found {
		return 0, ErrInvalidCoverage
	}
	record, err := NewCompactionRecord("compaction/"+edge.ID, *p.compaction, edge.Inputs, edge.Outputs[0],
		trace.graph, EffectiveInputBudget(limit), nil, &report)
	if err != nil {
		return 0, err
	}
	state, _ := ctx.Value(compactionCaptureKey{}).(*compactionCaptureState)
	if state == nil {
		return 0, ErrInvalidCompaction
	}
	state.records = append(state.records, record)
	state.channels = append(state.channels, finalBudgetChannel(ctx))
	return report.Total, nil
}

func compactionSummaryEdge(graph Lineage, output ContentRef) (LineageRecord, bool) {
	for _, edge := range slices.Backward(graph.Records) {
		if edge.Stage == traceStageSummarize && len(edge.Outputs) == 1 && baseContentRef(edge.Outputs[0]) == output {
			return edge, true
		}
	}
	return LineageRecord{
		ID:          "",
		Transform:   Descriptor{ID: "", Revision: ""},
		Inputs:      nil,
		Outputs:     nil,
		DecisionRef: "",
		Stage:       "",
	}, false
}

func compileCompactions(ctx context.Context) ([]CompactionRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, _ := ctx.Value(compactionCaptureKey{}).(*compactionCaptureState)
	if state == nil {
		return nil, nil
	}
	capture := contentCaptureFrom(ctx)
	var records []CompactionRecord
	for _, proposal := range state.records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var result *SavedContent
		if content, found := capture.kept[baseContentRef(proposal.Output)]; found {
			copyContent := content.clone()
			result = &copyContent
		}
		record, err := NewCompactionRecord(proposal.ID, proposal.Profile, proposal.Covered, proposal.Output,
			proposal.Lineage, proposal.Budget, result, proposal.Estimate)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
