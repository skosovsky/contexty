package contexty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

var ErrMissingRecordPolicy = errors.New("contexty: content capture requires a host policy")

type SavedContentKind string

const (
	SavedMessage  SavedContentKind = "message"
	SavedArtifact SavedContentKind = "artifact"
	SavedText     SavedContentKind = "text"
)

// SavedContent is immutable wire content. Its ref is metadata, not permission.
type SavedContent struct {
	Ref      ContentRef       `json:"ref"`
	Kind     SavedContentKind `json:"kind"`
	Encoding Descriptor       `json:"encoding"`
	Wire     json.RawMessage  `json:"wire"`
}

func (c SavedContent) clone() SavedContent {
	c.Wire = slices.Clone(c.Wire)
	return c
}

type CapturePurpose string

const (
	CaptureInput     CapturePurpose = "input"
	CaptureTransform CapturePurpose = "transform"
	CaptureOutput    CapturePurpose = "output"
)

// CaptureCandidate supplies actual bytes to host privacy/deletion policy.
// A policy must also classify derived content; contexty does not infer secrets.
type CaptureCandidate struct {
	Content SavedContent
	Purpose CapturePurpose
	Stage   string
}

type RecordContentPolicy interface {
	Keep(ctx context.Context, candidate CaptureCandidate) (bool, error)
}

type recordCaptureOptions struct {
	descriptor Descriptor
	policy     RecordContentPolicy
}

// WithCompileContentCapture opts into proposed saved results. It requires both
// manifest recording and a pinned host privacy policy. Storage stays with host.
func WithCompileContentCapture(privacy Descriptor, policy RecordContentPolicy) EngineOption {
	return func(e *Engine) {
		e.capture = &recordCaptureOptions{descriptor: privacy, policy: policy}
	}
}

type contentCaptureKey struct{}

type contentCapture struct {
	options  recordCaptureOptions
	codec    JSONSerializer
	encoding Descriptor
	kept     map[ContentRef]SavedContent
	denied   map[ContentRef]struct{}
}

func contentCaptureFrom(ctx context.Context) *contentCapture {
	value, _ := ctx.Value(contentCaptureKey{}).(*contentCapture)
	return value
}

