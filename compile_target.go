package contexty

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// CompileTarget requests an additional named projection from the same compile pass.
type CompileTarget struct {
	Name string
	// View requests a built-in rendered view and is mutually exclusive with
	// SourceSegment, Budget, and Formatter.
	View          string
	SourceSegment SegmentName
	Budget        *BudgetPipeline
	Formatter     SegmentFormatter
}

// CompileProjection is a named compile output with traceability to the shared pass.
type CompileProjection struct {
	Name            string
	Text            string
	Messages        []Message
	Transformations map[string]TransformChain
	Source          CompileRequest       // normalized request before pipeline mutations
	InputSnapshot   ConversationSnapshot // compiled snapshot used as target input
	ArtifactIDs     []string
	Lineage         Lineage
	Rendered        *RenderedOutput
}

// RenderedOutput is the typed content/metadata counterpart of a text view.
// Its role is a representation detail, not a trust or permission decision.
type RenderedOutput struct {
	Message  Message
	Ref      ContentRef
	Renderer Descriptor
}

func (r *RenderedOutput) clone() *RenderedOutput {
	if r == nil {
		return nil
	}
	return &RenderedOutput{Message: r.Message.Clone(), Ref: r.Ref, Renderer: r.Renderer}
}

func (p CompileProjection) clone() CompileProjection {
	return CompileProjection{
		Name:            p.Name,
		Text:            p.Text,
		Messages:        cloneMessageSlice(p.Messages),
		Transformations: cloneTransformRecords(p.Transformations),
		Source:          p.Source.Freeze(),
		InputSnapshot:   p.InputSnapshot.AllSegmentsSnapshot(),
		ArtifactIDs:     append([]string(nil), p.ArtifactIDs...),
		Lineage:         p.Lineage.Clone(),
		Rendered:        p.Rendered.clone(),
	}
}

func cloneCompileProjections(in map[string]CompileProjection) map[string]CompileProjection {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]CompileProjection, len(in))
	for k, v := range in {
		out[k] = v.clone()
	}
	return out
}

func cloneTransformRecords(in map[string]TransformChain) map[string]TransformChain {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]TransformChain, len(in))
	for id, chain := range in {
		out[id] = append(TransformChain(nil), chain...)
	}
	return out
}

