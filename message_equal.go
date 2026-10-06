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
	if !reflect.DeepEqual(a.Actor, b.Actor) {
		return false
	}
	if !reflect.DeepEqual(a.SourceRefs, b.SourceRefs) {
		return false
	}
	if !OriginEqual(a.Origin, b.Origin) {
		return false
	}
	if !cachePolicyEqual(a.LLMCache, b.LLMCache) {
		return false
	}
	if !extensionsEqual(a.Extensions, b.Extensions) {
		return false
	}
	return provenanceEqual(a.Provenance, b.Provenance)
}

func extensionsEqual(a, b []Extension) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !extensionEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func contentPartsEqual(a, b ContentPart) bool {
	return reflect.DeepEqual(a, b)
}

func extensionEqual(a, b Extension) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.ExtensionType() != b.ExtensionType() {
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

func provenanceEqual(a, b Provenance) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.ProvenanceType() != b.ProvenanceType() {
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
