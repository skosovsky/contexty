package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime"
	"slices"
	"strings"
)

var (
	ErrInvalidEstimateReport = errors.New("contexty: invalid estimate report")
	ErrUnknownEstimateCost   = errors.New("contexty: unknown content token cost")
	ErrInconsistentEstimate  = errors.New("contexty: inconsistent estimator totals")
	ErrStaleEstimate         = errors.New("contexty: stale estimate evidence")
)

type EstimateQuality string

const (
	EstimateCounted   EstimateQuality = "counted"
	EstimateEstimated EstimateQuality = "estimated"
	EstimateUnknown   EstimateQuality = "unknown"
)

type EstimateKind string

const (
	EstimateText       EstimateKind = "text"
	EstimateImage      EstimateKind = "image"
	EstimateToolCall   EstimateKind = "tool_call"
	EstimateToolResult EstimateKind = "tool_result"
	EstimateMedia      EstimateKind = "media_payload"
	EstimateExtension  EstimateKind = "extension"
)

type EstimateFallback struct {
	Policy Descriptor `json:"policy"`
	Tokens int        `json:"tokens"`
}

type EstimateProfile struct {
	Model        Descriptor                         `json:"model"`
	Estimator    Descriptor                         `json:"estimator"`
	Method       Descriptor                         `json:"method"`
	Encoding     Descriptor                         `json:"encoding"`
	Capabilities map[EstimateKind]EstimateQuality   `json:"capabilities"`
	Fallback     *EstimateFallback                  `json:"fallback,omitempty"`
	Extensions   map[string]EstimateExtensionPolicy `json:"extensions,omitempty"`
}

func (p EstimateProfile) clone() EstimateProfile {
	p.Capabilities = maps.Clone(p.Capabilities)
	p.Extensions = maps.Clone(p.Extensions)
	if p.Fallback != nil {
		fallback := *p.Fallback
		p.Fallback = &fallback
	}
	return p
}

func (p EstimateProfile) validate() error {
	for _, descriptor := range []Descriptor{p.Model, p.Estimator, p.Method, p.Encoding} {
		if err := descriptor.Validate(); err != nil {
			return err
		}
	}
	for kind, quality := range p.Capabilities {
		if !slices.Contains(
			[]EstimateKind{
				EstimateText,
				EstimateImage,
				EstimateToolCall,
				EstimateToolResult,
				EstimateMedia,
				EstimateExtension,
			},
			kind,
		) ||
			!validEstimateQuality(quality) {
			return ErrInvalidEstimateReport
		}
	}
	if err := validateEstimateExtensionPolicies(p.Extensions); err != nil {
		return err
	}
	if p.Fallback != nil {
		if err := p.Fallback.Policy.Validate(); err != nil {
			return err
		}
		if p.Fallback.Tokens <= 0 {
			return ErrInvalidEstimateReport
		}
	}
	return nil
}

func validEstimateQuality(quality EstimateQuality) bool {
	return slices.Contains([]EstimateQuality{EstimateCounted, EstimateEstimated, EstimateUnknown}, quality)
}

type EstimateSegment struct {
	Name     string
	Messages []Message
}

type EstimateRequest struct {
	Segments    []EstimateSegment
	Budget      BudgetRequest
	ManifestRef *ContentRef
	WireRef     *ContentRef
}

type EstimateCoverage struct {
	Kind           EstimateKind    `json:"kind"`
	Quality        EstimateQuality `json:"quality"`
	Parts          int             `json:"parts"`
	FallbackTokens int             `json:"fallback_tokens"`
}

type SegmentEstimate struct {
	Name       string             `json:"name"`
	Messages   []ContentRef       `json:"messages"`
	PerMessage []int              `json:"per_message"`
	Tokens     int                `json:"tokens"`
	Coverage   []EstimateCoverage `json:"coverage"`
	PartKinds  [][][]EstimateKind `json:"part_kinds"`
	// ExtensionTypes includes metadata-only values; PartKinds includes one
	// synthetic extension group per participating value after the AST groups.
	ExtensionTypes [][]string `json:"extension_types"`
}

