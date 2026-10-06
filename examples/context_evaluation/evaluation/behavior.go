package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/skosovsky/contexty"
	blobmemory "github.com/skosovsky/contexty/adapters/blob/memory"
	resourcememory "github.com/skosovsky/contexty/adapters/resource/memory"
)

const (
	behaviorLookup     = "lookup"
	behaviorAllowed    = "allowed"
	behaviorTextMIME   = "text/plain"
	behaviorOpaqueType = "behavior/opaque-bytes"
)

// behaviorChecks exercises actual contract failures; these are not model quality scores.
func behaviorChecks(ctx context.Context, fixture Fixture, budget int) ([]Check, error) {
	switch fixture.CaseKind {
	case "partial-round":
		return behaviorRound(ctx)
	case "resource-access":
		return behaviorResource(ctx, budget)
	case "blob-access":
		return behaviorBlob(ctx, budget)
	case "consumers":
		return behaviorConsumers(ctx, fixture.Messages)
	case "opaque-invalidation":
		return behaviorOpaque(ctx)
	default:
		return nil, nil
	}
}

//nolint:exhaustruct_v5 // Mechanical fixture enables message capacity only.
func behaviorBudget(limit int) *contexty.BudgetPipeline {
	return contexty.NewBudgetPipeline(
		contexty.BudgetConfig{Budget: contexty.EffectiveInputBudget(limit)},
		&contexty.FixedEstimator{
			TokensPerMessage: 1,
		},
	)
}

//nolint:exhaustruct_v5 // Partial protocol fixture deliberately omits unrelated compile options.
func behaviorRound(ctx context.Context) ([]Check, error) {
	call := contexty.Message{ID: "behavior-call", Role: contexty.RoleAssistant, Parts: []contexty.ContentPart{
		contexty.ToolCallPart{
			ID:   "first",
			Name: behaviorLookup,
		},
		contexty.ToolCallPart{ID: "second", Name: behaviorLookup},
	}}
	result := contexty.Message{ID: "behavior-result", Role: contexty.RoleTool, Parts: []contexty.ContentPart{
		contexty.ToolResultPart{
			ToolCallID: "first",
			Name:       behaviorLookup,
			Payload:    contexty.TextPayload("first only"),
		},
	}}
	request := contexty.CompileRequest{
		History: []contexty.Message{call, result},
	}
	engine := contexty.NewEngine(contexty.WithBudgetPipeline(behaviorBudget(2)))
	compiled, err := engine.CompileSnapshot(ctx, request)
	if err != nil {
		return nil, err
	}
	_, tightErr := contexty.NewEngine(contexty.WithBudgetPipeline(behaviorBudget(1))).
		CompileSnapshot(ctx, request)
	pending := contexty.TextMessage(contexty.RoleUser, "pending request")
	pending.ID = "behavior-pending"
	protected, pendingErr := contexty.NewEngine(contexty.WithBudgetPipeline(behaviorBudget(1))).
		CompileSnapshot(ctx,
			contexty.CompileRequest{
				History: []contexty.Message{contexty.TextMessage(contexty.RoleUser, "old")},
				Pending: []contexty.Message{pending},
			})
	if pendingErr != nil {
		return nil, pendingErr
	}
	return []Check{
		{
			Kind: "partial-round-preserved",
			Passed: len(compiled.Payload.History) == 2 && contexty.MessageEqual(compiled.Payload.History[0], call) &&
				contexty.MessageEqual(compiled.Payload.History[1], result),
			Detail: "Two-call partial round remains exact; no second result is fabricated.",
		},
		{
			Kind:   "partial-round-fail-closed",
			Passed: errors.Is(tightErr, contexty.ErrPendingExceedsBudget),
			Detail: fmt.Sprintf("Insufficient capacity returns %v.", tightErr),
		},
		{
			Kind:   "pending-preserved",
			Passed: len(protected.Payload.History) == 1 && protected.Payload.History[0].ID == pending.ID,
			Detail: "Pending input survives eviction of older history.",
		},
	}, nil
}

