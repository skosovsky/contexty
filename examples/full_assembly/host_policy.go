package main

import (
	"context"
	"mime"
	"regexp"
	"strings"

	"github.com/skosovsky/contexty"
)

// The host treats retrieved content as user-provided evidence. A provider adapter
// can instead supply native media or application-defined ContentPart values.
func hostMaterialization() *contexty.ArtifactMaterializationPolicy {
	return &contexty.ArtifactMaterializationPolicy{
		Identity: contexty.Descriptor{ID: "host-evidence", Revision: "pinned"},
		Materialize: func(_ context.Context, artifact contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
			payload := artifact.Payload
			mediaType, _, parseErr := mime.ParseMediaType(payload.MIMEType)
			major, _, _ := strings.Cut(mediaType, "/")
			media := len(payload.Binary) > 0 ||
				(payload.MIMEType != "" && (parseErr != nil || (mediaType != "application/json" && major != "text")))
			if !media {
				return contexty.ArtifactRepresentation{
					Role:  contexty.RoleUser,
					Parts: []contexty.ContentPart{contexty.TextPart{Text: payload.PlainText()}},
				}, nil
			}
			data := payload.Binary
			if len(data) == 0 {
				data = payload.Data
				if len(data) == 0 {
					data = []byte(payload.Text)
				}
			}
			part := contexty.MediaPart{MIMEType: payload.MIMEType, Data: data}
			if err := part.Validate(); err != nil {
				return contexty.ArtifactRepresentation{}, err
			}
			parts := []contexty.ContentPart{part}
			if len(payload.Binary) > 0 && payload.Text != "" {
				parts = append([]contexty.ContentPart{contexty.TextPart{Text: payload.Text}}, parts...)
			}
			return contexty.ArtifactRepresentation{Role: contexty.RoleUser, Parts: parts}, nil
		},
	}
}

// This example host owns its email matching rule. It is a prompt projection,
// independent of authorization and of durable source/checkpoint state.
func hostEmailPolicy() *contexty.OutputPolicy {
	pattern := regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	return &contexty.OutputPolicy{Identity: contexty.Descriptor{ID: "host-email-projection", Revision: "pinned"},
		Project: func(ctx context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
			if err := ctx.Err(); err != nil {
				return contexty.AbstractPayload{}, err
			}
			payload := input.Payload
			for _, messages := range [][]contexty.Message{payload.System, payload.History, payload.Tools, payload.Memory} {
				for i := range messages {
					for j, part := range messages[i].Parts {
						if text, ok := part.(contexty.TextPart); ok {
							messages[i].Parts[j] = contexty.TextPart{
								Text: pattern.ReplaceAllString(text.Text, "[REDACTED]"),
							}
						}
					}
				}
			}
			return payload, nil
		}}
}