type EstimateReport struct {
	Digest         string            `json:"digest"`
	RequestDigest  string            `json:"request_digest"`
	ProfileDigest  string            `json:"profile_digest"`
	Profile        EstimateProfile   `json:"profile"`
	Budget         BudgetRequest     `json:"budget"`
	EffectiveLimit int               `json:"effective_limit"`
	Segments       []SegmentEstimate `json:"segments"`
	Total          int               `json:"total"`
	Quality        EstimateQuality   `json:"quality"`
	OverflowReason string            `json:"overflow_reason,omitempty"`
	ManifestRef    *ContentRef       `json:"manifest_ref,omitempty"`
	WireRef        *ContentRef       `json:"wire_ref,omitempty"`
}

type EstimateReporter struct {
	estimator TokenEstimator
	profile   EstimateProfile
	codec     JSONSerializer
}

// NewEstimateReporter captures configuration, not execution or provider handles.
func NewEstimateReporter(
	estimator TokenEstimator,
	profile EstimateProfile,
	codec JSONSerializer,
) (*EstimateReporter, error) {
	if estimator == nil {
		return nil, ErrInvalidEstimateReport
	}
	if err := profile.validate(); err != nil {
		return nil, err
	}
	if accuracy, ok := estimator.(interface{ EstimateAccuracy() EstimateQuality }); ok {
		if accuracy.EstimateAccuracy() != EstimateCounted {
			for _, quality := range profile.Capabilities {
				if quality == EstimateCounted {
					return nil, ErrInvalidEstimateReport
				}
			}
		}
	}
	if capabilities, ok := estimator.(interface {
		EstimateCapabilities() map[EstimateKind]EstimateQuality
	}); ok {
		actual := capabilities.EstimateCapabilities()
		for kind, quality := range profile.Capabilities {
			if quality != EstimateUnknown && (actual[kind] == "" || actual[kind] == EstimateUnknown) {
				return nil, ErrInvalidEstimateReport
			}
		}
	}
	return &EstimateReporter{estimator: freezeBuiltinEstimator(estimator), profile: profile.clone(),
		codec: snapshotJSONSerializer(codec)}, nil
}

func (r *EstimateReporter) Report(ctx context.Context, request EstimateRequest) (EstimateReport, error) {
	if err := ctx.Err(); err != nil {
		return EstimateReport{}, err
	}
	limit, err := request.Budget.Resolve()
	if err != nil {
		return EstimateReport{}, err
	}
	if err = validateEstimateRefs(request.ManifestRef, request.WireRef); err != nil {
		return EstimateReport{}, err
	}
	request, err = r.prepareRequest(request)
	if err != nil {
		return EstimateReport{}, err
	}
	report := EstimateReport{ //nolint:exhaustruct_v5 // digest/totals populated from captured segments below
		Profile:        r.profile.clone(),
		Budget:         request.Budget,
		EffectiveLimit: limit,
		Quality:        EstimateCounted,
		ManifestRef:    cloneContentRef(request.ManifestRef),
		WireRef:        cloneContentRef(request.WireRef),
	}
	if accuracy, ok := r.estimator.(interface{ EstimateAccuracy() EstimateQuality }); ok {
		report.Quality = worseEstimateQuality(report.Quality, accuracy.EstimateAccuracy())
	}
	segments, err := r.estimateSegments(ctx, request.Segments)
	if err != nil {
		return EstimateReport{}, err
	}
	for _, estimate := range segments {
		report.Total, err = addEstimateTokens(report.Total, estimate.Tokens)
		if err != nil {
			return EstimateReport{}, err
		}
		for _, coverage := range estimate.Coverage {
			report.Quality = worseEstimateQuality(report.Quality, coverage.Quality)
		}
		if len(estimate.Coverage) == 0 && estimate.Tokens > 0 {
			report.Quality = worseEstimateQuality(report.Quality, EstimateEstimated)
		}
		report.Segments = append(report.Segments, estimate)
	}
	if report.Total > limit {
		report.OverflowReason = ReasonTokenBudgetExceeded
	}
	report.ProfileDigest, err = estimateDigest(report.Profile)
	if err != nil {
		return EstimateReport{}, err
	}
	report.RequestDigest, err = estimateRequestDigest(report)
	if err != nil {
		return EstimateReport{}, err
	}
	if err = ctx.Err(); err != nil {
		return EstimateReport{}, err
	}
	return finalizeEstimateReport(report)
}

