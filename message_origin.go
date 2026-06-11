package contexty

// MessageOrigin describes which template/layer produced a message fragment.
// Orthogonal to Provenance (user/system transport origin).
type MessageOrigin struct {
	TemplateID string `json:"template_id,omitempty"`
	LayerID    string `json:"layer_id,omitempty"`
}

// Clone returns a deep copy.
func (o *MessageOrigin) Clone() *MessageOrigin {
	if o == nil {
		return nil
	}
	cp := *o
	return &cp
}

// OriginEqual reports whether two origins are equal (including both nil).
func OriginEqual(a, b *MessageOrigin) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.TemplateID == b.TemplateID && a.LayerID == b.LayerID
}

// CachePolicyRef is a typed LLM cache hint for prompt assembly.
type CachePolicyRef struct {
	Type string `json:"type,omitempty"`
}

// Clone returns a deep copy.
func (c *CachePolicyRef) Clone() *CachePolicyRef {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// cachePolicyEqual reports whether two cache refs are equal (including both nil).
func cachePolicyEqual(a, b *CachePolicyRef) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Type == b.Type
}
