package contexty

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
)

var ErrMissingLineage = errors.New("contexty: missing source lineage")

const traceStageDeferred = "deferred"
const traceStageSource = "source"
const traceStageSummarize = "summarize"

// TraceMapping explicitly attributes changed/new output IDs to available input
// content. It must not refer to a source absent from the stage inputs.
type TraceMapping func(ctx context.Context, stage string, inputs, outputs []Message) (map[string][]ContentRef, error)

// TraceProfile pins execution and encoding for strict opt-in compile tracing.
// Stages are intrinsic names (source, deferred, merge, hook, role, format,
// summarize, budget, prompt, patch, project); descriptors identify host-provided behavior.
type TraceProfile struct {
	Encoding       Descriptor
	Codec          JSONSerializer
	Stages         map[string]Descriptor
	Mapping        TraceMapping
	Labels         LabelProjection
	RequireOrigins bool
	Codecs         []CodecBinding
}

// WithTraceProfile installs a defensive copy of trace configuration.
func WithTraceProfile(profile TraceProfile) EngineOption {
	return func(e *Engine) {
		copyProfile := profile
		copyProfile.Stages = maps.Clone(profile.Stages)
		copyProfile.Codec = snapshotJSONSerializer(profile.Codec)
		copyProfile.Codecs = cloneCodecBindings(profile.Codecs)
		copyProfile.Labels.Registry = profile.Labels.Registry.snapshot()
		copyProfile.Labels.RequiredTypes = canonicalLabelTypes(profile.Labels.RequiredTypes)
		e.trace = &copyProfile
	}
}

type compileTraceKey struct{}

type compileTrace struct {
	profile   TraceProfile
	graph     Lineage
	latest    map[ContentRef]ContentRef
	summaries map[ContentRef]ContentRef
	ordinal   int
	prefix    string
}

func newCompileTrace(profile *TraceProfile, req CompileRequest) (*compileTrace, error) {
	if req.CompilationID == "" {
		return nil, fmt.Errorf("%w: compilation identity", ErrInvalidDescriptor)
	}
	if err := profile.Encoding.Validate(); err != nil {
		return nil, err
	}
	if err := req.Lineage.Validate(); err != nil {
		return nil, err
	}
	trace := &compileTrace{ //nolint:exhaustruct_v5 // ordinal/prefix start at zero
		profile:   *profile,
		graph:     req.Lineage.Clone(),
		latest:    make(map[ContentRef]ContentRef),
		summaries: make(map[ContentRef]ContentRef),
		prefix:    "compile/" + req.CompilationID + "/",
	}
	for _, record := range trace.graph.Records {
		for _, ref := range record.Outputs {
			trace.latest[baseContentRef(ref)] = ref
		}
	}
	for _, origin := range req.Origins {
		if err := origin.Validate(); err != nil {
			return nil, err
		}
		trace.latest[baseContentRef(origin)] = origin
	}
	return trace, nil
}

func baseContentRef(ref ContentRef) ContentRef {
	ref.Occurrence = ""
	return ref
}

func traceFromContext(ctx context.Context) *compileTrace {
	trace, _ := ctx.Value(compileTraceKey{}).(*compileTrace)
	return trace
}

func (t *compileTrace) branch(name string) *compileTrace {
	if t == nil {
		return nil
	}
	return &compileTrace{
		profile:   t.profile,
		graph:     t.graph.Clone(),
		latest:    maps.Clone(t.latest),
		summaries: maps.Clone(t.summaries),
		ordinal:   t.ordinal,
		prefix:    t.prefix + "target/" + name + "/",
	}
}

func traceGraph(ctx context.Context) Lineage {
	if trace := traceFromContext(ctx); trace != nil {
		return trace.graph.Clone()
	}
	return Lineage{Records: nil, Unresolved: nil}
}

func traceStage(ctx context.Context, stage string, before, after []Message, summarize bool) ([]Message, error) {
	trace := traceFromContext(ctx)
	if trace == nil {
		return after, nil
	}
	return trace.capture(ctx, stage, before, after, summarize)
}

func traceSnapshot(
	ctx context.Context,
	stage string,
	before, after ConversationSnapshot,
) (ConversationSnapshot, error) {
	if traceFromContext(ctx) == nil {
		return after, nil
	}
	// A snapshot hook can move messages across segments; inputs remain global.
	projected, err := traceStage(ctx, stage, snapshotAllMessages(before), snapshotAllMessages(after), false)
	if err != nil {
		return ConversationSnapshot{}, err
	}
	byID := make(map[string]Message, len(projected))
	for _, msg := range projected {
		byID[msg.ID] = msg
	}
	for _, segment := range snapshotSegmentOrder() {
		msgs := after.Segment(segment)
		for i := range msgs {
			msgs[i] = byID[msgs[i].ID].Clone()
		}
		after = after.WithSegment(segment, msgs)
	}
	return after, nil
}

