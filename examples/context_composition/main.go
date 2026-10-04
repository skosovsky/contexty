// Offline composition of explicit offload, rolling summary and bounded restore.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/skosovsky/contexty"
)

const scope = "host-approved"

func main() {
	report, err := runComposition(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	wire, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(wire))
}

type compositionReport struct {
	OriginalBytes    int          `json:"original_bytes"`
	Preview          string       `json:"preview"`
	Summary          string       `json:"summary"`
	RecentIDs        []string     `json:"recent_ids"`
	RestoredOriginal bool         `json:"restored_original"`
	DeniedRead       bool         `json:"denied_read"`
	BoundedRead      bool         `json:"bounded_read"`
	StaleRead        bool         `json:"stale_read"`
	ClaimProtected   bool         `json:"claim_protected"`
	Collected        bool         `json:"collected"`
	FactReplacement  factDecision `json:"fact_replacement"`
	ProviderUsage    string       `json:"provider_usage"`
	Durability       string       `json:"durability"`
}

type previewer struct{}

func (previewer) PreviewBlob(_ context.Context, _ contexty.BlobContent) (contexty.BlobContent, error) {
	// Host preview deliberately omits the detail; restore is necessary to answer it.
	return contexty.BlobContent{
		MIMEType: "text/plain",
		Bytes:    []byte("Evidence archived; restore for original detail."),
	}, nil
}

type payloadDecoder struct{}

func (payloadDecoder) DecodeBlob(_ context.Context, content contexty.BlobContent) ([]contexty.Message, error) {
	var payload contexty.ToolPayload
	if err := json.Unmarshal(content.Bytes, &payload); err != nil {
		return nil, err
	}
	message := contexty.TextMessage(contexty.RoleUser, payload.PlainText())
	message.ID = "restored-evidence"
	message.SourceRefs = []contexty.SourceRef{evidenceSource()}
	return []contexty.Message{message}, nil
}

type fixtureSummary struct{ calls int }

func (s *fixtureSummary) Summarize(_ context.Context, request contexty.SummaryRequest) (contexty.Message, error) {
	s.calls++
	// Deterministic fixture copies an explicit host fact from the compacted prefix.
	// This validates mechanics, not the quality of any model's summary.
	var sources []contexty.SourceRef
	for _, message := range request.Messages {
		if strings.Contains(message.TextContent(), "constraint: do not disclose credentials") {
			sources = append(sources, message.SourceRefs...)
		}
	}
	if len(sources) == 0 {
		return contexty.Message{}, errors.New("required constraint absent from summary inputs")
	}
	message := contexty.TextMessage(contexty.RoleUser, "constraint: do not disclose credentials; evidence archived")
	message.ID = "summary-v1"
	message.SourceRefs = sources
	return message, nil
}

func evidenceSource() contexty.SourceRef {
	return contexty.SourceRef{
		Namespace:    "host-journal",
		Kind:         "completed-tool-evidence",
		ID:           "lookup-result",
		CheckpointID: "lookup-result:v1",
		URI:          "",
	}
}

func hostMaterialization() contexty.ArtifactMaterializationPolicy {
	return contexty.ArtifactMaterializationPolicy{
		Identity: contexty.Descriptor{ID: "host-evidence", Revision: "v1"},
		Materialize: func(_ context.Context, artifact contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
			parts, err := contexty.ArtifactContentParts(artifact)
			return contexty.ArtifactRepresentation{Role: contexty.RoleUser, Parts: parts}, err
		},
	}
}
