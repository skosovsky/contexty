package contexty

import (
	"context"
	"encoding/json"
)

// Tail identity is separate from every prefix boundary and never includes raw
// payload in the diagnostic report. It uses the same pinned semantic contracts.
type prefixTailIdentity struct {
	Renderer       Descriptor      `json:"renderer"`
	Encoding       Descriptor      `json:"encoding"`
	Policy         Descriptor      `json:"policy"`
	SupportedHints []string        `json:"supported_hints"`
	RequiredHints  []string        `json:"required_hints"`
	Messages       []PrefixMessage `json:"messages"`
}

func admittedPrefixTailDigest(ctx context.Context, manifest PrefixManifest, messages []Message,
	recipe PrefixRecipe,
) (string, error) {
	start := len(manifest.Boundaries[len(manifest.Boundaries)-1].Messages)
	tail, err := authorizePrefixMessages(ctx, messages[start:], recipe.Authorize, recipe.Codec, nil)
	if err != nil {
		return "", err
	}
	identity := prefixTailIdentity{
		Renderer: manifest.Renderer, Encoding: manifest.Encoding, Policy: manifest.Policy,
		SupportedHints: manifest.SupportedHints, RequiredHints: manifest.RequiredHints, Messages: tail,
	}
	wire, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return canonicalJSONDigest(wire)
}
