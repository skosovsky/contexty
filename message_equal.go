package contexty

import (
	"reflect"
	"slices"
)

// historiesEqual reports whether two message slices are semantically equal for thread
// persistence decisions. Nil and empty slices are equal; Metadata uses JSON-like equality
// for nested values (reflect.DeepEqual for types not handled explicitly).
func historiesEqual(left []Message, right []Message) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	if len(left) != len(right) {
		return false
	}
	return slices.EqualFunc(left, right, messagesEqual)
}

func messagesEqual(a, b Message) bool {
	if a.Role != b.Role || a.Name != b.Name || a.ToolCallID != b.ToolCallID {
		return false
	}
	if !slices.EqualFunc(a.Content, b.Content, contentPartsEqual) {
		return false
	}
	if !slices.EqualFunc(a.ToolCalls, b.ToolCalls, toolCallsEqual) {
		return false
	}
	if !stringAnyMapsEqual(a.Metadata, b.Metadata) {
		return false
	}
	return true
}

func contentPartsEqual(a, b ContentPart) bool {
	if a.Type != b.Type || a.Text != b.Text {
		return false
	}
	switch {
	case a.ImageURL == nil && b.ImageURL == nil:
		return true
	case a.ImageURL == nil || b.ImageURL == nil:
		return false
	default:
		return a.ImageURL.URL == b.ImageURL.URL && a.ImageURL.Detail == b.ImageURL.Detail
	}
}

func toolCallsEqual(a, b ToolCall) bool {
	return a.ID == b.ID && a.Type == b.Type &&
		a.Function.Name == b.Function.Name && a.Function.Arguments == b.Function.Arguments
}

// stringAnyMapsEqual treats two nil maps and two empty maps as equal.
func stringAnyMapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !anyValuesEqual(va, vb) {
			return false
		}
	}
	return true
}

func anyValuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case float32:
		bv, ok := b.(float32)
		return ok && av == bv
	case int:
		bv, ok := b.(int)
		return ok && av == bv
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case uint:
		bv, ok := b.(uint)
		return ok && av == bv
	case uint64:
		bv, ok := b.(uint64)
		return ok && av == bv
	case map[string]any:
		bv, ok := b.(map[string]any)
		return ok && stringAnyMapsEqual(av, bv)
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !anyValuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		// Avoid `==` on possibly uncomparable types (e.g. nested maps/slices in metadata).
		return reflect.DeepEqual(a, b)
	}
}
