package contexty

import (
	"cmp"
	"context"
	"errors"
	"slices"
)

type OpaqueInvalidationMode string

const (
	OpaqueFailClosed  OpaqueInvalidationMode = "fail"
	OpaqueDropInvalid OpaqueInvalidationMode = "drop"
)

// OpaqueStatePolicy pins the receiving host profile and explicit invalidation behavior.
// No callback interprets payload; providers own lifecycle rules expressed in bindings.
type OpaqueStatePolicy struct {
	Identity    Descriptor             `json:"identity"`
	Profile     Descriptor             `json:"profile"`
	Invalidated OpaqueInvalidationMode `json:"invalidated"`
}

func (p OpaqueStatePolicy) validate() error {
	if p.Identity.Validate() != nil || p.Profile.Validate() != nil {
		return ErrInvalidOpaqueState
	}
	if p.Invalidated != "" && p.Invalidated != OpaqueFailClosed && p.Invalidated != OpaqueDropInvalid {
		return ErrInvalidOpaqueState
	}
	return nil
}

func WithOpaqueStatePolicy(policy OpaqueStatePolicy) EngineOption {
	return func(e *Engine) {
		owned := policy
		if owned.Invalidated == "" {
			owned.Invalidated = OpaqueFailClosed
		}
		e.opaqueStatePolicy = &owned
	}
}

type opaqueStateInputsKey struct{}

type opaqueStateOrigin struct {
	message Message
	state   OpaqueState
	order   int
}

func opaqueStateInventory(messages []Message) (map[string]opaqueStateOrigin, error) {
	states := make(map[string]opaqueStateOrigin)
	for _, message := range messages {
		for order, extension := range message.Extensions {
			state, ok := opaqueStateFromExtension(extension)
			if !ok {
				if !nilInterfaceValue(extension) && extension.ExtensionType() == OpaqueStateExtensionType {
					return nil, ErrInvalidOpaqueState
				}
				continue
			}
			if err := state.validate(); err != nil {
				return nil, err
			}
			if _, exists := states[state.ID]; exists {
				return nil, ErrInvalidOpaqueState
			}
			states[state.ID] = opaqueStateOrigin{message: message.Clone(), state: cloneOpaqueState(state), order: order}
		}
	}
	return states, nil
}

func initializeOpaqueStateContext(
	ctx context.Context,
	req CompileRequest,
	policy *OpaqueStatePolicy,
) (context.Context, error) {
	if policy != nil {
		if err := policy.validate(); err != nil {
			return ctx, err
		}
	}
	inventory, err := opaqueStateInventory(req.AllMessages())
	if err != nil {
		return ctx, err
	}
	if len(inventory) > 0 && policy == nil {
		return ctx, ErrMissingOpaqueStatePolicy
	}
	return context.WithValue(ctx, opaqueStateInputsKey{}, inventory), nil
}

func registerPreparedOpaqueStates(ctx context.Context, messages []Message, policy *OpaqueStatePolicy) error {
	inventory, _ := ctx.Value(opaqueStateInputsKey{}).(map[string]opaqueStateOrigin)
	found, err := opaqueStateInventory(messages)
	if err != nil {
		return err
	}
	if len(found) > 0 && policy == nil {
		return ErrMissingOpaqueStatePolicy
	}
	for id, origin := range found {
		prior, exists := inventory[id]
		if exists && (!extensionEqual(prior.state, origin.state) || prior.message.ID != origin.message.ID) {
			return ErrInvalidOpaqueState
		}
		if !exists {
			inventory[id] = origin
		}
	}
	return nil
}

