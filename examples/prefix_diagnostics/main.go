// Local prefix diagnostics: compile first, check host policy, render actual wire
// bytes and attach a separate confirmation. No remote cache operation is made.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/skosovsky/contexty"
)

const (
	inputLimit = 256
	pinned     = "pinned"
	localHint  = "local-boundary"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	request := contexty.CompileRequest{
		System: []contexty.Message{
			message("policy", "Static host policy."),
			message("reference", "Current approved reference."),
		},
		History: []contexty.Message{message("turn", "Variable question.")},
	}
	result, err := contexty.NewEngine().CompileSnapshot(ctx, request)
	if err != nil {
		return err
	}
	messages := result.Payload.FlattenMessages()
	// Final whole-output admission happens before diagnostics. This is a local
	// semantic estimate, not a provider token count or remote cache guarantee.
	tokens, err := (contexty.CharTokenEstimator{}).Estimate(ctx, messages)
	if err != nil {
		return err
	}
	if tokens > inputLimit {
		return contexty.ErrBudgetExceeded
	}
	report, err := contexty.DiagnosePrefix(ctx, messages, hostRecipe(), nil)
	if err != nil {
		return err
	}
	wire, offsets, err := renderWire(ctx, messages)
	if err != nil {
		return err
	}
	boundary := report.Manifest.Boundaries[0]
	// Hash a literal prefix of the actual complete request, not an independently
	// rendered array with a different closing delimiter.
	digest := sha256.Sum256(wire[:offsets[boundary.Boundary.AfterMessageID]])
	confirmed, err := contexty.WithPrefixWireConfirmation(report, contexty.PrefixWireConfirmation{
		BoundaryID: boundary.Boundary.ID, SemanticDigest: boundary.Digest,
		Renderer: report.Manifest.Renderer, WireDigest: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return err
	}
	fmt.Printf("Semantic prefix: %s\n", confirmed.Manifest.Boundaries[0].Digest)
	fmt.Printf("Adapter wire prefix: %s\n", confirmed.WireConfirmations[0].WireDigest)
	fmt.Println("Semantic variable tail:", confirmed.TailDigest)
	fmt.Println("Remote cache hit, TTL and savings: not asserted")
	return nil
}

func message(id, text string) contexty.Message {
	msg := contexty.TextMessage(contexty.RoleSystem, text)
	msg.ID = id
	if id == "turn" {
		msg.Role = contexty.RoleUser
	} else {
		msg.LLMCache = &contexty.CachePolicyRef{Type: localHint}
	}
	return msg
}

func hostRecipe() contexty.PrefixRecipe {
	// This example has static local data. A real host checks its current source
	// revisions and authorization here; roles never substitute for permission.
	approved := map[string]bool{"policy": true, "reference": true, "turn": true}
	return contexty.PrefixRecipe{
		Renderer:       contexty.Descriptor{ID: "host-ndjson", Revision: pinned},
		Encoding:       contexty.Descriptor{ID: "host-semantic-codec", Revision: pinned},
		Policy:         contexty.Descriptor{ID: "host-current-admission", Revision: pinned},
		Codec:          contexty.DefaultJSONSerializer(),
		Boundaries:     []contexty.PrefixBoundary{{ID: "static-context", AfterMessageID: "reference"}},
		SupportedHints: []string{localHint}, RequiredHints: []string{localHint}, Evidence: nil,
		Authorize: func(ctx context.Context, msg contexty.Message) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !approved[msg.ID] {
				return contexty.ErrPrefixAdmission
			}
			return nil
		},
	}
}

func renderWire(ctx context.Context, messages []contexty.Message) ([]byte, map[string]int, error) {
	codec := contexty.DefaultJSONSerializer()
	var wire []byte
	offsets := make(map[string]int, len(messages))
	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		// This local adapter actually supports just this hint. Core's capability
		// declaration does not prove support in an arbitrary renderer/provider.
		if msg.LLMCache != nil && msg.LLMCache.Type != localHint {
			return nil, nil, contexty.ErrPrefixRequiredHint
		}
		encoded, err := codec.Marshal(msg)
		if err != nil {
			return nil, nil, err
		}
		wire = append(wire, encoded...)
		wire = append(wire, '\n')
		offsets[msg.ID] = len(wire)
	}
	return wire, offsets, nil
}
