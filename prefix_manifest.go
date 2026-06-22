package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrInvalidPrefixBoundary  = errors.New("contexty: invalid prefix boundary")
	ErrInvalidPrefixManifest  = errors.New("contexty: invalid prefix manifest")
	ErrPrefixAdmission        = errors.New("contexty: prefix admission failed")
	ErrPrefixRequiredHint     = errors.New("contexty: unsupported required prefix hint")
	ErrPrefixRendererMismatch = errors.New("contexty: incompatible prefix renderer")
)

// PrefixBoundary terminates after an exact message ID in the supplied wire order.
type PrefixBoundary struct {
	ID             string `json:"id"`
	AfterMessageID string `json:"after_message_id"`
}

// PrefixProjectionEvidence pins host-owned compaction/offload projection facts.
// Neither reference grants access to raw content or a storage object.
type PrefixProjectionEvidence struct {
	MessageID  string      `json:"message_id"`
	Compaction *ContentRef `json:"compaction,omitempty"`
	Offload    *ContentRef `json:"offload,omitempty"`
}

// PrefixRecipe is explicit configuration, not serialized remote cache state.
// Authorize must check current freshness, budget and host privacy/trust policy
// for every hashed message: prefix in BuildPrefixManifest, prefix and tail in
// DiagnosePrefix. It must not interpret a cache hint as permission.
// Encoding identifies the exact caller-pinned serializer and registry behavior.
type PrefixRecipe struct {
	Renderer       Descriptor
	Encoding       Descriptor
	Policy         Descriptor
	Codec          JSONSerializer
	Boundaries     []PrefixBoundary
	SupportedHints []string
	RequiredHints  []string
	Evidence       []PrefixProjectionEvidence
	Authorize      func(context.Context, Message) error
}

// PrefixMessage records full typed identity and optional specific change evidence.
type PrefixMessage struct {
	Content    ContentRef  `json:"content"`
	Hint       string      `json:"hint,omitempty"`
	Compaction *ContentRef `json:"compaction,omitempty"`
	Offload    *ContentRef `json:"offload,omitempty"`
}

// PrefixBoundarySnapshot is a cumulative ordered semantic prefix, without payload.
type PrefixBoundarySnapshot struct {
	Boundary PrefixBoundary  `json:"boundary"`
	Messages []PrefixMessage `json:"messages"`
	Digest   string          `json:"digest"`
}

// PrefixManifest contains semantic metadata only, never remote cache guarantees.
type PrefixManifest struct {
	Renderer       Descriptor               `json:"renderer"`
	Encoding       Descriptor               `json:"encoding"`
	Policy         Descriptor               `json:"policy"`
	SupportedHints []string                 `json:"supported_hints"`
	RequiredHints  []string                 `json:"required_hints"`
	Boundaries     []PrefixBoundarySnapshot `json:"boundaries"`
	Digest         string                   `json:"digest"`
}

// BuildPrefixManifest authorizes and hashes the final prefix without changing it.
// It never reads remote data, reorders messages or treats a digest as permission.
func BuildPrefixManifest(ctx context.Context, messages []Message, recipe PrefixRecipe) (PrefixManifest, error) {
	if err := ctx.Err(); err != nil {
		return PrefixManifest{}, err
	}
	manifest, positions, evidence, err := preparePrefixManifest(messages, recipe)
	if err != nil {
		return PrefixManifest{}, err
	}
	codec := snapshotJSONSerializer(recipe.Codec)
	prefix := cloneMessageSlice(messages[:positions[len(positions)-1]+1])
	refs, err := authorizePrefixMessages(ctx, prefix, recipe.Authorize, codec, evidence)
	if err != nil {
		return PrefixManifest{}, err
	}
	for i, position := range positions {
		boundary := &manifest.Boundaries[i]
		boundary.Messages = clonePrefixMessages(refs[:position+1])
		boundary.Digest, err = prefixBoundaryDigest(manifest, *boundary)
		if err != nil {
			return PrefixManifest{}, err
		}
	}
	manifest.Digest, err = prefixManifestDigest(manifest)
	if err != nil {
		return PrefixManifest{}, err
	}
	if err = ctx.Err(); err != nil {
		return PrefixManifest{}, err
	}
	return manifest, nil
}

func preparePrefixManifest(messages []Message, recipe PrefixRecipe) (PrefixManifest, []int,
	map[string]PrefixProjectionEvidence, error,
) {
	manifest := PrefixManifest{
		Renderer: recipe.Renderer, Encoding: recipe.Encoding, Policy: recipe.Policy,
		SupportedHints: canonicalLabelTypes(recipe.SupportedHints),
		RequiredHints:  canonicalLabelTypes(recipe.RequiredHints), Boundaries: nil, Digest: "",
	}
	if err := validatePrefixConfiguration(manifest); err != nil {
		return PrefixManifest{}, nil, nil, err
	}
	if recipe.Authorize == nil {
		return PrefixManifest{}, nil, nil, ErrPrefixAdmission
	}
	positions, err := prefixBoundaryPositions(messages, recipe.Boundaries)
	if err != nil {
		return PrefixManifest{}, nil, nil, err
	}
	evidence, err := prefixEvidenceMap(recipe.Evidence, messages[:positions[len(positions)-1]+1])
	if err != nil {
		return PrefixManifest{}, nil, nil, err
	}
	for _, boundary := range recipe.Boundaries {
		manifest.Boundaries = append(manifest.Boundaries, PrefixBoundarySnapshot{
			Boundary: boundary, Messages: nil, Digest: "",
		})
	}
	return manifest, positions, evidence, nil
}

