package contexty_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skosovsky/contexty"
)

func fixtureArtifactLabelRegistry() *contexty.ExtensionRegistry {
	registry := contexty.NewExtensionRegistry()
	registry.Register("fixture-label", func(data []byte) (contexty.Extension, error) {
		return fixtureWireExtension{wire: string(data)}, nil
	})
	return registry
}

func fixtureBlobArtifactRequest() contexty.BlobArtifactRequest {
	artifact := contexty.NewMemoryBlock(
		"large",
		contexty.TextPayload(strings.Repeat("private ", 1024)),
	).ContextArtifact.
		WithBudget(
			contexty.ArtifactBudgetPolicy{TokenLimit: 2},
		)
	artifact.SourceRefs = []contexty.SourceRef{{ID: "host-source"}}
	return contexty.BlobArtifactRequest{ID: "prepare-artifact", Artifact: artifact,
		ScopeRef: "write-scope", RetentionRef: "retention", MaxPreviewBytes: 2,
		AllowedPreviewMediaTypes: []string{fixtureBlobMIME}}
}

type fixtureBlobPolicy func(context.Context, contexty.BlobOffloadCandidate) (contexty.BlobOffloadDecision, error)

func (f fixtureBlobPolicy) SelectBlob(
	ctx context.Context,
	candidate contexty.BlobOffloadCandidate,
) (contexty.BlobOffloadDecision, error) {
	return f(ctx, candidate)
}

type fixtureBlobPreviewer func(context.Context, contexty.BlobContent) (contexty.BlobContent, error)

func (f fixtureBlobPreviewer) PreviewBlob(
	ctx context.Context,
	content contexty.BlobContent,
) (contexty.BlobContent, error) {
	return f(ctx, content)
}

func fixtureOffloadRequest(t *testing.T) contexty.BlobOffloadRequest {
	t.Helper()
	return contexty.BlobOffloadRequest{Put: fixtureBlobRequest(t), MaxPreviewBytes: 2,
		AllowedPreviewMediaTypes: []string{fixtureBlobMIME}}
}

type fixtureBlobDecoder func(context.Context, contexty.BlobContent) ([]contexty.Message, error)

func (f fixtureBlobDecoder) DecodeBlob(ctx context.Context, content contexty.BlobContent) ([]contexty.Message, error) {
	return f(ctx, content)
}

func fixtureBlobResolver(t *testing.T) (contexty.BlobResolver, contexty.BlobResolveRequest) {
	t.Helper()
	put := fixtureBlobRequest(t)
	reporter, err := contexty.NewEstimateReporter(
		contexty.CharTokenEstimator{},
		fixtureEstimateProfile(),
		contexty.DefaultJSONSerializer(),
	)
	require.NoError(t, err)
	resolver := contexty.BlobResolver{
		Storage: fixtureBlobStore{get: func(context.Context, contexty.BlobGetRequest) (contexty.BlobContent, error) {
			return put.Content, nil
		}},
		Decoder: fixtureBlobDecoder(func(_ context.Context, content contexty.BlobContent) ([]contexty.Message, error) {
			message := contexty.TextMessage(contexty.RoleUser, string(content.Bytes))
			message.ID = "decoded"
			return []contexty.Message{message}, nil
		}),
		DecoderIdentity: contexty.Descriptor{ID: "host-decode", Revision: "pinned"}, Reporter: reporter,
	}
	request := contexty.BlobResolveRequest{ID: "resolution", ScopeRef: "fresh-read", Blob: fixtureBlobReceipt(put),
		Limits: contexty.BlobLimits{MaxBytes: 100, AllowedMediaTypes: []string{fixtureBlobMIME}},
		Budget: contexty.WindowInputBudget(10, 2, 1)}
	return resolver, request
}

func fixtureBlobDecodeScenario(scenario string, failure error, cancel context.CancelFunc) ([]contexty.Message, error) {
	if scenario == "decoder-error" {
		return nil, failure
	}
	if scenario == "decoder-cancel" {
		cancel()
	}
	message := contexty.TextMessage(contexty.RoleUser, "payload")
	message.ID = "decoded"
	if scenario == "unknown-cost" {
		message.Parts = []contexty.ContentPart{contexty.MediaPart{MIMEType: "audio/wav", Data: []byte{1}}}
	}
	if scenario == "duplicate-id" {
		return []contexty.Message{message, message}, nil
	}
	return []contexty.Message{message}, nil
}

const fixtureBlobMIME = "text/plain"

type fixtureBlobStore struct {
	put func(context.Context, contexty.BlobPutRequest) (contexty.BlobDescriptor, error)
	get func(context.Context, contexty.BlobGetRequest) (contexty.BlobContent, error)
}

func (s fixtureBlobStore) Put(ctx context.Context, request contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
	return s.put(ctx, request)
}

func (s fixtureBlobStore) Get(ctx context.Context, request contexty.BlobGetRequest) (contexty.BlobContent, error) {
	return s.get(ctx, request)
}

func fixtureBlobReceipt(request contexty.BlobPutRequest) contexty.BlobDescriptor {
	digest := sha256.Sum256(request.Content.Bytes)
	return contexty.BlobDescriptor{
		Object: contexty.Descriptor{ID: "opaque-object", Revision: "immutable"},
		Digest: hex.EncodeToString(
			digest[:],
		),
		Length:       int64(len(request.Content.Bytes)),
		MIMEType:     request.Content.MIMEType,
		ScopeRef:     request.ScopeRef,
		RetentionRef: request.RetentionRef,
		Sources:      append([]contexty.ContentRef(nil), request.Sources...),
	}
}

func fixtureBlobRequest(t *testing.T) contexty.BlobPutRequest {
	t.Helper()
	return contexty.BlobPutRequest{ScopeRef: "write-scope", RetentionRef: "retention",
		Content: contexty.BlobContent{MIMEType: fixtureBlobMIME, Bytes: []byte("payload")},
		Sources: []contexty.ContentRef{fixtureRef(t, "source", "source")}}
}