func (e *Engine) enforceOpaqueState(
	ctx context.Context,
	payload AbstractPayload,
) (AbstractPayload, []OpaqueStateDrop, error) {
	messages := semanticOutputMessages(ctx, payload)
	current, err := opaqueStateInventory(messages)
	if err != nil {
		return AbstractPayload{}, nil, err
	}
	original, _ := ctx.Value(opaqueStateInputsKey{}).(map[string]opaqueStateOrigin)
	if len(current) == 0 && len(original) == 0 {
		return payload, nil, nil
	}
	if e.opaqueStatePolicy == nil {
		return AbstractPayload{}, nil, ErrMissingOpaqueStatePolicy
	}
	dropped, err := e.checkOpaqueInventory(ctx, messages, current, original)
	if err != nil {
		return AbstractPayload{}, nil, err
	}
	accepted := cloneOutputPolicyPayload(payload)
	for {
		invalid, invalidErr := e.invalidOpaqueStates(ctx, accepted, current)
		if invalidErr != nil {
			return AbstractPayload{}, nil, invalidErr
		}
		if len(invalid) == 0 {
			break
		}
		dropped = append(dropped, invalid...)
		accepted = dropOpaqueStates(accepted, invalid)
	}
	slices.SortFunc(dropped, func(a, b OpaqueStateDrop) int {
		if value := cmp.Compare(a.Message.ID, b.Message.ID); value != 0 {
			return value
		}
		return cmp.Compare(a.StateID, b.StateID)
	})
	return accepted, dropped, nil
}

func (e *Engine) checkOpaqueInventory(
	ctx context.Context,
	messages []Message,
	current, original map[string]opaqueStateOrigin,
) ([]OpaqueStateDrop, error) {
	var dropped []OpaqueStateDrop
	if err := validateOpaqueOrdering(messages, original); err != nil {
		return nil, err
	}
	for id, origin := range original {
		actual, exists := current[id]
		if exists {
			if !extensionEqual(origin.state, actual.state) || origin.message.ID != actual.message.ID {
				return nil, ErrInvalidOpaqueState
			}
			continue
		}
		if err := e.validateMissingOpaque(ctx, messages, origin); err != nil {
			return nil, err
		}
		ref, err := MessageContentRef(origin.message, selectionCodec(ctx))
		if err != nil {
			return nil, err
		}
		dropped = append(dropped, OpaqueStateDrop{Message: ref, StateID: id})
	}
	for id, origin := range current {
		if _, exists := original[id]; !exists {
			return nil, ErrInvalidOpaqueState
		}
		if origin.state.Binding.Profile != e.opaqueStatePolicy.Profile {
			return nil, ErrOpaqueStateInvalidated
		}
	}
	return dropped, nil
}

func (e *Engine) validateMissingOpaque(ctx context.Context, messages []Message, origin opaqueStateOrigin) error {
	if origin.state.Binding.Profile != e.opaqueStatePolicy.Profile {
		return ErrOpaqueStateInvalidated
	}
	if err := validateOpaquePayloadCodec(origin.state, selectionCodec(ctx).Extensions); err != nil {
		return err
	}
	if e.opaqueStatePolicy.Invalidated != OpaqueDropInvalid {
		return ErrOpaqueStateInvalidated
	}
	for _, message := range messages {
		if message.ID != origin.message.ID {
			continue
		}
		err := validateOneOpaqueState(
			messages,
			origin.message.ID,
			origin.state,
			selectionCodec(ctx),
			e.opaqueStatePolicy.Profile,
		)
		if err == nil {
			return ErrInvalidOpaqueState
		}
		if !errors.Is(err, ErrOpaqueStateInvalidated) {
			return err
		}
	}
	return nil
}

func (e *Engine) invalidOpaqueStates(
	ctx context.Context,
	payload AbstractPayload,
	before map[string]opaqueStateOrigin,
) ([]OpaqueStateDrop, error) {
	messages := semanticOutputMessages(ctx, payload)
	inventory, err := opaqueStateInventory(messages)
	if err != nil {
		return nil, err
	}
	var dropped []OpaqueStateDrop
	for id, origin := range inventory {
		err = validateOneOpaqueState(
			messages,
			origin.message.ID,
			origin.state,
			selectionCodec(ctx),
			e.opaqueStatePolicy.Profile,
		)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrOpaqueStateInvalidated) || e.opaqueStatePolicy.Invalidated != OpaqueDropInvalid {
			return nil, err
		}
		ref, refErr := MessageContentRef(before[id].message, selectionCodec(ctx))
		if refErr != nil {
			return nil, refErr
		}
		dropped = append(dropped, OpaqueStateDrop{Message: ref, StateID: id})
	}
	return dropped, nil
}

