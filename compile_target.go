package contexty

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	Transformations map[string]TransformRecord
	Source          CompileRequest       // normalized request before pipeline mutations
	InputSnapshot   ConversationSnapshot // compiled snapshot used as target input
	ArtifactIDs     []string
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

func cloneTransformRecords(in map[string]TransformRecord) map[string]TransformRecord {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]TransformRecord, len(in))
	maps.Copy(out, in)
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
	transforms map[string]TransformRecord,
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
	transforms map[string]TransformRecord,
) (CompileProjection, error) {
	if err := ctx.Err(); err != nil {
		return CompileProjection{}, fmt.Errorf("contexty: compile target %q: %w", target.Name, err)
	}
	name := strings.TrimSpace(target.Name)
	ctx = withCompileIdentity(ctx, source.IdentityPolicy, source.RequireDurableIdentity, source.TurnID, name)
	localTransforms := cloneTransformRecords(transforms)
	if target.View != "" {
		f, _ := builtinViewFormatter(target.View)
		text, err := f.Format(ctx, snap)
		if err != nil {
			return CompileProjection{}, fmt.Errorf("contexty: compile target %q: %w", name, err)
		}
		return CompileProjection{
			Name:            name,
			Text:            text,
			Messages:        nil,
			Transformations: localTransforms,
			Source:          emptyCompileRequest(),
			InputSnapshot:   snap.AllSegmentsSnapshot(),
			ArtifactIDs:     nil,
		}, nil
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
		working = trimmed
		recordProjectionBudgetTransforms(localTransforms, before, working)
	}
	if target.Formatter != nil {
		before := cloneMessageSlice(working)
		formatted, err := target.Formatter(ctx, cloneMessageSlice(working))
		if err != nil {
			return CompileProjection{}, fmt.Errorf("contexty: compile target %q formatter: %w", name, err)
		}
		working, err = ensureMessageIDsFromContext(ctx, seg, 0, formatted)
		if err != nil {
			return CompileProjection{}, fmt.Errorf("contexty: compile target %q identity: %w", name, err)
		}
		recordProjectionFormatterTransforms(localTransforms, before, working)
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
	}, nil
}

func emptyCompileRequest() CompileRequest {
	return CompileRequest{
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
	}
}

func plainMessagesText(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(formatPartsPlain(m.Parts))
	}
	return b.String()
}

func recordProjectionBudgetTransforms(records map[string]TransformRecord, before, after []Message) {
	beforeSet := messageIDSet(before)
	afterSet := messageIDSet(after)
	for _, msg := range before {
		if msg.ID == "" {
			continue
		}
		if _, kept := afterSet[msg.ID]; !kept {
			records[msg.ID] = TransformRecord{Action: ActionTruncated, Reason: ReasonTokenBudgetExceeded}
		}
	}
	for _, msg := range after {
		if msg.ID == "" {
			continue
		}
		if _, existed := beforeSet[msg.ID]; !existed {
			records[msg.ID] = TransformRecord{Action: ActionPassed, Reason: ""}
		}
	}
}

func recordProjectionFormatterTransforms(records map[string]TransformRecord, before, after []Message) {
	beforeSet := messageIDSet(before)
	afterSet := messageIDSet(after)
	for _, msg := range before {
		if msg.ID == "" {
			continue
		}
		if _, kept := afterSet[msg.ID]; !kept {
			records[msg.ID] = TransformRecord{Action: ActionFormatted, Reason: ReasonReplacedByFormatter}
			continue
		}
		formatted := findMessageByID(after, msg.ID)
		if !MessageEqual(msg, formatted) {
			records[msg.ID] = TransformRecord{Action: ActionFormatted, Reason: ReasonSegmentFormatter}
		}
	}
	for _, msg := range after {
		if msg.ID == "" {
			continue
		}
		if _, existed := beforeSet[msg.ID]; !existed {
			records[msg.ID] = TransformRecord{Action: ActionFormatted, Reason: ReasonSegmentFormatter}
		}
	}
}
