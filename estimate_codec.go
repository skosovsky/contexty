package contexty

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
)

func finalizeEstimateReport(report EstimateReport) (EstimateReport, error) {
	digest, err := report.contentDigest()
	if err != nil {
		return EstimateReport{}, err
	}
	report.Digest = digest
	if err := report.Validate(); err != nil {
		return EstimateReport{}, err
	}
	return report, nil
}

func (r EstimateReport) contentDigest() (string, error) {
	r.Digest = ""
	return estimateDigest(r)
}

func (r EstimateReport) Validate() error {
	if err := r.Profile.validate(); err != nil {
		return err
	}
	limit, err := r.Budget.Resolve()
	if err != nil {
		return err
	}
	if limit != r.EffectiveLimit || !validEstimateQuality(r.Quality) {
		return ErrInvalidEstimateReport
	}
	if err = validateEstimateRefs(r.ManifestRef, r.WireRef); err != nil {
		return err
	}
	if err = validateEstimateSegments(r); err != nil {
		return err
	}
	profile, err := estimateDigest(r.Profile)
	if err != nil || profile != r.ProfileDigest {
		return ErrStaleEstimate
	}
	request, err := estimateRequestDigest(r)
	if err != nil || request != r.RequestDigest {
		return ErrStaleEstimate
	}
	digest, err := r.contentDigest()
	if err != nil || digest != r.Digest {
		return ErrInvalidEstimateReport
	}
	return nil
}

func validateEstimateSegments(report EstimateReport) error {
	seen := make(map[string]bool)
	var total int
	quality := EstimateCounted
	for _, segment := range report.Segments {
		if segment.Name == "" || seen[segment.Name] {
			return ErrInvalidEstimateReport
		}
		seen[segment.Name] = true
		if err := validateEstimateSegment(segment, report.Profile); err != nil {
			return err
		}
		var err error
		total, err = addEstimateTokens(total, segment.Tokens)
		if err != nil {
			return err
		}
		for _, coverage := range segment.Coverage {
			quality = worseEstimateQuality(quality, coverage.Quality)
		}
		if len(segment.Coverage) == 0 && segment.Tokens > 0 {
			quality = worseEstimateQuality(quality, EstimateEstimated)
		}
	}
	if total != report.Total || worseEstimateQuality(report.Quality, quality) != report.Quality {
		return ErrInconsistentEstimate
	}
	reason := ""
	if total > report.EffectiveLimit {
		reason = ReasonTokenBudgetExceeded
	}
	if report.OverflowReason != reason {
		return ErrInvalidEstimateReport
	}
	return nil
}

func validateEstimateSegment(segment SegmentEstimate, profile EstimateProfile) error {
	if len(segment.Messages) != len(segment.PerMessage) || len(segment.Messages) != len(segment.PartKinds) {
		return ErrInconsistentEstimate
	}
	if err := validateEstimateExtensions(segment, profile); err != nil {
		return err
	}
	if err := validateManifestSegmentRefs(segment.Messages); err != nil {
		return err
	}
	var sum int
	for _, tokens := range segment.PerMessage {
		var err error
		sum, err = addEstimateTokens(sum, tokens)
		if err != nil {
			return err
		}
	}
	if sum != segment.Tokens {
		return ErrInconsistentEstimate
	}
	return validateEstimateCoverage(segment, profile)
}

func validateEstimateCoverage(segment SegmentEstimate, profile EstimateProfile) error {
	if err := validateEstimateKindCounts(segment); err != nil {
		return err
	}
	seen := make(map[EstimateKind]bool)
	var fallbacks int
	var unknown bool
	for _, entry := range segment.Coverage {
		if seen[entry.Kind] {
			return ErrInvalidEstimateReport
		}
		seen[entry.Kind] = true
		if err := validateEstimateSegmentCoverageEntry(entry, segment, profile); err != nil {
			return err
		}
		if entry.Quality == EstimateUnknown {
			unknown = true
		}
		var err error
		fallbacks, err = addEstimateTokens(fallbacks, entry.FallbackTokens)
		if err != nil {
			return err
		}
	}
	if unknown && (profile.Fallback == nil || fallbacks == 0) {
		return ErrUnknownEstimateCost
	}
	expected, err := estimateExpectedFallback(segment, profile)
	if err != nil {
		return err
	}
	if expected != fallbacks {
		return ErrInconsistentEstimate
	}
	if fallbacks > segment.Tokens || (!unknown && fallbacks != 0) ||
		(len(segment.Messages) == 0 && len(segment.Coverage) != 0) {
		return ErrInconsistentEstimate
	}
	return nil
}

func validateEstimateSegmentCoverageEntry(
	entry EstimateCoverage,
	segment SegmentEstimate,
	profile EstimateProfile,
) error {
	if err := validateEstimateCoverageEntry(entry, profile); err != nil {
		return err
	}
	if entry.Kind == EstimateExtension && entry.Quality != estimateExtensionCoverageQuality(segment, profile) {
		return ErrInvalidEstimateReport
	}
	return nil
}