type behaviorProjection struct{}

func (behaviorProjection) ProjectResource(
	_ context.Context,
	body contexty.ResourceBody,
) (contexty.ContextArtifact, error) {
	return body.Artifact.Clone(), nil
}

//nolint:exhaustruct_v5 // Fixed local fixture byte limits and minimal resolver configuration.
func behaviorResource(ctx context.Context, budget int) ([]Check, error) {
	artifact := contexty.NewRetrievalDocument(
		"behavior-resource",
		contexty.TextPayload("fixture evidence"),
	).ContextArtifact
	reference := identity("behavior-resource-body")
	body := contexty.ResourceBody{Reference: reference, Artifact: artifact}
	descriptor, err := contexty.DescribeResource(reference, "selected", artifact)
	if err != nil {
		return nil, err
	}
	reader, err := resourcememory.New(
		resourcememory.Config{
			MaxBodyBytes: descriptor.Length,
			Authorize: func(_ context.Context, r contexty.ResourceReadRequest) error {
				if r.ScopeRef != behaviorAllowed {
					return contexty.ErrResourceDenied
				}
				return nil
			},
		},
		body,
	)
	if err != nil {
		return nil, err
	}
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		OfflineProfile(),
		contexty.DefaultJSONSerializer(),
	)
	if err != nil {
		return nil, err
	}
	materialization := contexty.ArtifactMaterializationPolicy{
		Identity: identity("behavior-resource-materialize"),
		Materialize: func(_ context.Context, a contexty.ContextArtifact) (contexty.ArtifactRepresentation, error) {
			parts, e := contexty.ArtifactContentParts(a)
			return contexty.ArtifactRepresentation{Role: contexty.RoleUser, Parts: parts}, e
		},
	}
	resolver := contexty.ResourceResolver{
		Reader:             reader,
		ReaderIdentity:     identity("behavior-memory-reader"),
		Projection:         behaviorProjection{},
		ProjectionIdentity: identity("behavior-identity-projection"),
		Materialization:    &materialization,
		Reporter:           reporter,
	}
	request := contexty.ResourceResolveRequest{
		ID: "behavior-selected",
		Read: contexty.ResourceReadRequest{
			ScopeRef: behaviorAllowed,
			Resource: descriptor,
			MaxBytes: descriptor.Length,
		},
		Budget: contexty.EffectiveInputBudget(budget),
	}
	valid, err := resolver.Resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	request.Read.Resource.Reference.Revision = "stale"
	stale, staleErr := resolver.Resolve(ctx, request)
	request.Read.Resource = descriptor
	request.Read.ScopeRef = "denied"
	denied, deniedErr := resolver.Resolve(ctx, request)
	return []Check{
		{
			Kind: "resource-access",
			Passed: valid.Message.ID != "" && errors.Is(staleErr, contexty.ErrResourceMismatch) &&
				errors.Is(deniedErr, contexty.ErrResourceDenied) &&
				stale.Message.ID == "" &&
				denied.Message.ID == "",
			Detail: "Memory reader and ResourceResolver reject stale revision and denied scope before exposing materialized content.",
		},
	}, nil
}

type behaviorBlobDecoder struct{ calls *int }

func (d behaviorBlobDecoder) DecodeBlob(_ context.Context, c contexty.BlobContent) ([]contexty.Message, error) {
	*d.calls++
	m := contexty.TextMessage(contexty.RoleUser, string(c.Bytes))
	m.ID = "behavior-restored"
	return []contexty.Message{m}, nil
}

