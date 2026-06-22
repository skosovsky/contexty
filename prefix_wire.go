package contexty

import (
	"errors"
	"slices"
)

// ErrPrefixWireMismatch rejects stale, duplicate or malformed adapter confirmations.
var ErrPrefixWireMismatch = errors.New("contexty: prefix wire confirmation mismatch")

// PrefixWireConfirmation is an adapter assertion about its actual rendered bytes.
// Contexty checks identity binding, not remote cache hits, TTL, usage or prices.
type PrefixWireConfirmation struct {
	BoundaryID     string     `json:"boundary_id"`
	SemanticDigest string     `json:"semantic_digest"`
	Renderer       Descriptor `json:"renderer"`
	WireDigest     string     `json:"wire_digest"`
}

// WithPrefixWireConfirmation returns an owned report with a separate wire fact.
// Semantic digests are unchanged; a fresh diagnostic run has no confirmations.
func WithPrefixWireConfirmation(report PrefixDiagnosticReport,
	confirmation PrefixWireConfirmation,
) (PrefixDiagnosticReport, error) {
	if err := report.Manifest.Validate(); err != nil {
		return PrefixDiagnosticReport{}, err
	}
	seen := make(map[string]bool, len(report.WireConfirmations)+1)
	for _, item := range append(slices.Clone(report.WireConfirmations), confirmation) {
		if seen[item.BoundaryID] {
			return PrefixDiagnosticReport{}, ErrPrefixWireMismatch
		}
		seen[item.BoundaryID] = true
		if err := validatePrefixWireConfirmation(report.Manifest, item); err != nil {
			return PrefixDiagnosticReport{}, err
		}
	}
	result := clonePrefixDiagnosticReport(report)
	result.WireConfirmations = append(result.WireConfirmations, confirmation)
	return result, nil
}

func validatePrefixWireConfirmation(manifest PrefixManifest, confirmation PrefixWireConfirmation) error {
	ref := ContentRef{ID: confirmation.BoundaryID, Digest: confirmation.WireDigest, Occurrence: ""}
	if err := ref.Validate(); err != nil || confirmation.Renderer != manifest.Renderer {
		return ErrPrefixWireMismatch
	}
	for _, boundary := range manifest.Boundaries {
		if boundary.Boundary.ID == confirmation.BoundaryID {
			if boundary.Digest == confirmation.SemanticDigest {
				return nil
			}
			return ErrPrefixWireMismatch
		}
	}
	return ErrPrefixWireMismatch
}

func clonePrefixDiagnosticReport(report PrefixDiagnosticReport) PrefixDiagnosticReport {
	report.Manifest = clonePrefixManifest(report.Manifest)
	report.WireConfirmations = slices.Clone(report.WireConfirmations)
	report.Invalidations = slices.Clone(report.Invalidations)
	for i := range report.Invalidations {
		report.Invalidations[i].Reasons = slices.Clone(report.Invalidations[i].Reasons)
	}
	return report
}