func (t *compileTrace) inputRef(msg Message) (ContentRef, error) {
	ref, err := MessageContentRef(msg, t.profile.Codec)
	if err != nil {
		return ContentRef{}, err
	}
	if known, ok := t.latest[ref]; ok {
		return known, nil
	}
	if t.profile.RequireOrigins {
		return ContentRef{}, fmt.Errorf("%w: %s", ErrMissingLineage, msg.ID)
	}
	if !slices.Contains(t.graph.Unresolved, ref) {
		t.graph.Unresolved = append(t.graph.Unresolved, ref)
	}
	return ref, nil
}

func (t *compileTrace) capture(
	ctx context.Context,
	stage string,
	before, after []Message,
	summarize bool,
) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(before) == 0 && len(after) == 0 {
		return nil, nil
	}
	descriptor, ok := t.profile.Stages[stage]
	if !ok {
		return nil, fmt.Errorf("%w: stage %s", ErrInvalidDescriptor, stage)
	}
	descriptor = recordingStageDescriptor(ctx, stage, descriptor)
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	before = t.stageBefore(stage, before, after)
	refs, err := t.stageInputs(ctx, before)
	if err != nil {
		return nil, err
	}
	mapping, err := t.stageMapping(ctx, stage, before, after)
	if err != nil {
		return nil, err
	}
	result := cloneMessageSlice(after)
	for i, output := range result {
		inputs, inputErr := selectTraceInputs(output, before, refs, mapping, summarize, t.profile.Codec)
		if inputErr != nil {
			return nil, inputErr
		}
		projected, outputErr := t.captureOutput(ctx, stage, descriptor, inputs, refs, output)
		if outputErr != nil {
			return nil, outputErr
		}
		result[i] = projected
	}
	// Removed messages remain represented by an input-only invocation.
	kept := messageIDSet(after)
	var removed []ContentRef
	for _, msg := range before {
		if _, ok := kept[msg.ID]; !ok {
			removed = append(removed, refs[msgKey(msg, t.profile.Codec)])
		}
	}
	if len(removed) > 0 {
		err = t.appendRecord(LineageRecord{ID: t.nextID(stage), Transform: descriptor,
			Inputs: uniqueContentRefs(removed), Outputs: nil, DecisionRef: "", Stage: stage})
	}
	return result, err
}

func (t *compileTrace) stageBefore(stage string, before, after []Message) []Message {
	inputs := cloneMessageSlice(before)
	if stage != string(EvictionReasonBudget) {
		return inputs
	}
	for _, output := range after {
		if _, summarized := t.summaries[msgKey(output, t.profile.Codec)]; summarized {
			inputs = append(inputs, output.Clone())
		}
	}
	return inputs
}

func (t *compileTrace) stageMapping(ctx context.Context, stage string, before, after []Message) (
	map[string][]ContentRef, error,
) {
	mapping := make(map[string][]ContentRef)
	if t.profile.Mapping != nil {
		provided, err := t.profile.Mapping(ctx, stage, cloneMessageSlice(before), cloneMessageSlice(after))
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		if err != nil {
			return nil, err
		}
		// Do not mutate the map retained by a host callback.
		for id, refs := range provided {
			mapping[id] = slices.Clone(refs)
		}
	}
	if stage == string(EvictionReasonBudget) {
		for _, output := range after {
			if ref, summarized := t.summaries[msgKey(output, t.profile.Codec)]; summarized {
				// Reused summary bytes may already have a later pass-through occurrence.
				// Select the current stage revision rather than the original summary invocation.
				mapping[output.ID] = []ContentRef{baseContentRef(ref)}
			}
		}
	}
	return mapping, nil
}

func msgKey(msg Message, codec JSONSerializer) ContentRef {
	ref, _ := MessageContentRef(msg, codec) // stageInputs already validated every input
	return ref
}

func (t *compileTrace) stageInputs(ctx context.Context, msgs []Message) (map[ContentRef]ContentRef, error) {
	refs := make(map[ContentRef]ContentRef, len(msgs))
	for _, msg := range msgs {
		if err := t.profile.Labels.validateLabels(ctx, msg.Extensions, true); err != nil {
			return nil, err
		}
		ref, err := t.inputRef(msg)
		if err != nil {
			return nil, err
		}
		refs[baseContentRef(ref)] = ref
	}
	return refs, nil
}

func selectTraceInputs(output Message, before []Message, refs map[ContentRef]ContentRef,
	mapping map[string][]ContentRef, summarize bool, codec JSONSerializer,
) ([]Message, error) {
	selected, explicit := mapping[output.ID]
	var inputs []Message
	for _, msg := range before {
		if !explicit && (msg.ID == output.ID || summarize) {
			inputs = append(inputs, msg)
		}
	}
	if explicit {
		for _, selectedRef := range selected {
			base := baseContentRef(selectedRef)
			if !traceMappingRefAvailable(selectedRef, refs) {
				return nil, ErrMissingLineage
			}
			for _, msg := range before {
				if msgKey(msg, codec) == base {
					inputs = append(inputs, msg)
				}
			}
		}
	}
	if len(inputs) == 0 && len(before) > 0 {
		return nil, fmt.Errorf("%w: output %s needs input mapping", ErrMissingLineage, output.ID)
	}
	return inputs, nil
}