func dropOpaqueStates(payload AbstractPayload, dropped []OpaqueStateDrop) AbstractPayload {
	ids := make(map[string]bool, len(dropped))
	for _, drop := range dropped {
		ids[drop.StateID] = true
	}
	for _, segment := range [][]Message{payload.System, payload.History, payload.Tools, payload.Memory} {
		for i := range segment {
			extensions := segment[i].Extensions[:0]
			for _, extension := range segment[i].Extensions {
				state, ok := opaqueStateFromExtension(extension)
				if !ok || !ids[state.ID] {
					extensions = append(extensions, extension)
				}
			}
			segment[i].Extensions = extensions
		}
	}
	return payload
}

func recordOpaqueStateTransition(
	ctx context.Context,
	identity Descriptor,
	before, after AbstractPayload,
	drops []OpaqueStateDrop,
) error {
	trace := traceFromContext(ctx)
	if trace == nil {
		return nil
	}
	original, _ := ctx.Value(opaqueStateInputsKey{}).(map[string]opaqueStateOrigin)
	for _, drop := range drops {
		if origin, found := original[drop.StateID]; found {
			if err := captureTraceMessage(ctx, origin.message, "opaque-invalidation-input"); err != nil {
				return err
			}
		}
	}
	previous := semanticOutputMessages(ctx, before)
	current := semanticOutputMessages(ctx, after)
	for i, message := range current {
		if MessageEqual(previous[i], message) {
			continue
		}
		if err := recordOpaqueMessageTransition(ctx, trace, identity, previous[i], message); err != nil {
			return err
		}
	}
	return nil
}

func cloneOpaqueState(state OpaqueState) OpaqueState {
	cloned, _ := state.CloneExtension().(OpaqueState)
	return cloned
}

func (e *Engine) initializeOpaqueCompileContext(ctx context.Context, req CompileRequest) (context.Context, error) {
	ctx, err := initializeOpaqueStateContext(ctx, req, e.opaqueStatePolicy)
	if err != nil {
		return ctx, err
	}
	codec := DefaultJSONSerializer()
	if e.trace != nil {
		codec = e.trace.Codec
	}
	return initializeArtifactMaterializationContext(ctx, e.artifactMaterialization, codec)
}

func recordOpaqueMessageTransition(
	ctx context.Context,
	trace *compileTrace,
	identity Descriptor,
	previous, message Message,
) error {
	input, err := trace.inputRef(previous)
	if err != nil {
		return err
	}
	output, err := MessageContentRef(message, trace.profile.Codec)
	if err != nil {
		return err
	}
	output.Occurrence = trace.nextID("opaque-invalidation")
	if err = trace.appendRecord(
		LineageRecord{
			ID:          output.Occurrence,
			Transform:   identity,
			Inputs:      []ContentRef{input},
			Outputs:     []ContentRef{output},
			Stage:       "opaque-invalidation",
			DecisionRef: "",
		},
	); err != nil {
		return err
	}
	trace.latest[baseContentRef(output)] = output
	if recorder := transformRecorderFrom(ctx); recorder != nil {
		recorder.set(message.ID, ActionFormatted, "opaque_state_dropped")
	}
	if err = captureTraceMessage(ctx, previous, "opaque-invalidation-input"); err != nil {
		return err
	}
	if err = captureTraceMessage(ctx, message, "opaque-invalidation"); err != nil {
		return err
	}
	return nil
}

func (e *Engine) acceptOpaqueState(ctx context.Context, payload AbstractPayload) (AbstractPayload, error) {
	accepted, drops, err := e.enforceOpaqueState(ctx, payload)
	if err != nil {
		return AbstractPayload{}, err
	}
	if e.opaqueStatePolicy == nil {
		return accepted, nil
	}
	if err = recordOpaqueStateTransition(ctx, e.opaqueStatePolicy.Identity, payload, accepted, drops); err != nil {
		return AbstractPayload{}, err
	}
	if err = recordOpaqueStateDecision(ctx, e.opaqueStatePolicy, payload, accepted, drops); err != nil {
		return AbstractPayload{}, err
	}
	return accepted, nil
}

func validateOpaqueOrdering(messages []Message, original map[string]opaqueStateOrigin) error {
	for _, message := range messages {
		previous := -1
		for _, extension := range message.Extensions {
			state, ok := opaqueStateFromExtension(extension)
			if !ok {
				continue
			}
			origin, found := original[state.ID]
			if !found || origin.message.ID != message.ID {
				return ErrInvalidOpaqueState
			}
			if origin.order <= previous {
				return ErrInvalidOpaqueState
			}
			previous = origin.order
		}
	}
	return nil
}