// Freeze and preflight every segment before any estimator callback runs.
func (r *EstimateReporter) prepareRequest(request EstimateRequest) (EstimateRequest, error) {
	request.ManifestRef, request.WireRef = cloneContentRef(request.ManifestRef), cloneContentRef(request.WireRef)
	request.Segments = slices.Clone(request.Segments)
	seen := make(map[string]bool)
	for i, segment := range request.Segments {
		if segment.Name == "" || seen[segment.Name] {
			return EstimateRequest{}, ErrInvalidEstimateReport
		}
		seen[segment.Name] = true
		request.Segments[i].Messages = cloneMessageSlice(segment.Messages)
		for _, message := range request.Segments[i].Messages {
			if _, err := MessageContentRef(message, r.codec); err != nil {
				return EstimateRequest{}, err
			}
			if _, _, _, err := r.estimateParts(message.Parts); err != nil {
				return EstimateRequest{}, err
			}
			if _, _, _, _, err := r.estimateExtensions(message.Extensions); err != nil {
				return EstimateRequest{}, err
			}
		}
	}
	return request, nil
}

func validateEstimateRefs(refs ...*ContentRef) error {
	for _, ref := range refs {
		if ref != nil {
			if err := ref.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *EstimateReporter) prepareSegment(segment EstimateSegment) (SegmentEstimate, []Message, []int, error) {
	result := SegmentEstimate{
		Name:           segment.Name,
		Messages:       nil,
		PerMessage:     nil,
		Tokens:         0,
		Coverage:       nil,
		PartKinds:      nil,
		ExtensionTypes: nil,
	}
	known := cloneMessageSlice(segment.Messages)
	fallbacks := make([]int, len(known))
	for i, message := range segment.Messages {
		ref, err := MessageContentRef(message, r.codec)
		if err != nil {
			return SegmentEstimate{}, nil, nil, err
		}
		result.Messages = append(result.Messages, ref)
		var kinds [][]EstimateKind
		for _, part := range message.Parts {
			partKinds, kindErr := estimatePartKinds(part)
			if kindErr != nil {
				return SegmentEstimate{}, nil, nil, kindErr
			}
			kinds = append(kinds, partKinds)
		}
		parts, coverage, fallback, err := r.estimateParts(message.Parts)
		if err != nil {
			return SegmentEstimate{}, nil, nil, err
		}
		extensions, types, extensionCoverage, extensionFallback, err := r.estimateExtensions(message.Extensions)
		if err != nil {
			return SegmentEstimate{}, nil, nil, err
		}
		for _, typeID := range types {
			if !r.profile.Extensions[typeID].MetadataOnly {
				kinds = append(kinds, []EstimateKind{EstimateExtension})
			}
		}
		result.PartKinds = append(result.PartKinds, kinds)
		result.ExtensionTypes = append(result.ExtensionTypes, types)
		fallback, err = addEstimateTokens(fallback, extensionFallback)
		if err != nil {
			return SegmentEstimate{}, nil, nil, err
		}
		known[i].Parts, known[i].Extensions, fallbacks[i] = parts, extensions, fallback
		coverage = mergeEstimateCoverage(coverage, extensionCoverage)
		result.Coverage = mergeEstimateCoverage(result.Coverage, coverage)
	}
	return result, known, fallbacks, nil
}

func (r *EstimateReporter) estimateSegments(ctx context.Context, inputs []EstimateSegment) ([]SegmentEstimate, error) {
	var segments []SegmentEstimate
	var known []Message
	var fallbacks []int
	for _, input := range inputs {
		segment, messages, fallback, err := r.prepareSegment(input)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
		known, fallbacks = append(known, messages...), append(fallbacks, fallback...)
	}
	weights, err := r.estimateKnown(ctx, known)
	if err != nil {
		return nil, err
	}
	offset := 0
	for i, segment := range segments {
		for range segment.Messages {
			value, addErr := addEstimateTokens(weights[offset], fallbacks[offset])
			if addErr != nil {
				return nil, addErr
			}
			segments[i].PerMessage = append(segments[i].PerMessage, value)
			segments[i].Tokens, err = addEstimateTokens(segments[i].Tokens, value)
			if err != nil {
				return nil, err
			}
			offset++
		}
	}
	return segments, nil
}

func (r *EstimateReporter) estimateKnown(ctx context.Context, known []Message) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	perMessage, err := r.estimator.EstimatePerMessage(ctx, cloneMessageSlice(known))
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenCountFailed, err)
	}
	perMessage = slices.Clone(perMessage)
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	total, err := r.estimator.Estimate(ctx, cloneMessageSlice(known))
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenCountFailed, err)
	}
	if len(perMessage) != len(known) {
		return nil, ErrInconsistentEstimate
	}
	var sum int
	for _, tokens := range perMessage {
		sum, err = addEstimateTokens(sum, tokens)
		if err != nil {
			return nil, err
		}
	}
	if sum != total {
		return nil, ErrInconsistentEstimate
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return slices.Clone(perMessage), nil
}

