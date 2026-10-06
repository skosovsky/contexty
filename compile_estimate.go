package contexty

import (
	"context"
	"errors"
	"slices"
	"sort"
)

var ErrMissingEstimateReport = errors.New("contexty: missing final estimate report")

const manifestMessagesSegment = "messages"

type ManifestEstimateReport struct {
	Kind   ManifestOutputKind `json:"kind"`
	Name   string             `json:"name"`
	Report EstimateReport     `json:"report"`
}

type BoundEstimateReport struct {
	Manifest ContentRef         `json:"manifest"`
	Kind     ManifestOutputKind `json:"kind"`
	Name     string             `json:"name"`
	Report   EstimateReport     `json:"report"`
}

type finalEstimateReportsKey struct{}

func (r *EstimateReporter) Estimate(ctx context.Context, messages []Message) (int, error) {
	report, err := r.Report(
		ctx,
		EstimateRequest{Segments: []EstimateSegment{{Name: manifestMessagesSegment, Messages: messages}},
			Budget: EffectiveInputBudget(int(^uint(0) >> 1)), ManifestRef: nil, WireRef: nil},
	)
	if err != nil {
		return 0, err
	}
	return report.Total, nil
}

func (r *EstimateReporter) EstimatePerMessage(ctx context.Context, messages []Message) ([]int, error) {
	report, err := r.Report(
		ctx,
		EstimateRequest{Segments: []EstimateSegment{{Name: manifestMessagesSegment, Messages: messages}},
			Budget: EffectiveInputBudget(int(^uint(0) >> 1)), ManifestRef: nil, WireRef: nil},
	)
	if err != nil {
		return nil, err
	}
	return slices.Clone(report.Segments[0].PerMessage), nil
}

func finalBudgetChannel(ctx context.Context) manifestChannelKey {
	settings, _ := compileIdentityFromContext(ctx)
	if settings.targetName != "" {
		return manifestChannelKey{kind: ManifestTargetOutput, name: settings.targetName}
	}
	return manifestChannelKey{kind: ManifestMainOutput, name: string(ManifestMainOutput)}
}

func recordFinalEstimateReport(ctx context.Context, report EstimateReport) {
	reports, _ := ctx.Value(finalEstimateReportsKey{}).(map[manifestChannelKey]EstimateReport)
	if reports != nil {
		reports[finalBudgetChannel(ctx)] = report
	}
}

func compileEstimateReports(ctx context.Context) ([]ManifestEstimateReport, error) {
	reports, _ := ctx.Value(finalEstimateReportsKey{}).(map[manifestChannelKey]EstimateReport)
	var result []ManifestEstimateReport
	for key, report := range reports {
		copyReport, err := report.Clone()
		if err != nil {
			return nil, err
		}
		result = append(result, ManifestEstimateReport{Kind: key.kind, Name: key.name, Report: copyReport})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (p *BudgetPipeline) reportProfile() *EstimateProfile {
	if reporter, ok := p.estimator.(*EstimateReporter); ok {
		profile := reporter.profile.clone()
		return &profile
	}
	return nil
}

func (p *BudgetPipeline) validateSegments(ctx context.Context, segments []EstimateSegment) error {
	if reporter, ok := p.estimator.(*EstimateReporter); ok {
		report, err := reporter.Report(
			ctx,
			EstimateRequest{Segments: segments, Budget: p.cfg.Budget, ManifestRef: nil, WireRef: nil},
		)
		if err != nil {
			return err
		}
		if report.OverflowReason != "" {
			return ErrBudgetExceeded
		}
		recordFinalBudgetEstimate(ctx, report.Total)
		recordFinalEstimateReport(ctx, report)
		return nil
	}
	var messages []Message
	for _, segment := range segments {
		messages = append(messages, segment.Messages...)
	}
	return p.validateOutput(ctx, messages)
}

func payloadEstimateSegments(payload AbstractPayload) []EstimateSegment {
	return []EstimateSegment{
		{Name: string(SegmentSystem), Messages: payload.System},
		{
			Name:     string(SegmentHistory),
			Messages: payload.History,
		},
		{Name: string(SegmentTools), Messages: payload.Tools},
		{Name: string(SegmentMemory), Messages: payload.Memory},
	}
}

func (m CompileManifest) EstimateFor(kind ManifestOutputKind, name string) (BoundEstimateReport, error) {
	if err := m.Validate(); err != nil {
		return BoundEstimateReport{}, err
	}
	for _, estimate := range m.EstimateReports {
		if estimate.Kind == kind && estimate.Name == name {
			report, err := estimate.Report.Clone()
			if err != nil {
				return BoundEstimateReport{}, err
			}
			return BoundEstimateReport{Manifest: ContentRef{ID: m.ID, Digest: m.Digest, Occurrence: ""},
				Kind: kind, Name: name, Report: report}, nil
		}
	}
	return BoundEstimateReport{}, ErrMissingEstimateReport
}