func (e *Engine) startContentCapture(ctx context.Context, request CompileRequest) (context.Context, error) {
	if e.capture == nil {
		return ctx, nil
	}
	capture := &contentCapture{options: *e.capture, codec: e.trace.Codec, encoding: e.trace.Encoding,
		kept: make(map[ContentRef]SavedContent), denied: make(map[ContentRef]struct{})}
	ctx = context.WithValue(ctx, contentCaptureKey{}, capture)
	if err := capture.inputs(ctx, request); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func (c *contentCapture) inputs(ctx context.Context, request CompileRequest) error {
	messages := request.AllMessages()
	if request.CurrentTurn != nil {
		if prompt, present := request.CurrentTurn.promptMessage(); present {
			messages = append(messages, prompt)
		}
		if persisted, present := request.CurrentTurn.persistedMessage(); present {
			messages = append(messages, persisted)
		}
	}
	for _, message := range messages {
		if err := c.message(ctx, message, CaptureInput, ""); err != nil {
			return err
		}
	}
	for _, artifact := range request.Artifacts {
		if err := c.artifact(ctx, artifact, CaptureInput); err != nil {
			return err
		}
	}
	return nil
}

func (c *contentCapture) message(ctx context.Context, message Message, purpose CapturePurpose, stage string) error {
	ref, err := MessageContentRef(message, c.codec)
	if err != nil {
		return err
	}
	wire, err := c.codec.Marshal(message)
	if err != nil {
		return err
	}
	return c.keep(ctx, SavedContent{Ref: ref, Kind: SavedMessage, Encoding: c.encoding, Wire: wire}, purpose, stage)
}

func (c *contentCapture) artifact(ctx context.Context, artifact ContextArtifact, purpose CapturePurpose) error {
	return c.artifactAtStage(ctx, artifact, purpose, "")
}

func (c *contentCapture) artifactAtStage(
	ctx context.Context,
	artifact ContextArtifact,
	purpose CapturePurpose,
	stage string,
) error {
	ref, err := ArtifactContentRef(artifact)
	if err != nil {
		return err
	}
	wire, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	return c.keep(ctx, SavedContent{Ref: ref, Kind: SavedArtifact, Encoding: c.encoding, Wire: wire}, purpose, stage)
}

func (c *contentCapture) keep(ctx context.Context, content SavedContent, purpose CapturePurpose, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSavedContent(content, c.encoding); err != nil {
		return err
	}
	approved, err := c.options.policy.Keep(
		ctx,
		CaptureCandidate{Content: content.clone(), Purpose: purpose, Stage: stage},
	)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil {
		return err
	}
	ref := baseContentRef(content.Ref)
	if !approved {
		c.denied[ref] = struct{}{}
		delete(c.kept, ref)
		return nil
	}
	if _, denied := c.denied[ref]; !denied {
		c.kept[ref] = content.clone()
	}
	return nil
}

func (c *contentCapture) captureOutputs(
	ctx context.Context,
	manifest CompileManifest,
	result CompileResult,
) error {
	for _, message := range result.Payload.FlattenMessages() {
		if err := c.message(ctx, message, CaptureOutput, ""); err != nil {
			return err
		}
	}
	for _, output := range manifest.Outputs {
		if output.Kind == ManifestMainOutput {
			continue
		}
		if err := c.projection(ctx, result.Projections[output.Name]); err != nil {
			return err
		}
	}
	for _, artifact := range result.Artifacts {
		if err := c.artifact(ctx, artifact, CaptureOutput); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (c *contentCapture) finish(manifest CompileManifest) (SavedCompileRecord, error) {
	copyManifest, err := manifest.Clone()
	if err != nil {
		return SavedCompileRecord{}, err
	}
	record := SavedCompileRecord{
		Manifest:    copyManifest,
		State:       RecordProposed,
		DecisionRef: "",
		Content:     nil,
		Omitted:     nil,
	}
	for _, content := range c.kept {
		record.Content = append(record.Content, content.clone())
	}
	for ref := range c.denied {
		record.Omitted = append(record.Omitted, ref)
	}
	slices.SortFunc(record.Content, func(a, b SavedContent) int { return compareContentRefs(a.Ref, b.Ref) })
	slices.SortFunc(record.Omitted, compareContentRefs)
	return record, record.Validate()
}

func (c *contentCapture) projection(ctx context.Context, projection CompileProjection) error {
	for _, artifact := range projection.Artifacts {
		if err := c.artifact(ctx, artifact, CaptureOutput); err != nil {
			return err
		}
	}
	for _, message := range projection.Messages {
		if err := c.message(ctx, message, CaptureOutput, ""); err != nil {
			return err
		}
	}
	if projection.Rendered != nil {
		if err := c.message(ctx, projection.Rendered.Message, CaptureOutput, "render"); err != nil {
			return err
		}
	}
	ref, err := renderedTextRef(projection.Name, projection.Text)
	if err != nil {
		return err
	}
	wire, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: projection.Text})
	if err != nil {
		return err
	}
	return c.keep(
		ctx,
		SavedContent{Ref: ref, Kind: SavedText, Encoding: c.encoding, Wire: wire},
		CaptureOutput,
		"render",
	)
}

func compareContentRefs(a, b ContentRef) int {
	if a.ID < b.ID {
		return -1
	}
	if a.ID > b.ID {
		return 1
	}
	if a.Digest < b.Digest {
		return -1
	}
	if a.Digest > b.Digest {
		return 1
	}
	return 0
}

func validateSavedContent(content SavedContent, encoding Descriptor) error {
	if err := content.Ref.Validate(); err != nil {
		return err
	}
	if content.Encoding != encoding {
		return ErrReplayMismatch
	}
	digest, err := canonicalJSONDigest(content.Wire)
	if err != nil || digest != content.Ref.Digest {
		return ErrReplayContentMismatch
	}
	switch content.Kind {
	case SavedMessage, SavedArtifact:
		var identity struct {
			ID string `json:"id"`
		}
		if decodeErr := json.Unmarshal(content.Wire, &identity); decodeErr != nil || identity.ID != content.Ref.ID {
			return ErrReplayContentMismatch
		}
	case SavedText:
		var text struct {
			Text *string `json:"text"`
		}
		if decodeErr := json.Unmarshal(content.Wire, &text); decodeErr != nil || text.Text == nil {
			return ErrReplayContentMismatch
		}
	default:
		return fmt.Errorf("%w: content kind %s", ErrUnsupportedReplay, content.Kind)
	}
	return nil
}
