package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/skosovsky/contexty"
	"github.com/skosovsky/prompty"
)

var (
	ErrStaleExecution = errors.New("chat consumer: stale execution or report")
	ErrBudget         = errors.New("chat consumer: invalid or exceeded native budget")
	ErrIncomplete     = errors.New("chat consumer: incomplete terminal result")
)

// ByteBudget is an offline wire-byte budget, not a provider token count.
// Window includes output and wire overhead reservations. Each is deducted once.
type ByteBudget struct{ Window, Output, Overhead int }

type Report struct {
	Digest                string
	Profile               contexty.Descriptor
	Bytes, EffectiveLimit int
	Budget                ByteBudget
}

// Prepared pins semantic revisions as well as the final native request.
// Mutating Execution requires preparing a new request and report.
type Prepared struct {
	Execution *prompty.PromptExecution
	Report    Report
	Source    []contexty.ContentRef
	Mandatory []string
}

func requestWire(execution *prompty.PromptExecution) ([]byte, error) {
	wire, err := prompty.MarshalExecution(execution, prompty.WirePolicy{State: prompty.StatePreserve})
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}

func digest(wire []byte) string { sum := sha256.Sum256(wire); return hex.EncodeToString(sum[:]) }

func budgetLimit(budget ByteBudget) (int, error) {
	if budget.Window <= 0 || budget.Output < 0 || budget.Overhead < 0 || budget.Output >= budget.Window || budget.Overhead >= budget.Window-budget.Output {
		return 0, ErrBudget
	}
	return budget.Window - budget.Output - budget.Overhead, nil
}

// Prepare projects without a second truncation and reports on the final execution.
func (m Mapper) Prepare(messages []contexty.Message, mandatory []string, budget ByteBudget, now time.Time) (Prepared, error) {
	records, err := m.Export(messages, mandatory, now)
	if err != nil {
		return Prepared{}, err
	}
	native := make([]prompty.ChatMessage, len(records))
	var refs []contexty.ContentRef
	for i, record := range records {
		native[i] = record.Message
		ref, refErr := contexty.MessageContentRef(messages[i], m.Codec)
		if refErr != nil {
			return Prepared{}, refErr
		}
		refs = append(refs, ref)
	}
	execution := prompty.NewExecution(native)
	execution.ModelOptions = &prompty.ModelOptions{Model: m.Destination.Model}
	wire, err := requestWire(execution)
	if err != nil {
		return Prepared{}, err
	}
	limit, err := budgetLimit(budget)
	if err != nil || len(wire) > limit {
		return Prepared{}, ErrBudget
	}
	return Prepared{Execution: execution, Source: refs, Mandatory: slices.Clone(mandatory),
		Report: Report{Digest: digest(wire), Profile: m.Profile, Bytes: len(wire), EffectiveLimit: limit, Budget: budget}}, nil
}

// ValidatePrepared runs immediately before execution, also after a restore.
func (m Mapper) ValidatePrepared(prepared Prepared, messages []contexty.Message, now time.Time) error {
	actual, err := m.Prepare(messages, prepared.Mandatory, prepared.Report.Budget, now)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual.Source, prepared.Source) || actual.Report != prepared.Report {
		return ErrStaleExecution
	}
	wire, err := requestWire(prepared.Execution)
	if err != nil {
		return err
	}
	if digest(wire) != actual.Report.Digest {
		return ErrStaleExecution
	}
	return prompty.ValidateContinuation(prepared.Execution.Messages, m.Destination, now)
}