func validateCompileTargets(targets []CompileTarget) error {
	if len(targets) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		name := strings.TrimSpace(target.Name)
		if name == "" {
			return errors.New("contexty: compile target name is empty")
		}
		if _, ok := seen[name]; ok {
			return ErrDuplicateCompileTarget
		}
		if target.View != "" {
			if _, ok := builtinViewFormatter(target.View); !ok {
				return fmt.Errorf("%w: %s", ErrUnknownCompileTargetView, target.View)
			}
			if target.SourceSegment != "" || target.Budget != nil || target.Formatter != nil {
				return fmt.Errorf("%w: target %q view is mutually exclusive", ErrConflictingCompileTargetFields, name)
			}
		}
		if target.SourceSegment != "" && !isKnownSegment(target.SourceSegment) {
			return fmt.Errorf("%w: %s", ErrInvalidCompileTargetSegment, target.SourceSegment)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func isKnownSegment(seg SegmentName) bool {
	switch seg {
	case SegmentSystem, SegmentHistory, SegmentTools, SegmentMemory:
		return true
	default:
		return false
	}
}

func (e *Engine) compileTargets(
	ctx context.Context,
	snap ConversationSnapshot,
	targets []CompileTarget,
	source CompileRequest,
	transforms map[string]TransformChain,
	artifacts []ContextArtifact,
) (map[string]CompileProjection, error) {
	if len(targets) == 0 {
		return map[string]CompileProjection{}, nil
	}
	out := make(map[string]CompileProjection, len(targets))
	artifactIDs := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.ID != "" {
			artifactIDs = append(artifactIDs, artifact.ID)
		}
	}
	for _, target := range targets {
		proj, err := e.compileTarget(ctx, snap, target, source, transforms)
		if err != nil {
			return nil, err
		}
		proj.Source = source.Freeze()
		proj.ArtifactIDs = append([]string(nil), artifactIDs...)
		out[proj.Name] = proj
	}
	return out, nil
}

func (e *Engine) compileTarget(
	ctx context.Context,
	snap ConversationSnapshot,
	target CompileTarget,
	source CompileRequest,
	transforms map[string]TransformChain,
) (CompileProjection, error) {
	if err := ctx.Err(); err != nil {
		return CompileProjection{}, fmt.Errorf("contexty: compile target %q: %w", target.Name, err)
	}
	name := strings.TrimSpace(target.Name)
	ctx = withCompileIdentity(ctx, source.IdentityPolicy, source.RequireDurableIdentity, source.TurnID, name)
	if trace := traceFromContext(ctx); trace != nil {
		ctx = context.WithValue(ctx, compileTraceKey{}, trace.branch(name))
	}
	// Target-local pipeline events cannot mutate the recorder of the shared pass.
	ctx = withTransformRecorder(ctx, newTransformRecorder(snapshotAllMessages(snap)))
	localTransforms := cloneTransformRecords(transforms)
	if target.View != "" {
		return compileTextViewTarget(ctx, snap, target, localTransforms)
	}
	seg := target.SourceSegment
	if seg == "" {
		seg = SegmentHistory
	}
	working := snap.Segment(seg)
	if target.Budget != nil {
		before := cloneMessageSlice(working)
		budgetCtx := withBudgetIdentitySegment(ctx, seg)
		trimmed, err := target.Budget.Apply(budgetCtx, working)
		if err != nil {
			return CompileProjection{}, fmt.Errorf("contexty: compile target %q budget: %w", name, err)
		}
		working = trimmed.Messages
		working, err = traceStage(ctx, "budget", before, working, false)
		if err != nil {
			return CompileProjection{}, err
		}
		recordProjectionBudgetTransforms(localTransforms, before, working)
	}
	if target.Formatter != nil {
		before := cloneMessageSlice(working)
		formatted, err := formatTargetMessages(ctx, target, seg, working)
		if err != nil {
			return CompileProjection{}, err
		}
		working = formatted
		recordProjectionFormatterTransforms(localTransforms, before, working)
	}
	projected, err := traceStage(ctx, "project", working, working, false)
	if err != nil {
		return CompileProjection{}, err
	}
	working = projected
	if target.Budget != nil {
		if err := target.Budget.validateRecordedRetention(ctx, working); err != nil {
			return CompileProjection{}, err
		}
		if err := target.Budget.validateSegments(
			ctx,
			[]EstimateSegment{{Name: manifestMessagesSegment, Messages: working}},
		); err != nil {
			return CompileProjection{}, fmt.Errorf("contexty: compile target %q final budget: %w", name, err)
		}
	}
	if err := validateUniqueMessageIDs(working); err != nil {
		return CompileProjection{}, fmt.Errorf("contexty: compile target %q identity: %w", name, err)
	}
	return CompileProjection{
		Name:            name,
		Text:            plainMessagesText(working),
		Messages:        cloneMessageSlice(working),
		Transformations: localTransforms,
		Source:          emptyCompileRequest(),
		InputSnapshot:   snap.AllSegmentsSnapshot(),
		ArtifactIDs:     nil,
		Lineage:         traceGraph(ctx),
		Rendered:        nil,
	}, nil
}

func compileTextViewTarget(ctx context.Context, snap ConversationSnapshot, target CompileTarget,
	transforms map[string]TransformChain,
) (CompileProjection, error) {
	ctx = withRecordingComponent(
		ctx,
		recordingKey(RecordingViewRenderer, strings.TrimSpace(target.Name), "", 0),
		"render",
	)
	f, _ := builtinViewFormatter(target.View)
	text, err := f.Format(ctx, snap)
	if err != nil {
		return CompileProjection{}, fmt.Errorf("contexty: compile target %q: %w", target.Name, err)
	}
	var rendered *RenderedOutput
	if trace := traceFromContext(ctx); trace != nil {
		output, renderErr := trace.captureRendering(ctx, snap, text)
		if renderErr != nil {
			return CompileProjection{}, renderErr
		}
		rendered = &output
	}
	return CompileProjection{
		Name: strings.TrimSpace(target.Name), Text: text, Messages: nil,
		Transformations: transforms, Source: emptyCompileRequest(),
		InputSnapshot: snap.AllSegmentsSnapshot(), ArtifactIDs: nil,
		Lineage: traceGraph(ctx), Rendered: rendered,
	}, nil
}

func formatTargetMessages(ctx context.Context, target CompileTarget, seg SegmentName,
	working []Message,
) ([]Message, error) {
	ctx = withRecordingComponent(
		ctx,
		recordingKey(RecordingTargetFormatter, strings.TrimSpace(target.Name), "", 0),
		"format",
	)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	formatted, err := target.Formatter(ctx, cloneMessageSlice(working))
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, fmt.Errorf("contexty: compile target %q formatter: %w", target.Name, err)
	}
	formatted, err = ensureMessageIDsFromContext(ctx, seg, 0, formatted)
	if err != nil {
		return nil, fmt.Errorf("contexty: compile target %q identity: %w", target.Name, err)
	}
	return traceStage(ctx, "format", working, formatted, false)
}