func (r *EstimateReporter) estimateParts(parts []ContentPart) ([]ContentPart, []EstimateCoverage, int, error) {
	var known []ContentPart
	var coverage []EstimateCoverage
	var fallback int
	for _, part := range parts {
		kinds, err := estimatePartKinds(part)
		if err != nil {
			return nil, nil, 0, err
		}
		partCoverage, quality := r.partCoverage(kinds)
		if quality != EstimateUnknown {
			known = append(known, part.clonePart())
			coverage = mergeEstimateCoverage(coverage, partCoverage)
			continue
		}
		if r.profile.Fallback == nil {
			return nil, nil, 0, ErrUnknownEstimateCost
		}
		fallback, err = addEstimateTokens(fallback, r.profile.Fallback.Tokens)
		if err != nil {
			return nil, nil, 0, err
		}
		partCoverage = recordEstimateFallback(partCoverage, kinds, r.profile.Fallback.Tokens)
		// The whole part was excluded from the counter: none of its cost is measured.
		for i := range partCoverage {
			partCoverage[i].Quality = EstimateUnknown
		}
		coverage = mergeEstimateCoverage(coverage, partCoverage)
	}
	return known, coverage, fallback, nil
}

func (r *EstimateReporter) partCoverage(kinds []EstimateKind) ([]EstimateCoverage, EstimateQuality) {
	var coverage []EstimateCoverage
	quality := EstimateCounted
	for _, kind := range kinds {
		status, found := r.profile.Capabilities[kind]
		if !found {
			status = EstimateUnknown
		}
		quality = worseEstimateQuality(quality, status)
		coverage = append(coverage, EstimateCoverage{Kind: kind, Quality: status, Parts: 1, FallbackTokens: 0})
	}
	return coverage, quality
}

