package contexty

import "maps"

// Attributes holds host-controlled metadata separate from Provenance (source URI/ID).
type Attributes map[string]any

// Clone returns a deep copy of attributes (shallow copy of map values).
func (a Attributes) Clone() Attributes {
	if len(a) == 0 {
		return nil
	}
	out := make(Attributes, len(a))
	maps.Copy(out, a)
	return out
}
