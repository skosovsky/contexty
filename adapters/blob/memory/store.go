// Package memory provides an application-owned reference blob backend.
// It is ephemeral, not a durable checkpoint coordinator or an authorization system.
package memory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"strconv"
	"sync"

	"github.com/skosovsky/contexty"
)

var (
	ErrConfiguration = errors.New("memory blob: invalid configuration")
	ErrClaimConflict = errors.New("memory blob: retention identity conflict")
	ErrActiveClaim   = errors.New("memory blob: active retention claim")
	ErrRetiring      = errors.New("memory blob: object/source retiring")
)

type Action string

const (
	ActionPut          Action = "put"
	ActionGet          Action = "get"
	ActionCheck        Action = "check"
	ActionRetain       Action = "retain"
	ActionCommit       Action = "commit"
	ActionRelease      Action = "release"
	ActionRetireSource Action = "retire_source"
	ActionCleanup      Action = "cleanup"
)

// Access is interpreted only by the application's authorization callback.
type Access struct {
	Action     Action
	ScopeRef   string
	Object     contexty.Descriptor
	Checkpoint contexty.Descriptor
	ClaimRef   string
	Source     contexty.ContentRef
}

type Config struct {
	Namespace      string
	MaxObjectBytes int64
	Authorize      func(context.Context, Access) error
}

type claimState uint8

const (
	claimPending claimState = iota
	claimCommitted
	claimReleased
)

type claim struct {
	object     contexty.Descriptor
	checkpoint contexty.Descriptor
	scopeRef   string
	state      claimState
}

type object struct {
	descriptor contexty.BlobDescriptor
	content    contexty.BlobContent
	retiring   bool
}

type checkpoint struct {
	claims   []string
	released bool
}

// Store serializes retention/deletion under one mutex. Object/claim/checkpoint
// identities are never reused, including after cleanup. Authorize runs outside
// the lock; state and cancellation are rechecked under the lock before mutation.
type Store struct {
	mu             sync.Mutex
	config         Config
	incarnation    string
	sequence       uint64
	objects        map[contexty.Descriptor]object
	claims         map[string]claim
	checkpoints    map[contexty.Descriptor]checkpoint
	deletedSources map[contexty.ContentRef]bool
}

func New(config Config) (*Store, error) {
	if config.Namespace == "" || config.MaxObjectBytes < 0 || config.Authorize == nil {
		return nil, ErrConfiguration
	}
	return &Store{mu: sync.Mutex{}, config: config, incarnation: rand.Text(), sequence: 0,
		objects: make(map[contexty.Descriptor]object), claims: make(map[string]claim),
		checkpoints: make(map[contexty.Descriptor]checkpoint), deletedSources: make(map[contexty.ContentRef]bool)}, nil
}

func (s *Store) authorize(ctx context.Context, access Access) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.config.Authorize == nil || access.ScopeRef == "" {
		return contexty.ErrBlobDenied
	}
	err := s.config.Authorize(ctx, access)
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	return err
}

func (s *Store) Put(ctx context.Context, request contexty.BlobPutRequest) (contexty.BlobDescriptor, error) {
	request.Content.Bytes = slices.Clone(request.Content.Bytes)
	request.Sources = slices.Clone(request.Sources)
	digest := sha256.Sum256(request.Content.Bytes)
	descriptor := contexty.BlobDescriptor{
		Object: contexty.Descriptor{ID: "candidate", Revision: "immutable"},
		Digest: hex.EncodeToString(
			digest[:],
		),
		Length:       int64(len(request.Content.Bytes)),
		MIMEType:     request.Content.MIMEType,
		ScopeRef:     request.ScopeRef,
		RetentionRef: request.RetentionRef,
		Sources:      request.Sources,
	}
	if err := descriptor.Validate(); err != nil {
		return contexty.BlobDescriptor{}, err
	}
	access := Access{
		Action:   ActionPut,
		ScopeRef: request.ScopeRef,
		ClaimRef: request.RetentionRef,
		Object: contexty.Descriptor{
			ID:       "",
			Revision: "",
		},
		Checkpoint: contexty.Descriptor{ID: "", Revision: ""},
		Source:     contexty.ContentRef{ID: "", Digest: "", Occurrence: ""},
	}
	if err := s.authorize(ctx, access); err != nil {
		return contexty.BlobDescriptor{}, err
	}
	return s.putLocked(ctx, request, descriptor)
}

