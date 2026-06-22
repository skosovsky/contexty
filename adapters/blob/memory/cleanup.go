package memory

import (
	"context"
	"slices"
	"strings"

	"github.com/skosovsky/contexty"
)

// RetireSource retires derivatives of this exact content revision, not all
// revisions of a logical ID. Existing claims remain active; new claims/commits
// and new writes derived from this source are rejected. Nothing is deleted here.
func (s *Store) RetireSource(
	ctx context.Context,
	scope string,
	source contexty.ContentRef,
) ([]contexty.Descriptor, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	access := retentionAccess(
		ActionRetireSource,
		scope,
		contexty.Descriptor{ID: "", Revision: ""},
		contexty.Descriptor{ID: "", Revision: ""},
		"",
	)
	access.Source = source
	if err := s.authorize(ctx, access); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.deletedSources[source] = true
	var affected []contexty.Descriptor
	for identity, value := range s.objects {
		if slices.Contains(value.descriptor.Sources, source) {
			value.retiring = true
			s.objects[identity] = value
			affected = append(affected, identity)
		}
	}
	slices.SortFunc(affected, func(left, right contexty.Descriptor) int {
		return strings.Compare(left.ID, right.ID)
	})
	return affected, nil
}

// Collect deletes only a retired object with no provisional or committed claims.
// Missing objects are idempotent success. It never releases a claim implicitly.
func (s *Store) Collect(ctx context.Context, scope string, identity contexty.Descriptor) (bool, error) {
	if err := identity.Validate(); err != nil {
		return false, err
	}
	if err := s.authorize(
		ctx,
		retentionAccess(ActionCleanup, scope, identity, contexty.Descriptor{ID: "", Revision: ""}, ""),
	); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return s.collectLocked(identity)
}

func (s *Store) collectLocked(identity contexty.Descriptor) (bool, error) {
	value, exists := s.objects[identity]
	if !exists {
		return false, nil
	}
	if !value.retiring {
		return false, ErrRetiring
	}
	for _, retained := range s.claims {
		if retained.object == identity && retained.state != claimReleased {
			return false, ErrActiveClaim
		}
	}
	delete(s.objects, identity)
	return true, nil
}

// ReconcileCleanup aborts only a proven uncommitted claim, then tries cleanup.
// The host must establish definitive checkpoint failure before calling this.
// ErrActiveClaim means the abort was recorded but another claim defers deletion.
// Committed claims cannot be aborted, even with a matching cleanup intent.
func (s *Store) ReconcileCleanup(ctx context.Context, scope string, intent contexty.BlobCleanupIntent) (bool, error) {
	if intent.Object.Validate() != nil || intent.RetentionRef == "" || intent.ScopeRef == "" {
		return false, contexty.ErrInvalidBlob
	}
	if err := s.authorize(
		ctx,
		retentionAccess(
			ActionCleanup,
			scope,
			intent.Object,
			contexty.Descriptor{ID: "", Revision: ""},
			intent.RetentionRef,
		),
	); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	value, exists := s.claims[intent.RetentionRef]
	if !exists || value.object != intent.Object || value.scopeRef != intent.ScopeRef {
		return false, ErrClaimConflict
	}
	if value.state == claimCommitted {
		return false, ErrActiveClaim
	}
	value.state = claimReleased
	s.claims[intent.RetentionRef] = value
	if stored, found := s.objects[intent.Object]; found {
		stored.retiring = true
		s.objects[intent.Object] = stored
	}
	return s.collectLocked(intent.Object)
}