//nolint:exhaustruct_v5,mnd // Fixed local fixture byte limits and source-free ephemeral blob.
func behaviorBlob(ctx context.Context, budget int) ([]Check, error) {
	store, err := blobmemory.New(
		blobmemory.Config{
			Namespace:      "behavior",
			MaxObjectBytes: 256,
			Authorize: func(_ context.Context, a blobmemory.Access) error {
				if a.ScopeRef != behaviorAllowed {
					return contexty.ErrBlobDenied
				}
				return nil
			},
		},
	)
	if err != nil {
		return nil, err
	}
	blob, err := store.Put(
		ctx,
		contexty.BlobPutRequest{
			ScopeRef:     behaviorAllowed,
			RetentionRef: "behavior-claim",
			Content:      contexty.BlobContent{MIMEType: behaviorTextMIME, Bytes: []byte("original evidence")},
		},
	)
	if err != nil {
		return nil, err
	}
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		OfflineProfile(),
		contexty.DefaultJSONSerializer(),
	)
	if err != nil {
		return nil, err
	}
	calls := 0
	resolver := contexty.BlobResolver{
		Storage:         store,
		Decoder:         behaviorBlobDecoder{calls: &calls},
		DecoderIdentity: identity("behavior-text-decoder"),
		Reporter:        reporter,
	}
	request := contexty.BlobResolveRequest{
		ID:       "behavior-blob",
		ScopeRef: behaviorAllowed,
		Blob:     blob,
		Limits:   contexty.BlobLimits{MaxBytes: 256, AllowedMediaTypes: []string{behaviorTextMIME}},
		Budget:   contexty.EffectiveInputBudget(budget),
	}
	valid, err := resolver.Resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	request.Blob.Digest = "0000000000000000000000000000000000000000000000000000000000000000"
	stale, staleErr := resolver.Resolve(ctx, request)
	request.Blob = blob
	request.ScopeRef = "denied"
	denied, deniedErr := resolver.Resolve(ctx, request)
	return []Check{
		{
			Kind: "blob-access",
			Passed: len(valid.Messages) == 1 && errors.Is(staleErr, contexty.ErrBlobDigestMismatch) &&
				errors.Is(deniedErr, contexty.ErrBlobDenied) &&
				len(stale.Messages) == 0 &&
				len(denied.Messages) == 0 &&
				calls == 1,
			Detail: "Verified BlobResolver restoration succeeds once; stale digest and denied scope never invoke the decoder or expose messages.",
		},
	}, nil
}

//nolint:exhaustruct_v5 // History-only consumer configuration.
func behaviorConsumers(ctx context.Context, messages []contexty.Message) ([]Check, error) {
	if len(messages) < 2 {
		return nil, errors.New("consumer fixture requires at least two messages")
	}
	request := contexty.CompileRequest{History: messages, Targets: []contexty.CompileTarget{
		{
			Name:     "full",
			Segments: []contexty.SegmentName{contexty.SegmentHistory},
			Budget:   behaviorBudget(len(messages)),
		},
		{Name: "small", Segments: []contexty.SegmentName{contexty.SegmentHistory}, Budget: behaviorBudget(1)},
	}}
	compiled, err := contexty.NewEngine(contexty.WithBudgetPipeline(behaviorBudget(1))).
		CompileSnapshot(ctx, request)
	if err != nil {
		return nil, err
	}
	full := compiled.Projections["full"]
	small := compiled.Projections["small"]
	return []Check{
		{
			Kind: "independent-consumers",
			Passed: len(compiled.PreparedSnapshot.Segment(contexty.SegmentHistory)) == len(messages) &&
				len(full.InputSnapshot.Segment(contexty.SegmentHistory)) == len(messages) &&
				len(small.InputSnapshot.Segment(contexty.SegmentHistory)) == len(messages) &&
				len(full.Messages) == len(messages) &&
				len(small.Messages) == 1 &&
				len(compiled.Payload.History) == 1,
			Detail: "Main and small consumer limits do not reduce the shared prepared history available to the full consumer.",
		},
	}, nil
}

type behaviorOpaquePayload struct {
	Bytes []byte `json:"bytes"`
}

func (behaviorOpaquePayload) ExtensionType() string { return behaviorOpaqueType }
func (p behaviorOpaquePayload) CloneExtension() contexty.Extension {
	return behaviorOpaquePayload{Bytes: slices.Clone(p.Bytes)}
}

