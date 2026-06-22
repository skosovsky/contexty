package memory

import (
	"context"
	"slices"

	"github.com/skosovsky/contexty"
)

// Retain creates a new provisional claim for an existing object. A claim belongs
// to exactly one checkpoint; sharing an object requires a distinct claim per checkpoint.
func (s *Store) Retain(ctx context.Context, scope string, identity contexty.Descriptor, claimRef string) error {
	if identity.Validate() != nil || claimRef == "" {
		return ErrClaimConflict
	}
	if err := s.authorize(
		ctx,
		retentionAccess(ActionRetain, scope, identity, contexty.Descriptor{ID: "", Revision: ""}, claimRef),
	); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	value, exists := s.objects[identity]
	if !exists {
		return contexty.ErrBlobMissing
	}
	if value.retiring {
		return ErrRetiring
	}
	if _, exists = s.claims[claimRef]; exists {
		return ErrClaimConflict
	}
	s.claims[claimRef] = claim{
		object:     identity,
		checkpoint: contexty.Descriptor{ID: "", Revision: ""},
		scopeRef:   value.descriptor.ScopeRef,
		state:      claimPending,
	}
	return nil
}

// CommitCheckpoint atomically converts all provisional claims into checkpoint
// claims. Call only after an authoritative host checkpoint commit. An uncertain
// commit must keep provisional claims until the host reconciles its outcome.
func (s *Store) CommitCheckpoint(
	ctx context.Context,
	scope string,
	identity contexty.Descriptor,
	claimRefs []string,
) error {
	refs, err := normalizeClaims(identity, claimRefs)
	if err != nil {
		return err
	}
	if err = s.authorizeClaims(ctx, ActionCommit, scope, identity, refs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	if saved, exists := s.checkpoints[identity]; exists {
		if saved.released || !slices.Equal(saved.claims, refs) {
			return ErrClaimConflict
		}
		return nil
	}
	for _, ref := range refs {
		value, exists := s.claims[ref]
		if !exists || value.state != claimPending {
			return ErrClaimConflict
		}
		stored, exists := s.objects[value.object]
		if !exists {
			return contexty.ErrBlobMissing
		}
		if stored.retiring {
			return ErrRetiring
		}
	}
	for _, ref := range refs {
		value := s.claims[ref]
		value.state, value.checkpoint = claimCommitted, identity
		s.claims[ref] = value
	}
	s.checkpoints[identity] = checkpoint{claims: refs, released: false}
	return nil
}

func normalizeClaims(identity contexty.Descriptor, claimRefs []string) ([]string, error) {
	if identity.Validate() != nil || len(claimRefs) == 0 {
		return nil, ErrClaimConflict
	}
	refs := slices.Clone(claimRefs)
	slices.Sort(refs)
	for index, ref := range refs {
		if ref == "" || (index > 0 && refs[index-1] == ref) {
			return nil, ErrClaimConflict
		}
	}
	return refs, nil
}

func retentionAccess(action Action, scope string, objectID, checkpointID contexty.Descriptor, claimRef string) Access {
	return Access{Action: action, ScopeRef: scope, Object: objectID, Checkpoint: checkpointID,
		ClaimRef: claimRef, Source: contexty.ContentRef{ID: "", Digest: "", Occurrence: ""}}
}

// Authorize every actual object, not merely a caller-supplied checkpoint name.
// Claims never change object binding, so their snapshot remains valid after the
// callbacks; mutation still rechecks state under the mutex.
func (s *Store) authorizeClaims(
	ctx context.Context,
	action Action,
	scope string,
	identity contexty.Descriptor,
	refs []string,
) error {
	if err := s.authorize(
		ctx,
		retentionAccess(action, scope, contexty.Descriptor{ID: "", Revision: ""}, identity, ""),
	); err != nil {
		return err
	}
	s.mu.Lock()
	accesses := make([]Access, 0, len(refs))
	for _, ref := range refs {
		value, exists := s.claims[ref]
		if !exists {
			s.mu.Unlock()
			return ErrClaimConflict
		}
		accesses = append(accesses, retentionAccess(action, scope, value.object, identity, ref))
	}
	s.mu.Unlock()
	for _, access := range accesses {
		if err := s.authorize(ctx, access); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseCheckpoint is an explicit host acknowledgement that the checkpoint
// no longer needs its blobs. It releases claims, but never deletes objects.
func (s *Store) ReleaseCheckpoint(ctx context.Context, scope string, identity contexty.Descriptor) error {
	if identity.Validate() != nil {
		return ErrClaimConflict
	}
	if err := s.authorize(
		ctx,
		retentionAccess(ActionRelease, scope, contexty.Descriptor{ID: "", Revision: ""}, identity, ""),
	); err != nil {
		return err
	}
	s.mu.Lock()
	value, exists := s.checkpoints[identity]
	refs := slices.Clone(value.claims)
	s.mu.Unlock()
	if !exists {
		return ErrClaimConflict
	}
	if err := s.authorizeClaims(ctx, ActionRelease, scope, identity, refs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	value = s.checkpoints[identity]
	for _, ref := range value.claims {
		retained := s.claims[ref]
		retained.state = claimReleased
		s.claims[ref] = retained
	}
	value.released = true
	s.checkpoints[identity] = value
	return nil
}
