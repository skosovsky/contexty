package contexty

import (
	"encoding/json"
	"reflect"
	"slices"
)

// MessagesEqual reports whether two message slices are deeply equal for persistence.
func MessagesEqual(a, b []Message) bool {
	return slices.EqualFunc(a, b, MessageEqual)
}

// MessageEqual compares semantic message fields.
func MessageEqual(a, b Message) bool {
	if a.Role != b.Role {
		return false
	}
	if !slices.EqualFunc(a.Parts, b.Parts, contentPartsEqual) {
		return false
	}
	if a.ID != b.ID {
		return false
	}
	if a.Annotations != b.Annotations {
		return false
	}
	if !attributesEqual(a.Attributes, b.Attributes) {
		return false
	}
	return provenanceEqual(a.Provenance, b.Provenance)
}

func attributesEqual(a, b Attributes) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func contentPartsEqual(a, b ContentPart) bool {
	return reflect.DeepEqual(a, b)
}

func provenanceEqual(a, b Provenance) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.provenanceType() != b.provenanceType() {
		return false
	}
	ja, err := json.Marshal(a)
	if err != nil {
		return false
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ja) == string(jb)
}