func validateEstimateCoverageEntry(entry EstimateCoverage, profile EstimateProfile) error {
	if !validEstimateKind(entry.Kind) || !validEstimateQuality(entry.Quality) || entry.Parts <= 0 {
		return ErrInvalidEstimateReport
	}
	declared, found := profile.Capabilities[entry.Kind]
	if !found {
		declared = EstimateUnknown
	}
	if worseEstimateQuality(entry.Quality, declared) != entry.Quality ||
		(entry.Quality != EstimateUnknown && entry.FallbackTokens != 0) {
		return ErrInvalidEstimateReport
	}
	return nil
}

func estimateExpectedFallback(segment SegmentEstimate, profile EstimateProfile) (int, error) {
	var total int
	for index, message := range segment.PartKinds {
		unsupported := estimateUnsupportedUnits(message, segment.ExtensionTypes[index], profile)
		if unsupported == 0 {
			continue
		}
		if profile.Fallback == nil {
			return 0, ErrUnknownEstimateCost
		}
		if profile.Fallback.Tokens > int(^uint(0)>>1)/unsupported {
			return 0, ErrInconsistentEstimate
		}
		var err error
		total, err = addEstimateTokens(total, unsupported*profile.Fallback.Tokens)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func estimateUnsupportedUnits(message [][]EstimateKind, types []string, profile EstimateProfile) int {
	count := 0
	for _, part := range message {
		if slices.Contains(part, EstimateExtension) {
			continue
		}
		unsupported := slices.ContainsFunc(part, func(kind EstimateKind) bool {
			quality, found := profile.Capabilities[kind]
			return !found || quality == EstimateUnknown
		})
		if unsupported {
			count++
		}
	}
	for _, typeID := range types {
		if !profile.Extensions[typeID].MetadataOnly && estimateExtensionQuality(profile, typeID) == EstimateUnknown {
			count++
		}
	}
	return count
}

func estimateExtensionCoverageQuality(segment SegmentEstimate, profile EstimateProfile) EstimateQuality {
	quality := EstimateCounted
	for _, types := range segment.ExtensionTypes {
		for _, typeID := range types {
			if !profile.Extensions[typeID].MetadataOnly {
				quality = worseEstimateQuality(quality, estimateExtensionQuality(profile, typeID))
			}
		}
	}
	return quality
}

func validateEstimateKindCounts(segment SegmentEstimate) error {
	counts := make(map[EstimateKind]int)
	for _, message := range segment.PartKinds {
		for _, part := range message {
			if len(part) == 0 {
				return ErrInvalidEstimateReport
			}
			for _, kind := range part {
				if !validEstimateKind(kind) {
					return ErrInvalidEstimateReport
				}
				counts[kind]++
			}
		}
	}
	if len(counts) != len(segment.Coverage) {
		return ErrInvalidEstimateReport
	}
	for _, entry := range segment.Coverage {
		if counts[entry.Kind] != entry.Parts {
			return ErrInvalidEstimateReport
		}
	}
	return nil
}

func validEstimateKind(kind EstimateKind) bool {
	return slices.Contains(
		[]EstimateKind{
			EstimateText,
			EstimateImage,
			EstimateToolCall,
			EstimateToolResult,
			EstimateMedia,
			EstimateExtension,
		},
		kind,
	)
}

func validateEstimateExtensions(segment SegmentEstimate, profile EstimateProfile) error {
	if len(segment.ExtensionTypes) != len(segment.Messages) {
		return ErrInvalidEstimateReport
	}
	for i, types := range segment.ExtensionTypes {
		if err := validateEstimateExtensionGroups(segment.PartKinds[i], types, profile); err != nil {
			return err
		}
	}
	return nil
}

func validateEstimateExtensionGroups(parts [][]EstimateKind, types []string, profile EstimateProfile) error {
	participating := 0
	for _, typeID := range types {
		if typeID == "" {
			return ErrInvalidEstimateReport
		}
		if !profile.Extensions[typeID].MetadataOnly {
			participating++
		}
	}
	groups := 0
	for _, kinds := range parts {
		if slices.Contains(kinds, EstimateExtension) {
			if len(kinds) != 1 {
				return ErrInvalidEstimateReport
			}
			groups++
		}
	}
	if groups != participating {
		return ErrInvalidEstimateReport
	}
	return nil
}

func EncodeEstimateReport(report EstimateReport) ([]byte, error) {
	if err := report.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(report)
}

func DecodeEstimateReport(wire []byte) (EstimateReport, error) {
	var report EstimateReport
	if err := decodeEstimateValue(wire, &report); err != nil {
		return EstimateReport{}, err
	}
	if err := report.Validate(); err != nil {
		return EstimateReport{}, err
	}
	return report, nil
}

func decodeEstimateValue(wire []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.Join(ErrInvalidEstimateReport, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidEstimateReport
	}
	return nil
}

func (r EstimateReport) Clone() (EstimateReport, error) {
	wire, err := EncodeEstimateReport(r)
	if err != nil {
		return EstimateReport{}, err
	}
	return DecodeEstimateReport(wire)
}
