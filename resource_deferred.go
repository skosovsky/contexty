package contexty

import (
	"context"
	"slices"
)

// ResourceSelection is the declared dependency before lazy resolution. It holds
// no body, access scope, reader handle or executable capability.
type ResourceSelection struct {
	ID            string                `json:"id"`
	Resource      ResourceDescriptor    `json:"resource"`
	Configuration ResourceConfiguration `json:"configuration"`
	Budget        BudgetRequest         `json:"budget"`
	MaxBytes      int64                 `json:"max_bytes"`
}

func (s ResourceSelection) Validate() error {
	if s.ID == "" || s.Resource.Validate() != nil || s.MaxBytes < s.Resource.Length {
		return ErrInvalidResource
	}
	if _, err := s.Budget.Resolve(); err != nil {
		return err
	}
	return s.Configuration.Validate()
}

func cloneResourceSelections(selections []ResourceSelection) []ResourceSelection {
	result := slices.Clone(selections)
	for i := range result {
		result[i].Configuration = result[i].Configuration.Clone()
	}
	return result
}

func validateResourceSelections(selections []ResourceSelection) error {
	ids := make(map[string]bool)
	for _, selection := range selections {
		if err := selection.Validate(); err != nil {
			return err
		}
		if ids[selection.ID] {
			return ErrInvalidResource
		}
		ids[selection.ID] = true
	}
	return nil
}

func (e *Engine) validateDeferredResources() error {
	var selections []ResourceSelection
	for _, block := range e.deferred {
		if len(block.Resources) != 0 && block.Resolve == nil {
			return ErrInvalidResource
		}
		for _, selection := range block.Resources {
			if err := selection.Validate(); err != nil {
				return err
			}
			if err := block.ResourceCodec.validateConfiguration(selection.Configuration); err != nil {
				return err
			}
		}
		selections = append(selections, block.Resources...)
	}
	return validateResourceSelections(selections)
}

func (e *Engine) resolvedDeferredMessages(
	ctx context.Context,
	block DeferredBlock,
	result DeferredResult,
) ([]Message, error) {
	if len(block.Resources) != len(result.Resources) {
		return nil, ErrMissingReplayDependency
	}
	if err := validateResourceMessageIdentities(result); err != nil {
		return nil, err
	}
	messages := cloneMessageSlice(result.Messages)
	for index, resource := range result.Resources {
		frozen, err := validateDeferredResource(ctx, block.Resources[index], resource, block.ResourceCodec)
		if err != nil {
			return nil, err
		}
		resource = frozen
		if err = traceDeferredResource(ctx, resource); err != nil {
			return nil, err
		}
		admitted, err := e.admitDeferredResource(ctx, resource)
		if err != nil {
			return nil, err
		}
		if err = captureResolvedResource(ctx, resource); err != nil {
			return nil, err
		}
		if admitted {
			messages = append(messages, resourceMaterialization(ctx, resource))
		}
	}
	return removeReplacedResourceContent(ctx, messages), ctx.Err()
}

func validateResourceMessageIdentities(result DeferredResult) error {
	for _, resource := range result.Resources {
		for _, message := range result.Messages {
			if message.ID == resource.Message.ID {
				return ErrInvalidResource
			}
		}
	}
	return nil
}

func validateDeferredResource(ctx context.Context, selection ResourceSelection,
	resource ResolvedResource, codec ResourceCodec,
) (ResolvedResource, error) {
	frozen, err := resource.Clone()
	if err != nil {
		return ResolvedResource{}, err
	}
	if frozen.ID != selection.ID || frozen.Resource != selection.Resource ||
		frozen.Estimate.Budget != selection.Budget {
		return ResolvedResource{}, ErrResourceMismatch
	}
	expected, err := selection.Configuration.Ref()
	if err != nil {
		return ResolvedResource{}, err
	}
	actual, err := frozen.Configuration.Ref()
	if err != nil || actual != expected {
		return ResolvedResource{}, ErrReplayMismatch
	}
	if err = frozen.Validate(ctx, codec); err != nil {
		return ResolvedResource{}, err
	}
	return frozen, nil
}

func (e *Engine) deferredResourceSelections() []ResourceSelection {
	var selections []ResourceSelection
	for _, block := range e.deferred {
		selections = append(selections, cloneResourceSelections(block.Resources)...)
	}
	return selections
}

func traceDeferredResource(ctx context.Context, resource ResolvedResource) error {
	trace := traceFromContext(ctx)
	if trace == nil {
		return nil
	}
	message, err := MessageContentRef(resource.Message, trace.profile.Codec)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil || message != resource.Estimate.Segments[0].Messages[0] {
		return ErrReplayCodec
	}
	for _, record := range resource.Lineage.Records {
		if err := trace.appendRecord(record); err != nil {
			return err
		}
		for _, ref := range record.Outputs {
			trace.latest[baseContentRef(ref)] = ref
		}
	}
	return ctx.Err()
}
