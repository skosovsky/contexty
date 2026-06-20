package contexty

// SourceRef points from semantic context nodes to host-owned source identity.
type SourceRef struct {
	Namespace    string `json:"namespace,omitempty"`
	Kind         string `json:"kind,omitempty"`
	ID           string `json:"id,omitempty"`
	CheckpointID string `json:"checkpoint_id,omitempty"`
	URI          string `json:"uri,omitempty"`
}

func cloneSourceRefs(refs []SourceRef) []SourceRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]SourceRef, len(refs))
	copy(out, refs)
	return out
}

func cloneSourceRefPtr(ref *SourceRef) *SourceRef {
	if ref == nil {
		return nil
	}
	cp := *ref
	return &cp
}

// Actor identifies the participant behind a provider-facing role.
type Actor struct {
	Kind        string      `json:"kind,omitempty"`
	ID          string      `json:"id,omitempty"`
	DisplayName string      `json:"display_name,omitempty"`
	SourceRefs  []SourceRef `json:"source_refs,omitempty"`
}

// Clone returns a deep copy.
func (a *Actor) Clone() *Actor {
	if a == nil {
		return nil
	}
	cp := *a
	cp.SourceRefs = cloneSourceRefs(a.SourceRefs)
	return &cp
}

// RoleProjectionPolicy maps actor-aware messages to provider-facing roles.
type RoleProjectionPolicy interface {
	ProjectRole(Message) (Role, error)
}

// RoleProjectionFunc adapts a function to RoleProjectionPolicy.
type RoleProjectionFunc func(Message) (Role, error)

// ProjectRole implements RoleProjectionPolicy.
func (f RoleProjectionFunc) ProjectRole(msg Message) (Role, error) {
	return f(msg)
}
