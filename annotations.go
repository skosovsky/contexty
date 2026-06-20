package contexty

import "time"

// Annotations holds non-identity transport metadata separate from message payload text.
type Annotations struct {
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

// Clone returns a deep copy of annotations.
func (a Annotations) Clone() Annotations {
	cloned := a
	if a.Timestamp != nil {
		t := *a.Timestamp
		cloned.Timestamp = &t
	}
	return cloned
}