func traceMappingRefAvailable(selected ContentRef, refs map[ContentRef]ContentRef) bool {
	available, found := refs[baseContentRef(selected)]
	return found && (selected.Occurrence == "" || selected == available)
}

func (t *compileTrace) captureOutput(ctx context.Context, stage string, descriptor Descriptor,
	inputs []Message, available map[ContentRef]ContentRef, output Message,
) (Message, error) {
	projected := output.Clone()
	decision := ""
	if stage == traceStageSource || stage == traceStageDeferred {
		if err := t.profile.Labels.validateLabels(ctx, output.Extensions, true); err != nil {
			return Message{}, err
		}
	} else {
		var err error
		projected, decision, err = t.profile.Labels.Project(ctx, inputs, output, descriptor)
		if err != nil {
			return Message{}, err
		}
	}
	var inputRefs []ContentRef
	for _, input := range inputs {
		ref, ok := available[msgKey(input, t.profile.Codec)]
		if !ok {
			return Message{}, ErrMissingLineage
		}
		inputRefs = append(inputRefs, ref)
	}
	ref, err := MessageContentRef(projected, t.profile.Codec)
	if err != nil {
		return Message{}, err
	}
	// An empty rendered envelope is a representation of zero inputs, not a newly
	// introduced source message. Its pinned renderer descriptor explains the bytes.
	if len(inputs) == 0 && stage != "render" {
		root, rootErr := t.inputRef(output)
		if rootErr != nil {
			return Message{}, rootErr
		}
		inputRefs = append(inputRefs, root)
	}
	invocation := t.nextID(stage)
	ref.Occurrence = invocation
	err = t.appendRecord(LineageRecord{ID: invocation, Transform: descriptor,
		Inputs: uniqueContentRefs(inputRefs), Outputs: []ContentRef{ref}, DecisionRef: decision, Stage: stage})
	if err != nil {
		return Message{}, err
	}
	if captureErr := captureTraceMessage(ctx, projected, stage); captureErr != nil {
		return Message{}, captureErr
	}
	t.latest[baseContentRef(ref)] = ref
	if stage == traceStageSummarize {
		t.summaries[baseContentRef(ref)] = ref
	}
	updateIntroducedLabels(ctx, projected)
	return projected, nil
}

func updateIntroducedLabels(ctx context.Context, projected Message) {
	recorder := transformRecorderFrom(ctx)
	if recorder == nil {
		return
	}
	baseline, introduced := recorder.introduced[projected.ID]
	if !introduced {
		return
	}
	baseline.Extensions = cloneExtensions(projected.Extensions)
	baseline.SourceRefs = cloneSourceRefs(projected.SourceRefs)
	recorder.introduced[projected.ID] = baseline
}

func captureTraceMessage(ctx context.Context, message Message, stage string) error {
	if capture := contentCaptureFrom(ctx); capture != nil {
		return capture.message(ctx, message, CaptureTransform, stage)
	}
	return nil
}

func (t *compileTrace) captureRendering(ctx context.Context, snap ConversationSnapshot, text string) (
	RenderedOutput, error,
) {
	message := TextMessage(RoleUser, text)
	message.ID = t.prefix + "rendered"
	var before []Message
	for _, segment := range viewSegmentOrder() {
		before = append(before, snap.Segment(segment)...)
	}
	outputs, err := t.capture(ctx, "render", before, []Message{message}, true)
	if err != nil {
		return RenderedOutput{}, err
	}
	ref, err := MessageContentRef(outputs[0], t.profile.Codec)
	if err != nil {
		return RenderedOutput{}, err
	}
	return RenderedOutput{
		Message:  outputs[0].Clone(),
		Ref:      t.latest[ref],
		Renderer: recordingStageDescriptor(ctx, "render", t.profile.Stages["render"]),
	}, nil
}

func (t *compileTrace) nextID(stage string) string {
	t.ordinal++
	return t.prefix + stage + "/" + strconv.Itoa(t.ordinal)
}

func (t *compileTrace) appendRecord(record LineageRecord) error {
	graph, err := t.graph.WithRecord(record)
	if err == nil {
		t.graph = graph
	}
	return err
}

func uniqueContentRefs(refs []ContentRef) []ContentRef {
	result := make([]ContentRef, 0, len(refs))
	for _, ref := range refs {
		if !slices.Contains(result, ref) {
			result = append(result, ref)
		}
	}
	return result
}

func (t *compileTrace) mainBranch() *compileTrace {
	copyTrace := t.branch("")
	copyTrace.prefix = t.prefix + "main/"
	return copyTrace
}