func estimatePartKinds(part ContentPart) ([]EstimateKind, error) {
	switch value := canonicalPartValue(part).(type) {
	case TextPart:
		return []EstimateKind{EstimateText}, nil
	case ImagePart:
		return []EstimateKind{EstimateImage}, nil
	case MediaPart:
		if err := value.Validate(); err != nil {
			return nil, err
		}
		return []EstimateKind{EstimateMedia}, nil
	case ToolCallPart:
		return estimatePayloadKinds(EstimateToolCall, value.Arguments), nil
	case ToolResultPart:
		return estimatePayloadKinds(EstimateToolResult, value.Payload), nil
	default:
		return nil, ErrInvalidEstimateReport
	}
}

func estimatePayloadKinds(kind EstimateKind, payload ToolPayload) []EstimateKind {
	kinds := []EstimateKind{kind}
	mediaType, _, parseErr := mime.ParseMediaType(payload.MIMEType)
	major, _, _ := strings.Cut(mediaType, "/")
	if len(payload.Binary) > 0 || (payload.MIMEType != "" &&
		(parseErr != nil || (mediaType != mimeApplicationJSON && major != "text"))) {
		kinds = append(kinds, EstimateMedia)
	}
	return kinds
}

func mergeEstimateCoverage(dst, src []EstimateCoverage) []EstimateCoverage {
	for _, incoming := range src {
		index := slices.IndexFunc(dst, func(item EstimateCoverage) bool { return item.Kind == incoming.Kind })
		if index < 0 {
			dst = append(dst, incoming)
			continue
		}
		dst[index].Parts += incoming.Parts
		dst[index].FallbackTokens += incoming.FallbackTokens
		dst[index].Quality = worseEstimateQuality(dst[index].Quality, incoming.Quality)
	}
	return dst
}

func recordEstimateFallback(coverage []EstimateCoverage, kinds []EstimateKind, tokens int) []EstimateCoverage {
	// Attribute the fallback once, to the first unknown kind of this whole part.
	for i, entry := range coverage {
		if entry.Quality == EstimateUnknown && slices.Contains(kinds, entry.Kind) {
			coverage[i].FallbackTokens += tokens
			break
		}
	}
	return coverage
}

func worseEstimateQuality(a, b EstimateQuality) EstimateQuality {
	if a == EstimateUnknown || b == EstimateUnknown {
		return EstimateUnknown
	}
	if a == EstimateEstimated || b == EstimateEstimated {
		return EstimateEstimated
	}
	return EstimateCounted
}

func addEstimateTokens(a, b int) (int, error) {
	if a < 0 || b < 0 || b > int(^uint(0)>>1)-a {
		return 0, ErrInconsistentEstimate
	}
	return a + b, nil
}

func estimateDigest(value any) (string, error) {
	wire, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return canonicalJSONDigest(wire)
}

func estimateRequestDigest(report EstimateReport) (string, error) {
	inputs := make([]ManifestSegment, len(report.Segments))
	kinds := make([][][][]EstimateKind, len(report.Segments))
	extensions := make([][][]string, len(report.Segments))
	for i, segment := range report.Segments {
		inputs[i] = ManifestSegment{Name: segment.Name, Messages: slices.Clone(segment.Messages)}
		kinds[i] = segment.PartKinds
		extensions[i] = segment.ExtensionTypes
	}
	return estimateDigest(struct {
		Inputs        []ManifestSegment    `json:"inputs"`
		Kinds         [][][][]EstimateKind `json:"kinds"`
		Extensions    [][][]string         `json:"extensions"`
		Budget        BudgetRequest        `json:"budget"`
		ProfileDigest string               `json:"profile_digest"`
		ManifestRef   *ContentRef          `json:"manifest_ref,omitempty"`
		WireRef       *ContentRef          `json:"wire_ref,omitempty"`
	}{Inputs: inputs, Kinds: kinds, Extensions: extensions, Budget: report.Budget, ProfileDigest: report.ProfileDigest,
		ManifestRef: report.ManifestRef, WireRef: report.WireRef})
}
