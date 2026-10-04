package contexty_test

import (
	"context"
	"mime"
	"regexp"
	"strings"

	"github.com/skosovsky/contexty"
)

// The test host explicitly chooses the historical fixture role. Production
// applications choose their own role and representation at the boundary.
func fixtureEngine(options ...contexty.EngineOption) *contexty.Engine {
	return contexty.NewEngine(
		append([]contexty.EngineOption{contexty.WithArtifactMaterialization(*fixtureMaterialization())}, options...)...)
}

func fixtureMaterialization() *contexty.ArtifactMaterializationPolicy {
	return &contexty.ArtifactMaterializationPolicy{
		Identity: contexty.Descriptor{ID: "fixture-materialization", Revision: "pinned"},
		Materialize: func(_ context.Context, artifact contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
			parts, err := fixturePayloadParts(artifact.Payload)
			return contexty.ArtifactRepresentation{Role: contexty.RoleSystem, Parts: parts}, err
		},
	}
}

func fixturePayloadParts(payload contexty.ToolPayload) ([]contexty.ContentPart, error) {
	mediaType, _, parseErr := mime.ParseMediaType(payload.MIMEType)
	major, _, _ := strings.Cut(mediaType, "/")
	media := len(payload.Binary) > 0 ||
		(payload.MIMEType != "" && (parseErr != nil || (mediaType != "application/json" && major != "text")))
	if !media {
		return []contexty.ContentPart{contexty.TextPart{Text: payload.PlainText()}}, nil
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
		return nil, err
	}
	parts := []contexty.ContentPart{part}
	if len(payload.Binary) > 0 && payload.Text != "" {
		parts = append([]contexty.ContentPart{contexty.TextPart{Text: payload.Text}}, parts...)
	}
	return parts, nil
}

// Fixture transformations exercise the generic hook port. Email recognition is
// an application policy, with no library-provided privacy or safety guarantee.
type fixtureTextTransform struct{ Replacer func(string) string }

func fixtureEmailTransform() fixtureTextTransform {
	pattern := regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	return fixtureTextTransform{
		Replacer: func(text string) string { return pattern.ReplaceAllString(text, "[REDACTED]") },
	}
}

func (hook fixtureTextTransform) Transform(
	ctx context.Context,
	snapshot contexty.ConversationSnapshot,
) (contexty.ConversationSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return contexty.ConversationSnapshot{}, err
	}
	next := snapshot
	for _, segment := range snapshot.SegmentNames() {
		messages := snapshot.Segment(segment)
		for i := range messages {
			messages[i] = messages[i].Clone()
			for j, part := range messages[i].Parts {
				if text, ok := part.(contexty.TextPart); ok {
					messages[i].Parts[j] = contexty.TextPart{Text: hook.Replacer(text.Text)}
				}
			}
		}
		next = next.WithSegment(segment, messages)
	}
	return next, nil
}
