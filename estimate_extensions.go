package contexty

import (
	"bytes"
	"errors"
)

var ErrMissingEstimateExtensionCodec = errors.New("contexty: missing estimate extension codec")

// EstimateExtensionPolicy is a host decision about wire participation, not trust.
type EstimateExtensionPolicy struct {
	Codec        Descriptor `json:"codec"`
	Policy       Descriptor `json:"policy"`
	MetadataOnly bool       `json:"metadata_only"`
}

func (r *EstimateReporter) estimateExtensions(
	inputs []Extension,
) ([]Extension, []string, []EstimateCoverage, int, error) {
	var known []Extension
	var types []string
	var coverage []EstimateCoverage
	var fallback int
	for _, extension := range inputs {
		if extension == nil || extension.ExtensionType() == "" {
			return nil, nil, nil, 0, ErrInvalidEstimateReport
		}
		typeID := extension.ExtensionType()
		if err := r.validateEstimateExtensionCodec(extension); err != nil {
			return nil, nil, nil, 0, err
		}
		if !r.hasExtensionCodec(typeID) && typeID != OpaqueStateExtensionType {
			return nil, nil, nil, 0, ErrMissingEstimateExtensionCodec
		}
		types = append(types, typeID)
		if r.profile.Extensions[typeID].MetadataOnly {
			continue
		}
		kinds := []EstimateKind{EstimateExtension}
		quality := estimateExtensionQuality(r.profile, typeID)
		partCoverage := []EstimateCoverage{{Kind: EstimateExtension, Quality: quality, Parts: 1, FallbackTokens: 0}}
		if quality != EstimateUnknown {
			known = append(known, extension.CloneExtension())
			coverage = mergeEstimateCoverage(coverage, partCoverage)
			continue
		}
		if r.profile.Fallback == nil {
			return nil, nil, nil, 0, ErrUnknownEstimateCost
		}
		var err error
		fallback, err = addEstimateTokens(fallback, r.profile.Fallback.Tokens)
		if err != nil {
			return nil, nil, nil, 0, err
		}
		coverage = mergeEstimateCoverage(
			coverage,
			recordEstimateFallback(partCoverage, kinds, r.profile.Fallback.Tokens),
		)
	}
	return known, types, coverage, fallback, nil
}

func (r *EstimateReporter) validateEstimateExtensionCodec(extension Extension) error {
	if extension.ExtensionType() != OpaqueStateExtensionType {
		return nil
	}
	encoded, err := EncodeExtension(extension)
	if err != nil {
		return err
	}
	if r.codec.Extensions == nil {
		return ErrMissingEstimateExtensionCodec
	}
	restored, err := r.codec.Extensions.Decode(encoded)
	if err != nil {
		return errors.Join(ErrMissingEstimateExtensionCodec, err)
	}
	again, err := EncodeExtension(restored)
	if err != nil {
		return err
	}
	if !bytes.Equal(encoded, again) {
		return ErrMissingEstimateExtensionCodec
	}
	return nil
}

func estimateExtensionQuality(profile EstimateProfile, typeID string) EstimateQuality {
	if _, found := profile.Extensions[typeID]; !found {
		return EstimateUnknown
	}
	quality := profile.Capabilities[EstimateExtension]
	if quality == "" {
		return EstimateUnknown
	}
	return quality
}

func (r *EstimateReporter) hasExtensionCodec(typeID string) bool {
	registry := r.codec.Extensions
	if registry == nil {
		return false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.decoders[typeID] != nil
}

func validateEstimateExtensionPolicies(policies map[string]EstimateExtensionPolicy) error {
	for typeID, policy := range policies {
		if typeID == "" || (typeID == OpaqueStateExtensionType && policy.MetadataOnly) {
			return ErrInvalidEstimateReport
		}
		if err := policy.Codec.Validate(); err != nil {
			return err
		}
		if err := policy.Policy.Validate(); err != nil {
			return err
		}
	}
	return nil
}
