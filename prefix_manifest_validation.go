package contexty

import (
	"fmt"
	"slices"
)

// Validate checks self identity and the cumulative ordered boundary contract.
func (m PrefixManifest) Validate() error {
	if err := validatePrefixConfiguration(m); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPrefixManifest, err)
	}
	if len(m.Boundaries) == 0 {
		return ErrInvalidPrefixManifest
	}
	seen := make(map[string]bool, len(m.Boundaries))
	var previous []PrefixMessage
	for _, boundary := range m.Boundaries {
		if err := validatePrefixBoundarySnapshot(boundary, previous, seen); err != nil {
			return err
		}
		digest, err := prefixBoundaryDigest(m, boundary)
		if err != nil || digest != boundary.Digest {
			return ErrInvalidPrefixManifest
		}
		previous = boundary.Messages
	}
	digest, err := prefixManifestDigest(m)
	if err != nil || digest != m.Digest {
		return ErrInvalidPrefixManifest
	}
	return nil
}

func validatePrefixBoundarySnapshot(boundary PrefixBoundarySnapshot, previous []PrefixMessage,
	seen map[string]bool,
) error {
	if boundary.Boundary.ID == "" || seen[boundary.Boundary.ID] || len(boundary.Messages) <= len(previous) {
		return ErrInvalidPrefixManifest
	}
	seen[boundary.Boundary.ID] = true
	if !slices.EqualFunc(previous, boundary.Messages[:len(previous)], prefixMessagesEqual) ||
		boundary.Messages[len(boundary.Messages)-1].Content.ID != boundary.Boundary.AfterMessageID {
		return ErrInvalidPrefixManifest
	}
	ids := make(map[string]bool, len(boundary.Messages))
	for _, msg := range boundary.Messages {
		if ids[msg.Content.ID] || msg.Content.Occurrence != "" {
			return ErrInvalidPrefixManifest
		}
		ids[msg.Content.ID] = true
		if err := msg.Content.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidPrefixManifest, err)
		}
		if err := validatePrefixEvidence(msg.Compaction, msg.Offload); err != nil {
			return err
		}
	}
	return nil
}

func prefixMessagesEqual(a, b PrefixMessage) bool {
	return a.Content == b.Content && a.Hint == b.Hint &&
		prefixRefsEqual(a.Compaction, b.Compaction) && prefixRefsEqual(a.Offload, b.Offload)
}

func prefixRefsEqual(a, b *ContentRef) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
