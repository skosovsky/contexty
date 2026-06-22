package contexty

import (
	"context"
	"slices"
)

// PrefixInvalidationReason classifies a semantic change, not a remote cache miss.
type PrefixInvalidationReason string

const (
	PrefixContentChanged    PrefixInvalidationReason = "content"
	PrefixOrderChanged      PrefixInvalidationReason = "order"
	PrefixPolicyChanged     PrefixInvalidationReason = "policy"
	PrefixCodecChanged      PrefixInvalidationReason = "codec"
	PrefixCompactionChanged PrefixInvalidationReason = "compaction"
	PrefixOffloadChanged    PrefixInvalidationReason = "offload"
)

// PrefixInvalidation identifies one affected or removed boundary.
type PrefixInvalidation struct {
	BoundaryID     string                     `json:"boundary_id"`
	PreviousDigest string                     `json:"previous_digest,omitempty"`
	CurrentDigest  string                     `json:"current_digest,omitempty"`
	Removed        bool                       `json:"removed"`
	Reasons        []PrefixInvalidationReason `json:"reasons"`
}

// PrefixDiagnosticReport separates semantic comparison from adapter wire facts.
// Compared with no invalidations never implies cache availability or permission.
type PrefixDiagnosticReport struct {
	Manifest              PrefixManifest           `json:"manifest"`
	TailDigest            string                   `json:"tail_digest"`
	Compared              bool                     `json:"compared"`
	FirstAffectedBoundary string                   `json:"first_affected_boundary,omitempty"`
	Invalidations         []PrefixInvalidation     `json:"invalidations"`
	WireConfirmations     []PrefixWireConfirmation `json:"wire_confirmations"`
}

// DiagnosePrefix freshly admits current content and compares optional prior metadata.
// A previous manifest is validated and owned before any host admission callback.
func DiagnosePrefix(ctx context.Context, messages []Message, recipe PrefixRecipe,
	previous *PrefixManifest,
) (PrefixDiagnosticReport, error) {
	if err := ctx.Err(); err != nil {
		return PrefixDiagnosticReport{}, err
	}
	var prior *PrefixManifest
	if previous != nil {
		if err := previous.Validate(); err != nil {
			return PrefixDiagnosticReport{}, err
		}
		if previous.Renderer != recipe.Renderer {
			return PrefixDiagnosticReport{}, ErrPrefixRendererMismatch
		}
		copyPrevious := clonePrefixManifest(*previous)
		prior = &copyPrevious
	}
	// Own the entire ordered request and encoding before any host callback. Tail
	// identity must not observe mutations made while admitting the prefix.
	currentMessages := cloneMessageSlice(messages)
	recipe.Codec = snapshotJSONSerializer(recipe.Codec)
	manifest, err := BuildPrefixManifest(ctx, currentMessages, recipe)
	if err != nil {
		return PrefixDiagnosticReport{}, err
	}
	tailDigest, err := admittedPrefixTailDigest(ctx, manifest, currentMessages, recipe)
	if err != nil {
		return PrefixDiagnosticReport{}, err
	}
	report := PrefixDiagnosticReport{Manifest: manifest, Compared: prior != nil,
		TailDigest: tailDigest, FirstAffectedBoundary: "", Invalidations: nil, WireConfirmations: nil}
	if prior != nil {
		comparePrefixManifests(&report, *prior)
	}
	if err := ctx.Err(); err != nil {
		return PrefixDiagnosticReport{}, err
	}
	return report, nil
}

func clonePrefixManifest(manifest PrefixManifest) PrefixManifest {
	manifest.SupportedHints = slices.Clone(manifest.SupportedHints)
	manifest.RequiredHints = slices.Clone(manifest.RequiredHints)
	manifest.Boundaries = slices.Clone(manifest.Boundaries)
	for i := range manifest.Boundaries {
		manifest.Boundaries[i].Messages = clonePrefixMessages(manifest.Boundaries[i].Messages)
	}
	return manifest
}