func (s *Store) putLocked(ctx context.Context, request contexty.BlobPutRequest,
	descriptor contexty.BlobDescriptor,
) (contexty.BlobDescriptor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return contexty.BlobDescriptor{}, err
	}
	if descriptor.Length > s.config.MaxObjectBytes {
		return contexty.BlobDescriptor{}, contexty.ErrBlobSizeLimit
	}
	if _, exists := s.claims[request.RetentionRef]; exists || s.sequence == math.MaxUint64 {
		return contexty.BlobDescriptor{}, ErrClaimConflict
	}
	for _, source := range descriptor.Sources {
		if s.deletedSources[source] {
			return contexty.BlobDescriptor{}, ErrRetiring
		}
	}
	s.sequence++
	descriptor.Object = contexty.Descriptor{
		ID:       s.config.Namespace + "/" + s.incarnation + "/" + strconv.FormatUint(s.sequence, 10),
		Revision: "immutable",
	}
	s.objects[descriptor.Object] = object{descriptor: descriptor.Clone(), content: request.Content, retiring: false}
	s.claims[request.RetentionRef] = claim{
		object:     descriptor.Object,
		checkpoint: contexty.Descriptor{ID: "", Revision: ""},
		scopeRef:   descriptor.ScopeRef,
		state:      claimPending,
	}
	return descriptor.Clone(), nil
}

func (s *Store) Get(ctx context.Context, request contexty.BlobGetRequest) (contexty.BlobContent, error) {
	if request.Object.Validate() != nil || request.MaxBytes < 0 {
		return contexty.BlobContent{}, contexty.ErrInvalidBlob
	}
	if err := s.authorize(ctx, Access{
		Action:   ActionGet,
		ScopeRef: request.ScopeRef,
		Object:   request.Object,
		Checkpoint: contexty.Descriptor{
			ID:       "",
			Revision: "",
		},
		ClaimRef: "",
		Source:   contexty.ContentRef{ID: "", Digest: "", Occurrence: ""},
	}); err != nil {
		return contexty.BlobContent{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return contexty.BlobContent{}, err
	}
	value, exists := s.objects[request.Object]
	if !exists {
		return contexty.BlobContent{}, contexty.ErrBlobMissing
	}
	if value.descriptor.Length > request.MaxBytes {
		return contexty.BlobContent{}, contexty.ErrBlobSizeLimit
	}
	return contexty.BlobContent{MIMEType: value.content.MIMEType, Bytes: slices.Clone(value.content.Bytes)}, nil
}

func (s *Store) CheckBlob(ctx context.Context, request contexty.BlobAvailabilityRequest) error {
	request.Blob = request.Blob.Clone()
	if err := request.Blob.Validate(); err != nil {
		return err
	}
	if err := s.authorize(ctx, Access{
		Action:     ActionCheck,
		ScopeRef:   request.ScopeRef,
		Object:     request.Blob.Object,
		Checkpoint: contexty.Descriptor{ID: "", Revision: ""},
		ClaimRef:   request.Blob.RetentionRef,
		Source:     contexty.ContentRef{ID: "", Digest: "", Occurrence: ""},
	}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	value, exists := s.objects[request.Blob.Object]
	if !exists {
		return contexty.ErrBlobMissing
	}
	retained, knownClaim := s.claims[request.Blob.RetentionRef]
	if !knownClaim || retained.object != request.Blob.Object || retained.scopeRef != request.Blob.ScopeRef {
		return contexty.ErrBlobDigestMismatch
	}
	if value.descriptor.Digest != request.Blob.Digest || value.descriptor.Length != request.Blob.Length ||
		value.descriptor.MIMEType != request.Blob.MIMEType || value.descriptor.ScopeRef != request.Blob.ScopeRef ||
		!slices.Equal(value.descriptor.Sources, request.Blob.Sources) {
		return contexty.ErrBlobDigestMismatch
	}
	return nil
}

var (
	_ contexty.BlobStore        = (*Store)(nil)
	_ contexty.BlobAvailability = (*Store)(nil)
)
