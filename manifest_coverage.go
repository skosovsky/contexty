package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
)

var ErrInvalidCoverage = errors.New("contexty: invalid manifest coverage")

const coverageNotSelected = "not_selected"
const manifestArtifactsSegment = "artifacts"

type CoverageStatus string

const (
	CoverageIncluded    CoverageStatus = "included"
	CoverageTransformed CoverageStatus = "transformed"
	CoverageSummarized  CoverageStatus = "summarized"
	CoverageExcluded    CoverageStatus = "excluded"
)

type ManifestCoverage struct {
	Kind       ManifestOutputKind `json:"kind"`
	OutputName string             `json:"output_name"`
	Segment    string             `json:"segment"`
	Input      *ContentRef        `json:"input,omitempty"`
	Status     CoverageStatus     `json:"status"`
	Outputs    []ContentRef       `json:"outputs"`
	Reason     string             `json:"reason"`
}

type ArtifactExclusion struct {
	Input  ContentRef `json:"input"`
	Reason string     `json:"reason"`
}

func manifestArtifactExclusions(ctx context.Context, result CompileResult) ([]ArtifactExclusion, error) {
	decisions, _ := ctx.Value(artifactExclusionsKey{}).(map[ContentRef]string)
	var exclusions []ArtifactExclusion
	seen := make(map[ContentRef]bool)
	for _, artifact := range compileArtifactEvidence(ctx, result.Source.Artifacts) {
		present, err := artifactRevisionPresent(result.Artifacts, artifact)
		if err != nil {
			return nil, err
		}
		if present {
			continue
		}
		ref, err := ArtifactContentRef(artifact)
		if err != nil {
			return nil, err
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		reason := decisions[ref]
		if reason == "" {
			reason = "artifact_merge"
		}
		exclusions = append(exclusions, ArtifactExclusion{Input: ref, Reason: reason})
	}
	return exclusions, nil
}

type coverageVisit struct {
	ref        ContentRef
	summarized bool
}
type coverageReach struct {
	output     ContentRef
	summarized bool
}

func manifestCoverage(manifest CompileManifest) []ManifestCoverage {
	var ledger []ManifestCoverage
	inputs := slices.Clone(manifest.Inputs)
	inputs = append(inputs, ManifestSegment{Name: "resolved", Messages: slices.Clone(manifest.ResolvedDependencies)})
	for _, output := range manifest.Outputs {
		reach := coverageAncestry(output, manifest.InheritedTransforms)
		for _, segment := range inputs {
			if len(segment.Messages) == 0 {
				ledger = append(
					ledger,
					ManifestCoverage{Kind: output.Kind, OutputName: output.Name, Segment: segment.Name,
						Input: nil, Status: CoverageExcluded, Outputs: nil, Reason: "empty_segment"},
				)
			}
			for _, ref := range segment.Messages {
				entry := coverageEntry(manifest, output, segment.Name, ref, reach)
				ledger = append(ledger, entry)
			}
		}
	}
	return ledger
}

func coverageEntry(manifest CompileManifest, output ManifestOutput, segment string, input ContentRef,
	reach map[ContentRef][]coverageReach,
) ManifestCoverage {
	entry := ManifestCoverage{Kind: output.Kind, OutputName: output.Name, Segment: segment,
		Input: cloneContentRef(&input), Status: CoverageExcluded, Outputs: nil, Reason: coverageNotSelected}
	if segment == "current/persistence" {
		entry.Reason = "persistence_only"
		return entry
	}
	anchors := coverageAnchors(output.Lineage, manifest.InheritedTransforms, segment, input)
	for _, anchor := range anchors {
		for _, path := range reach[anchor] {
			entry.Outputs = append(entry.Outputs, path.output)
			if path.summarized {
				entry.Status = CoverageSummarized
			}
		}
	}
	entry.Outputs = uniqueContentRefs(entry.Outputs)
	if len(entry.Outputs) > 0 {
		if entry.Status == CoverageSummarized {
			entry.Reason = traceStageSummarize
			return entry
		}
		entry.Status, entry.Reason = CoverageTransformed, "transform"
		for _, ref := range entry.Outputs {
			if baseContentRef(ref) == baseContentRef(input) {
				entry.Status, entry.Reason = CoverageIncluded, "retained"
			}
		}
		return entry
	}
	entry.Reason = coverageExclusionReason(manifest, output, segment, input)
	return entry
}

func coverageExclusionReason(manifest CompileManifest, output ManifestOutput, segment string, input ContentRef) string {
	if segment == manifestArtifactsSegment {
		for _, exclusion := range manifest.ExcludedArtifacts {
			if exclusion.Input == input {
				return exclusion.Reason
			}
		}
	}
	anchors := coverageAnchors(output.Lineage, manifest.InheritedTransforms, segment, input)
	if reason := coverageRemovalReason(output.Lineage, anchors); reason != "" {
		return reason
	}
	if output.Kind == ManifestTargetOutput && output.SourceSegment != "" &&
		isKnownSegment(SegmentName(segment)) && output.SourceSegment != SegmentName(segment) {
		return coverageNotSelected
	}
	chain := output.Transformations[input.ID]
	for _, step := range slices.Backward(chain) {
		if step.Action == ActionEvicted || step.Action == ActionTruncated ||
			step.Reason == ReasonReplacedByFormatter || step.Reason == ReasonReplacedByHook {
			return step.Reason
		}
	}
	return coverageNotSelected
}

// Follow actual content revisions, not the last transformation with the same ID.
// An in-place formatting event is not evidence that content was excluded.
func coverageRemovalReason(graph Lineage, anchors []ContentRef) string {
	reachable := make(map[ContentRef]bool)
	for _, anchor := range anchors {
		reachable[anchor] = true
	}
	for changed := true; changed; {
		changed = false
		for _, record := range graph.Records {
			if !slices.ContainsFunc(record.Inputs, func(ref ContentRef) bool { return reachable[ref] }) {
				continue
			}
			for _, output := range record.Outputs {
				if !reachable[output] {
					reachable[output], changed = true, true
				}
			}
		}
	}
	for _, record := range slices.Backward(graph.Records) {
		if len(record.Outputs) == 0 &&
			slices.ContainsFunc(record.Inputs, func(ref ContentRef) bool { return reachable[ref] }) {
			return coverageRemovalStageReason(record.Stage)
		}
	}
	return ""
}

func coverageRemovalStageReason(stage string) string {
	switch stage {
	case "budget":
		return ReasonTokenBudgetExceeded
	case "hook":
		return ReasonReplacedByHook
	case "format":
		return ReasonReplacedByFormatter
	case "merge":
		return "merge"
	case "project":
		return coverageNotSelected
	default:
		return stage
	}
}

func coverageAnchors(graph Lineage, inherited []string, segment string, input ContentRef) []ContentRef {
	if segment == "resolved" {
		return []ContentRef{input}
	}
	var anchors []ContentRef
	stage := traceStageSource
	if segment == "current/prompt" {
		stage = traceStagePromptTemplate
	}
	if segment == manifestArtifactsSegment {
		stage = traceStageArtifact
	}
	for _, record := range graph.Records {
		materialization := record.Stage == stage
		if stage == traceStageArtifact {
			materialization = materialization || record.Stage == "resource-materialize" ||
				record.Stage == resourceAppendMaterializeStage
		}
		if !materialization || slices.Contains(inherited, record.ID) {
			continue
		}
		candidates := record.Outputs
		if stage == traceStageArtifact {
			candidates = record.Inputs
		}
		for _, candidate := range candidates {
			if baseContentRef(candidate) == baseContentRef(input) {
				anchors = append(anchors, candidate)
			}
		}
	}
	return uniqueContentRefs(anchors)
}

func coverageAncestry(output ManifestOutput, inherited []string) map[ContentRef][]coverageReach {
	producers := make(map[ContentRef][]LineageRecord)
	for _, record := range output.Lineage.Records {
		for _, ref := range record.Outputs {
			producers[ref] = append(producers[ref], record)
		}
	}
	var finals []ContentRef
	for _, segment := range output.Segments {
		finals = append(finals, segment.Messages...)
	}
	if output.Rendered != nil {
		finals = append(finals, *output.Rendered)
	}
	reach := make(map[ContentRef][]coverageReach)
	for _, final := range finals {
		root := latestCoverageRef(output.Lineage, final)
		walkCoverage(reach, producers, inherited, final, root)
	}
	return reach
}

func latestCoverageRef(graph Lineage, final ContentRef) ContentRef {
	if final.Occurrence != "" {
		return final
	}
	for _, record := range slices.Backward(graph.Records) {
		for _, ref := range record.Outputs {
			if baseContentRef(ref) == final {
				return ref
			}
		}
	}
	return final
}

func walkCoverage(reach map[ContentRef][]coverageReach, producers map[ContentRef][]LineageRecord,
	inherited []string, final, root ContentRef,
) {
	queue := []coverageVisit{{ref: root, summarized: false}}
	seen := make(map[coverageVisit]struct{})
	for len(queue) > 0 {
		visit := queue[0]
		queue = queue[1:]
		if _, visited := seen[visit]; visited {
			continue
		}
		seen[visit] = struct{}{}
		reach[visit.ref] = append(reach[visit.ref], coverageReach{output: final, summarized: visit.summarized})
		for _, producer := range producers[visit.ref] {
			summary := visit.summarized ||
				(producer.Stage == traceStageSummarize && !slices.Contains(inherited, producer.ID))
			for _, input := range producer.Inputs {
				queue = append(queue, coverageVisit{ref: input, summarized: summary})
			}
		}
	}
}

func validateManifestCoverage(manifest CompileManifest) error {
	if err := validateArtifactExclusions(manifest); err != nil {
		return err
	}
	actual, err := json.Marshal(manifest.Coverage)
	if err != nil {
		return err
	}
	expected, err := json.Marshal(manifestCoverage(manifest))
	if err != nil {
		return err
	}
	if !slices.Equal(actual, expected) {
		return ErrInvalidCoverage
	}
	return nil
}

func validateArtifactExclusions(manifest CompileManifest) error {
	var inputs []ContentRef
	for _, segment := range manifest.Inputs {
		if segment.Name == manifestArtifactsSegment {
			inputs = segment.Messages
		}
	}
	inputs = append(inputs, manifestDerivedArtifactRefs(manifest)...)
	seen := make(map[ContentRef]bool)
	allowedReasons := []string{"artifact_merge", artifactInactiveReason, ReasonTokenBudgetExceeded}
	for _, excluded := range manifest.ExcludedArtifacts {
		if !slices.Contains(allowedReasons, excluded.Reason) ||
			!slices.Contains(inputs, excluded.Input) ||
			slices.Contains(manifest.Artifacts, excluded.Input) ||
			seen[excluded.Input] {
			return ErrInvalidCoverage
		}
		seen[excluded.Input] = true
		if err := excluded.Input.Validate(); err != nil {
			return err
		}
	}
	return nil
}