func comparePrefixManifests(report *PrefixDiagnosticReport, previous PrefixManifest) {
	old := make(map[string]PrefixBoundarySnapshot, len(previous.Boundaries))
	for _, boundary := range previous.Boundaries {
		old[boundary.Boundary.ID] = boundary
	}
	firstPosition := 0
	for _, current := range report.Manifest.Boundaries {
		prior, exists := old[current.Boundary.ID]
		delete(old, current.Boundary.ID)
		if exists && current.Digest == prior.Digest {
			continue
		}
		reasons := []PrefixInvalidationReason{PrefixOrderChanged}
		if exists {
			reasons = prefixInvalidationReasons(report.Manifest, previous, current, prior)
		}
		invalidation := PrefixInvalidation{BoundaryID: current.Boundary.ID,
			PreviousDigest: prior.Digest, CurrentDigest: current.Digest, Removed: false, Reasons: reasons}
		report.Invalidations = append(report.Invalidations, invalidation)
		selectFirstPrefixBoundary(report, &firstPosition, len(current.Messages), invalidation)
	}
	for _, prior := range previous.Boundaries {
		if _, removed := old[prior.Boundary.ID]; !removed {
			continue
		}
		invalidation := PrefixInvalidation{BoundaryID: prior.Boundary.ID,
			PreviousDigest: prior.Digest, CurrentDigest: "", Removed: true,
			Reasons: []PrefixInvalidationReason{PrefixOrderChanged}}
		report.Invalidations = append(report.Invalidations, invalidation)
		selectFirstPrefixBoundary(report, &firstPosition, len(prior.Messages), invalidation)
	}
}

func selectFirstPrefixBoundary(report *PrefixDiagnosticReport, position *int, candidate int,
	invalidation PrefixInvalidation,
) {
	if *position == 0 || candidate < *position || (candidate == *position && invalidation.Removed) {
		*position = candidate
		report.FirstAffectedBoundary = invalidation.BoundaryID
	}
}

type prefixChanges struct {
	content    bool
	order      bool
	policy     bool
	codec      bool
	compaction bool
	offload    bool
}

func prefixInvalidationReasons(
	current, previous PrefixManifest,
	boundary, prior PrefixBoundarySnapshot,
) []PrefixInvalidationReason {
	changes := prefixChanges{
		content: false,
		order:   boundary.Boundary != prior.Boundary || !prefixMessageOrderEqual(boundary.Messages, prior.Messages),
		policy: current.Policy != previous.Policy || !slices.Equal(current.SupportedHints, previous.SupportedHints) ||
			!slices.Equal(current.RequiredHints, previous.RequiredHints),
		codec:      current.Encoding != previous.Encoding,
		compaction: false,
		offload:    false,
	}
	old := make(map[string]PrefixMessage, len(prior.Messages))
	for _, msg := range prior.Messages {
		old[msg.Content.ID] = msg
	}
	for _, msg := range boundary.Messages {
		before := old[msg.Content.ID]
		changes.compareMessage(msg, before)
		delete(old, msg.Content.ID)
	}
	for _, msg := range old {
		var absent PrefixMessage
		changes.compareMessage(absent, msg)
	}
	return changes.reasons()
}

func prefixMessageOrderEqual(a, b []PrefixMessage) bool {
	return slices.EqualFunc(a, b, func(x, y PrefixMessage) bool { return x.Content.ID == y.Content.ID })
}

func (c *prefixChanges) compareMessage(current, previous PrefixMessage) {
	c.content = c.content || current.Content != previous.Content
	c.policy = c.policy || current.Hint != previous.Hint
	c.compaction = c.compaction || !prefixRefsEqual(current.Compaction, previous.Compaction)
	c.offload = c.offload || !prefixRefsEqual(current.Offload, previous.Offload)
}

func (c *prefixChanges) reasons() []PrefixInvalidationReason {
	const reasonCount = 6
	result := make([]PrefixInvalidationReason, 0, reasonCount)
	for _, item := range []struct {
		changed bool
		reason  PrefixInvalidationReason
	}{
		{changed: c.content, reason: PrefixContentChanged},
		{changed: c.order, reason: PrefixOrderChanged},
		{changed: c.policy, reason: PrefixPolicyChanged},
		{changed: c.codec, reason: PrefixCodecChanged},
		{changed: c.compaction, reason: PrefixCompactionChanged},
		{changed: c.offload, reason: PrefixOffloadChanged},
	} {
		if item.changed {
			result = append(result, item.reason)
		}
	}
	return result
}
