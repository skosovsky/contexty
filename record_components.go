package contexty

import (
	"context"
	"errors"
	"slices"
	"strings"
)

var ErrInvalidRecordingComponent = errors.New("contexty: missing or invalid recording component binding")

type RecordingComponentKind string

const (
	RecordingHook             RecordingComponentKind = "hook"
	RecordingResolver         RecordingComponentKind = "resolver"
	RecordingSegmentFormatter RecordingComponentKind = "segment_formatter"
	RecordingTargetFormatter  RecordingComponentKind = "target_formatter"
	RecordingRolePolicy       RecordingComponentKind = "role_policy"
	RecordingLabelPolicy      RecordingComponentKind = "label_policy"
	RecordingTraceMapping     RecordingComponentKind = "trace_mapping"
	RecordingIdentityPolicy   RecordingComponentKind = "identity_policy"
	RecordingViewRenderer     RecordingComponentKind = "view_renderer"
)

// RecordingComponentKey addresses one configured callback, not a domain object.
type RecordingComponentKey struct {
	Kind    RecordingComponentKind `json:"kind"`
	Target  string                 `json:"target,omitempty"`
	Segment SegmentName            `json:"segment,omitempty"`
	Index   int                    `json:"index"`
}

// RecordingComponent binds a caller-owned implementation identity to its slot.
type RecordingComponent struct {
	Key        RecordingComponentKey `json:"key"`
	Descriptor Descriptor            `json:"descriptor"`
}

func cloneRecordingComponents(components []RecordingComponent) []RecordingComponent {
	out := slices.Clone(components)
	slices.SortFunc(out, func(a, b RecordingComponent) int {
		for _, pair := range [][2]string{{string(a.Key.Kind), string(b.Key.Kind)}, {a.Key.Target, b.Key.Target},
			{string(a.Key.Segment), string(b.Key.Segment)}} {
			if order := strings.Compare(pair[0], pair[1]); order != 0 {
				return order
			}
		}
		if a.Key.Index < b.Key.Index {
			return -1
		}
		if a.Key.Index > b.Key.Index {
			return 1
		}
		return 0
	})
	return out
}

func validateRecordingComponents(components []RecordingComponent) error {
	seen := make(map[RecordingComponentKey]bool)
	for _, component := range components {
		if seen[component.Key] || !validRecordingKey(component.Key) {
			return ErrInvalidRecordingComponent
		}
		if err := component.Descriptor.Validate(); err != nil {
			return err
		}
		seen[component.Key] = true
	}
	if !slices.Equal(components, cloneRecordingComponents(components)) {
		return ErrInvalidRecordingComponent
	}
	return nil
}

func validRecordingKey(key RecordingComponentKey) bool {
	if key.Index < 0 {
		return false
	}
	switch key.Kind {
	case RecordingHook, RecordingResolver:
		return key.Target == "" && key.Segment == ""
	case RecordingSegmentFormatter:
		return key.Target == "" && key.Index == 0 && isKnownSegment(key.Segment)
	case RecordingTargetFormatter, RecordingViewRenderer:
		return key.Index == 0 && key.Segment == "" && key.Target != "" && key.Target == strings.TrimSpace(key.Target)
	case RecordingRolePolicy, RecordingLabelPolicy, RecordingTraceMapping, RecordingIdentityPolicy:
		return key.Index == 0 && key.Target == "" && key.Segment == ""
	default:
		return false
	}
}

func (e *Engine) validateRecordingComponentTopology(request CompileRequest) error {
	expected := e.recordingComponentTopology(request)
	if len(expected) != len(e.recording.Components) {
		return ErrInvalidRecordingComponent
	}
	for _, binding := range e.recording.Components {
		if !expected[binding.Key] {
			return ErrInvalidRecordingComponent
		}
	}
	return nil
}

func recordingKey(kind RecordingComponentKind, target string, segment SegmentName, index int) RecordingComponentKey {
	return RecordingComponentKey{Kind: kind, Target: target, Segment: segment, Index: index}
}

func (e *Engine) recordingComponentTopology(request CompileRequest) map[RecordingComponentKey]bool {
	keys := make(map[RecordingComponentKey]bool)
	for index, hook := range e.hooks {
		if hook != nil {
			keys[recordingKey(RecordingHook, "", "", index)] = true
		}
	}
	for index, block := range e.deferred {
		if block.Resolve != nil {
			keys[recordingKey(RecordingResolver, "", "", index)] = true
		}
	}
	for segment, formatter := range e.formatters {
		if formatter != nil {
			keys[recordingKey(RecordingSegmentFormatter, "", segment, 0)] = true
		}
	}
	if e.roleProjection != nil {
		keys[recordingKey(RecordingRolePolicy, "", "", 0)] = true
	}
	if e.trace.Mapping != nil {
		keys[recordingKey(RecordingTraceMapping, "", "", 0)] = true
	}
	if e.trace.Labels.Policy != nil {
		keys[recordingKey(RecordingLabelPolicy, "", "", 0)] = true
	}
	if request.IdentityPolicy != nil {
		keys[recordingKey(RecordingIdentityPolicy, "", "", 0)] = true
	}
	for _, target := range request.Targets {
		name := strings.TrimSpace(target.Name)
		if target.Formatter != nil {
			keys[recordingKey(RecordingTargetFormatter, name, "", 0)] = true
		}
		if target.View != "" {
			keys[recordingKey(RecordingViewRenderer, name, "", 0)] = true
		}
	}
	return keys
}

type recordingComponentsKey struct{}
type recordingStageComponentKey struct{}
type recordingStageComponent struct {
	stage      string
	descriptor Descriptor
}

func withRecordingComponent(ctx context.Context, key RecordingComponentKey, stage string) context.Context {
	bindings, _ := ctx.Value(recordingComponentsKey{}).([]RecordingComponent)
	for _, binding := range bindings {
		if binding.Key == key {
			return context.WithValue(
				ctx,
				recordingStageComponentKey{},
				recordingStageComponent{stage: stage, descriptor: binding.Descriptor},
			)
		}
	}
	return ctx
}

func recordingStageDescriptor(ctx context.Context, stage string, fallback Descriptor) Descriptor {
	component, found := ctx.Value(recordingStageComponentKey{}).(recordingStageComponent)
	if found && component.stage == stage {
		return component.descriptor
	}
	return fallback
}