// CompileTurn compiles exactly one current turn from a host snapshot.
func (m Mapper) CompileTurn(ctx context.Context, state contexty.ConversationState, turn contexty.Message) (contexty.CompileResult, error) {
	if turn.ID == "" || turn.Role != contexty.RoleUser {
		return contexty.CompileResult{}, ErrUnsupported
	}
	for _, message := range state.Segment(contexty.SegmentHistory) {
		if message.ID == turn.ID {
			return contexty.CompileResult{}, ErrStaleExecution
		}
	}
	current := contexty.NewCurrentTurn(turn)
	stages := make(map[string]contexty.Descriptor)
	for _, stage := range []string{"source", "project", "opaque-state", "prompt-template", "prompt", "patch", "merge", "format", "role", "hook", "budget"} {
		stages[stage] = contexty.Descriptor{ID: "host/" + stage, Revision: "fixed"}
	}
	engine := contexty.NewEngine(
		contexty.WithTraceProfile(contexty.TraceProfile{Encoding: contexty.Descriptor{ID: "host/json", Revision: "fixed"}, Codec: m.Codec, Stages: stages, Labels: contexty.LabelProjection{Registry: m.Codec.Extensions, Policy: preserveLabels{}}}),
		contexty.WithOpaqueStatePolicy(contexty.OpaqueStatePolicy{Identity: contexty.Descriptor{ID: "host-state-policy", Revision: "fixed"}, Profile: m.Profile, Invalidated: contexty.OpaqueFailClosed}),
		contexty.WithOutputPolicy(contexty.OutputPolicy{Identity: contexty.Descriptor{ID: "host-output-policy", Revision: "fixed"},
			Project: func(_ context.Context, input contexty.OutputPolicyInput) (contexty.AbstractPayload, error) {
				return input.Payload, nil
			}}),
	)
	return engine.CompileSnapshot(ctx, contexty.CompileRequest{System: state.Segment(contexty.SegmentSystem),
		History: state.Segment(contexty.SegmentHistory), Tools: state.Segment(contexty.SegmentTools), Memory: state.Segment(contexty.SegmentMemory),
		CompilationID: "turn:" + turn.ID, CurrentTurn: &current, SourceRevision: state.Version()})
}

// CommitTerminal accepts a complete response only. Outcome is a host-declared
// transport terminal fact; no streaming completion is inferred from text.
func (m Mapper) CommitTerminal(ctx context.Context, store contexty.ConversationStateStore, conversationID string,
	expected int64, turn contexty.Message, response *prompty.Response, terminal bool, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if turn.ID == "" || turn.Role != contexty.RoleUser {
		return ErrUnsupported
	}
	if !terminal || response == nil || response.Outcome != prompty.OutcomeCompleted {
		return ErrIncomplete
	}
	result, _, err := m.Import([]Record{{ID: turn.ID + ":result", Message: prompty.ChatMessage{Role: prompty.RoleAssistant,
		Content: response.Content, ProviderState: response.ProviderState, Annotations: response.Annotations,
		MessageAnnotations: response.MessageAnnotations, ContinuationUnavailable: response.ContinuationUnavailable}}}, now)
	if err != nil {
		return err
	}
	if len(result[0].ToolCallParts()) > 0 {
		return ErrIncomplete
	}
	state, err := store.LoadState(ctx, conversationID)
	if err != nil {
		return err
	}
	history := state.Segment(contexty.SegmentHistory)
	var savedTurn, savedResult *contexty.Message
	for i := range history {
		switch history[i].ID {
		case turn.ID:
			savedTurn = &history[i]
		case result[0].ID:
			savedResult = &history[i]
		}
	}
	// Bind terminal state to the actual committed prefix, not an empty import prefix.
	prefixHistory := history
	if savedTurn != nil {
		for index, message := range history {
			if message.ID == turn.ID {
				prefixHistory = history[:index]
				break
			}
		}
	}
	prefix := append(slices.Clone(prefixHistory), turn)
	for i, extension := range result[0].Extensions {
		if envelope, ok := extension.(contexty.OpaqueState); ok {
			for _, message := range append(state.Segment(contexty.SegmentSystem), prefix...) {
				ref, refErr := contexty.MessageContentRef(message, m.Codec)
				if refErr != nil {
					return refErr
				}
				envelope.Binding.Prefix = append(envelope.Binding.Prefix, ref)
			}
			envelope.Binding.Boundary = turn.ID
			result[0].Extensions[i] = envelope
		}
	}
	if savedTurn != nil || savedResult != nil {
		if savedTurn != nil && savedResult != nil && contexty.MessageEqual(*savedTurn, turn) && contexty.MessageEqual(*savedResult, result[0]) {
			return nil
		}
		return ErrStaleExecution
	}
	if state.Version() != expected {
		return contexty.ErrConversationVersionConflict
	}
	return store.CommitState(ctx, conversationID, expected, contexty.ConversationDelta{Operation: contexty.DeltaAppendMessages,
		Segment: contexty.SegmentHistory, Messages: []contexty.Message{turn, result[0]}})
}

type preserveLabels struct{}

func (preserveLabels) ProjectLabels(_ context.Context, _ []contexty.Message, output contexty.Message, _ contexty.Descriptor) (contexty.LabelDecision, error) {
	return contexty.LabelDecision{Extensions: output.Extensions}, nil
}