//nolint:exhaustruct_v5 // Fixed fixture state bytes and minimal traced compilation.
func behaviorOpaque(ctx context.Context) ([]Check, error) {
	registry := contexty.NewExtensionRegistry()
	codecID := identity("behavior-opaque-codec")
	registry.RegisterOpaquePayload(behaviorOpaqueType, codecID, func(data []byte) (contexty.Extension, error) {
		var p behaviorOpaquePayload
		err := json.Unmarshal(data, &p)
		return p, err
	})
	codec := contexty.JSONSerializer{Provenance: contexty.DefaultProvenanceRegistry(), Extensions: registry}
	input := contexty.TextMessage(contexty.RoleUser, "bound evidence")
	input.ID = "behavior-input"
	ref, err := contexty.MessageContentRef(input, codec)
	if err != nil {
		return nil, err
	}
	carrier := contexty.TextMessage(contexty.RoleAssistant, "visible answer")
	carrier.ID = "behavior-carrier"
	profile := identity("behavior-host-profile")
	carrier.Extensions = []contexty.Extension{
		contexty.OpaqueState{
			ID:        "behavior-signature",
			Codec:     codecID,
			Payload:   behaviorOpaquePayload{Bytes: []byte{0, 255, 1}},
			Placement: contexty.OpaquePlacement{AfterPart: 0},
			Binding: contexty.OpaqueBinding{
				Profile:  profile,
				Required: []contexty.ContentRef{ref},
				Prefix:   []contexty.ContentRef{ref},
				Boundary: input.ID,
			},
		},
	}
	trace := contexty.TraceProfile{
		Encoding: identity("behavior-json"),
		Codec:    codec,
		Stages: map[string]contexty.Descriptor{
			"source":  identity("behavior-source"),
			"project": identity("behavior-project"),
		},
		Labels: contexty.LabelProjection{Registry: registry},
		Codecs: []contexty.CodecBinding{
			{
				Kind:       contexty.CodecExtension,
				Type:       contexty.OpaqueStateExtensionType,
				Descriptor: identity("behavior-opaque-envelope"),
			},
			{
				Kind:       contexty.CodecLabel,
				Type:       contexty.OpaqueStateExtensionType,
				Descriptor: identity("behavior-opaque-envelope"),
			},
			{
				Kind:       contexty.CodecExtension,
				Type:       behaviorOpaqueType,
				Descriptor: codecID,
			},
			{Kind: contexty.CodecLabel, Type: behaviorOpaqueType, Descriptor: codecID},
		},
	}
	engine := func(mode contexty.OpaqueInvalidationMode) *contexty.Engine {
		return contexty.NewEngine(
			contexty.WithTraceProfile(trace),
			contexty.WithOpaqueStatePolicy(
				contexty.OpaqueStatePolicy{
					Identity:    identity("behavior-opaque-lifecycle"),
					Profile:     profile,
					Invalidated: mode,
				},
			),
		)
	}
	request := contexty.CompileRequest{
		CompilationID: "behavior-opaque-compilation",
		History:       []contexty.Message{input, carrier},
	}
	accepted, err := engine(contexty.OpaqueFailClosed).CompileSnapshot(ctx, request)
	if err != nil {
		return nil, err
	}
	changed := contexty.TextMessage(contexty.RoleUser, "changed bound evidence")
	changed.ID = input.ID
	request.History[0] = changed
	_, rejected := engine(contexty.OpaqueFailClosed).CompileSnapshot(ctx, request)
	dropped, err := engine(contexty.OpaqueDropInvalid).CompileSnapshot(ctx, request)
	if err != nil {
		return nil, err
	}
	return []Check{
		{
			Kind: "opaque-invalidation",
			Passed: len(accepted.Payload.History[1].Extensions) == 1 &&
				errors.Is(rejected, contexty.ErrOpaqueStateInvalidated) &&
				len(dropped.Payload.History[1].Extensions) == 0,
			Detail: "Exact bound-input mutation rejects typed opaque state under fail-closed policy and removes it only under explicit host drop policy.",
		},
	}, nil
}
