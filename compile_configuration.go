package contexty

import (
	"context"
	"encoding/json"
	"slices"
)

type DeferredConfiguration struct {
	Resources   []ResourceSelection `json:"resources,omitempty"`
	Index       int                 `json:"index"`
	Name        string              `json:"name"`
	Segment     SegmentName         `json:"segment"`
	MergePolicy MergePolicy         `json:"merge_policy"`
}

// CompileConfiguration contains identities, never option values or executions.
type CompileConfiguration struct {
	Options  ContentRef              `json:"options"`
	Deferred []DeferredConfiguration `json:"deferred"`
}

type compileOptionIdentityKey struct{}

const compileOptionsIdentity = "compile/options"

func compileOptionsRef(options compileOptions) (ContentRef, error) {
	wire, err := json.Marshal(struct {
		Replacements        []TextReplacement              `json:"replacements"`
		ResolveVars         map[string]string              `json:"resolve_vars"`
		HistoricalArguments []HistoricalArgumentProjection `json:"historical_arguments"`
	}{Replacements: options.replacements, ResolveVars: options.resolveVars, HistoricalArguments: options.historicalArguments})
	if err != nil {
		return ContentRef{}, err
	}
	digest, err := canonicalJSONDigest(wire)
	return ContentRef{ID: compileOptionsIdentity, Digest: digest, Occurrence: ""}, err
}

func recordCompileOptions(ctx context.Context, options compileOptions) (context.Context, error) {
	ref, err := compileOptionsRef(options)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, compileOptionIdentityKey{}, ref), nil
}

func (e *Engine) prepareCompileOptions(
	ctx context.Context,
	options []CompileOption,
) (context.Context, compileOptions, error) {
	evaluated := applyCompileOptions(options)
	for _, replacement := range evaluated.replacements {
		if !isKnownSegment(replacement.Segment) || replacement.MessageID == "" {
			return ctx, evaluated, ErrInvalidTextReplacement
		}
	}
	if e.recording != nil {
		var err error
		ctx, err = recordCompileOptions(ctx, evaluated)
		if err != nil {
			return ctx, evaluated, err
		}
	}
	return withCompileResolveVars(ctx, evaluated.resolveVars), evaluated, nil
}

func (e *Engine) deferredConfiguration() ([]DeferredConfiguration, error) {
	var configs []DeferredConfiguration
	for index, block := range e.deferred {
		if block.Resolve == nil {
			continue
		}
		segment := block.Segment
		if segment == "" {
			segment = SegmentMemory
		}
		if !isKnownSegment(segment) {
			return nil, ErrInvalidRecordingComponent
		}
		policy := block.MergePolicy
		if policy == "" {
			policy = PolicyAppend
		}
		switch policy {
		case PolicyAppend, PolicyReplaceByOrigin, PolicyDeduplicateByLayer:
		default:
			return nil, ErrInvalidRecordingComponent
		}
		configs = append(
			configs,
			DeferredConfiguration{Index: index, Name: block.Name, Segment: segment, MergePolicy: policy,
				Resources: cloneResourceSelections(block.Resources)},
		)
	}
	return configs, nil
}

func (e *Engine) compileConfiguration(ctx context.Context) (CompileConfiguration, error) {
	ref, found := ctx.Value(compileOptionIdentityKey{}).(ContentRef)
	if !found {
		return CompileConfiguration{}, ErrInvalidRecordingComponent
	}
	deferred, err := e.deferredConfiguration()
	return CompileConfiguration{Options: ref, Deferred: deferred}, err
}

func (c CompileConfiguration) clone() CompileConfiguration {
	c.Deferred = slices.Clone(c.Deferred)
	for i := range c.Deferred {
		c.Deferred[i].Resources = cloneResourceSelections(c.Deferred[i].Resources)
	}
	return c
}

func (c CompileConfiguration) validate(profile RecordProfile) error {
	if c.Options.ID != compileOptionsIdentity || c.Options.Occurrence != "" {
		return ErrInvalidRecordingComponent
	}
	if err := c.Options.Validate(); err != nil {
		return err
	}
	indices := make(map[int]bool)
	var resources []ResourceSelection
	prior := -1
	for _, deferred := range c.Deferred {
		if err := validateResourceSelections(deferred.Resources); err != nil {
			return err
		}
		if deferred.Index <= prior || !isKnownSegment(deferred.Segment) {
			return ErrInvalidRecordingComponent
		}
		switch deferred.MergePolicy {
		case PolicyAppend, PolicyReplaceByOrigin, PolicyDeduplicateByLayer:
		default:
			return ErrInvalidRecordingComponent
		}
		indices[deferred.Index] = true
		resources = append(resources, deferred.Resources...)
		prior = deferred.Index
	}
	if err := validateResourceSelections(resources); err != nil {
		return err
	}
	for _, component := range profile.Components {
		if component.Key.Kind != RecordingResolver {
			continue
		}
		if !indices[component.Key.Index] {
			return ErrInvalidRecordingComponent
		}
		delete(indices, component.Key.Index)
	}
	if len(indices) != 0 {
		return ErrInvalidRecordingComponent
	}
	return nil
}