func emptyCompileRequest() CompileRequest {
	return CompileRequest{
		DeferredResources:      nil,
		TurnID:                 "",
		System:                 nil,
		History:                nil,
		Memory:                 nil,
		Tools:                  nil,
		Pending:                nil,
		CurrentTurn:            nil,
		Artifacts:              nil,
		Options:                nil,
		IdentityPolicy:         nil,
		RequireDurableIdentity: false,
		Targets:                nil,
		CompilationID:          "",
		Lineage:                Lineage{Records: nil, Unresolved: nil},
		Origins:                nil,
		SourceRevision:         0,
		PreviousRecord:         nil,
	}
}

func plainMessagesText(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(formatPartsPlain(m.Parts))
	}
	return b.String()
}

func recordProjectionBudgetTransforms(records map[string]TransformChain, before, after []Message) {
	beforeSet := messageIDSet(before)
	afterSet := messageIDSet(after)
	for _, msg := range before {
		if msg.ID == "" {
			continue
		}
		if _, kept := afterSet[msg.ID]; !kept {
			records[msg.ID] = append(
				records[msg.ID],
				TransformRecord{Action: ActionTruncated, Reason: ReasonTokenBudgetExceeded},
			)
		}
	}
	for _, msg := range after {
		if msg.ID == "" {
			continue
		}
		if _, existed := beforeSet[msg.ID]; !existed {
			records[msg.ID] = append(records[msg.ID], TransformRecord{Action: ActionPassed, Reason: ""})
		}
	}
}

func recordProjectionFormatterTransforms(records map[string]TransformChain, before, after []Message) {
	beforeSet := messageIDSet(before)
	afterSet := messageIDSet(after)
	for _, msg := range before {
		if msg.ID == "" {
			continue
		}
		if _, kept := afterSet[msg.ID]; !kept {
			records[msg.ID] = append(
				records[msg.ID],
				TransformRecord{Action: ActionFormatted, Reason: ReasonReplacedByFormatter},
			)
			continue
		}
		formatted := findMessageByID(after, msg.ID)
		if !MessageEqual(msg, formatted) {
			records[msg.ID] = append(
				records[msg.ID],
				TransformRecord{Action: ActionFormatted, Reason: ReasonSegmentFormatter},
			)
		}
	}
	for _, msg := range after {
		if msg.ID == "" {
			continue
		}
		if _, existed := beforeSet[msg.ID]; !existed {
			records[msg.ID] = append(
				records[msg.ID],
				TransformRecord{Action: ActionFormatted, Reason: ReasonSegmentFormatter},
			)
		}
	}
}