func validatePrefixConfiguration(manifest PrefixManifest) error {
	for _, descriptor := range []Descriptor{manifest.Renderer, manifest.Encoding, manifest.Policy} {
		if err := descriptor.Validate(); err != nil {
			return err
		}
	}
	for _, list := range [][]string{manifest.SupportedHints, manifest.RequiredHints} {
		if !slices.Equal(list, canonicalLabelTypes(list)) {
			return ErrInvalidPrefixManifest
		}
		for _, hint := range list {
			if hint == "" || hint != strings.TrimSpace(hint) {
				return ErrInvalidPrefixManifest
			}
		}
	}
	for _, required := range manifest.RequiredHints {
		if !slices.Contains(manifest.SupportedHints, required) {
			return ErrPrefixRequiredHint
		}
	}
	return nil
}

func prefixBoundaryPositions(messages []Message, boundaries []PrefixBoundary) ([]int, error) {
	if len(boundaries) == 0 {
		return nil, ErrInvalidPrefixBoundary
	}
	ids := make(map[string]int, len(messages))
	for i, msg := range messages {
		if _, duplicate := ids[msg.ID]; duplicate || msg.ID == "" {
			return nil, ErrInvalidPrefixBoundary
		}
		ids[msg.ID] = i
	}
	seen := make(map[string]bool, len(boundaries))
	positions := make([]int, 0, len(boundaries))
	previous := -1
	for _, boundary := range boundaries {
		position, exists := ids[boundary.AfterMessageID]
		if !exists || position <= previous || boundary.ID == "" || seen[boundary.ID] {
			return nil, ErrInvalidPrefixBoundary
		}
		seen[boundary.ID] = true
		positions = append(positions, position)
		previous = position
	}
	return positions, nil
}

func prefixEvidenceMap(evidence []PrefixProjectionEvidence, messages []Message) (
	map[string]PrefixProjectionEvidence, error,
) {
	ids := make(map[string]bool, len(messages))
	for _, msg := range messages {
		ids[msg.ID] = true
	}
	result := make(map[string]PrefixProjectionEvidence, len(evidence))
	for _, item := range evidence {
		if _, duplicate := result[item.MessageID]; duplicate || !ids[item.MessageID] ||
			(item.Compaction == nil && item.Offload == nil) {
			return nil, ErrInvalidPrefixManifest
		}
		if err := validatePrefixEvidence(item.Compaction, item.Offload); err != nil {
			return nil, err
		}
		item.Compaction = clonePrefixRef(item.Compaction)
		item.Offload = clonePrefixRef(item.Offload)
		result[item.MessageID] = item
	}
	return result, nil
}

func authorizePrefixMessages(ctx context.Context, messages []Message, authorize func(context.Context, Message) error,
	codec JSONSerializer, evidence map[string]PrefixProjectionEvidence,
) ([]PrefixMessage, error) {
	result := make([]PrefixMessage, 0, len(messages))
	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := authorize(ctx, msg.Clone())
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrPrefixAdmission, err)
		}
		ref, err := MessageContentRef(msg, codec)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, err
		}
		projection := evidence[msg.ID]
		item := PrefixMessage{Content: ref, Hint: "", Compaction: projection.Compaction, Offload: projection.Offload}
		if msg.LLMCache != nil {
			item.Hint = msg.LLMCache.Type
		}
		result = append(result, item)
	}
	return result, nil
}

func validatePrefixEvidence(refs ...*ContentRef) error {
	for _, ref := range refs {
		if ref != nil {
			if err := ref.Validate(); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidPrefixManifest, err)
			}
		}
	}
	return nil
}

func clonePrefixRef(ref *ContentRef) *ContentRef {
	if ref == nil {
		return nil
	}
	copyRef := *ref
	return &copyRef
}

func clonePrefixMessages(messages []PrefixMessage) []PrefixMessage {
	result := slices.Clone(messages)
	for i := range result {
		result[i].Compaction = clonePrefixRef(result[i].Compaction)
		result[i].Offload = clonePrefixRef(result[i].Offload)
	}
	return result
}

func prefixBoundaryDigest(manifest PrefixManifest, boundary PrefixBoundarySnapshot) (string, error) {
	manifest.Digest = ""
	boundary.Digest = ""
	manifest.Boundaries = []PrefixBoundarySnapshot{boundary}
	return prefixManifestDigest(manifest)
}

func prefixManifestDigest(manifest PrefixManifest) (string, error) {
	manifest.Digest = ""
	wire, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return canonicalJSONDigest(wire)
}
