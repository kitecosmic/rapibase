package filter

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// toFloat coerces any numeric Go value (including json.Number) into a
// float64. Returns ok=false for non-numeric inputs. Strings are NOT
// parsed as numbers — that would let `"7"` equal `7` which is rarely
// what a SQL-shaped filter wants.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// compare returns -1/0/1 for a < b / a == b / a > b, or ok=false when
// the two values have no defined ordering. Both operands may originate
// from any codec (so numbers might be int64, float64 or json.Number);
// compare normalises them.
func compare(a, b any) (int, bool) {
	if a == nil || b == nil {
		return 0, false
	}

	// Numeric (covers msgpack int64 ↔ json.Number ↔ float, etc.)
	af, aOK := toFloat(a)
	bf, bOK := toFloat(b)
	if aOK && bOK {
		switch {
		case af < bf:
			return -1, true
		case af > bf:
			return 1, true
		default:
			return 0, true
		}
	}

	// time.Time on either side.
	if at, ok := a.(time.Time); ok {
		if bt, ok := timeFrom(b); ok {
			return compareTimes(at, bt), true
		}
	}
	if bt, ok := b.(time.Time); ok {
		if at, ok := timeFrom(a); ok {
			return compareTimes(at, bt), true
		}
	}

	// Strings (lexicographic).
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return strings.Compare(as, bs), true
		}
	}

	// Booleans (false < true).
	if ab, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			switch {
			case !ab && bb:
				return -1, true
			case ab && !bb:
				return 1, true
			default:
				return 0, true
			}
		}
	}

	return 0, false
}

// timeFrom parses a Go value as a time.Time when possible: either it
// already is one, or it is an RFC3339 string.
func timeFrom(v any) (time.Time, bool) {
	if t, ok := v.(time.Time); ok {
		return t, true
	}
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func compareTimes(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	default:
		return 0
	}
}

// equalsAny implements eq semantics for arbitrary codec-decoded values.
// Numbers compare regardless of underlying Go type (int64 == 3.0).
// Times compare with RFC3339 string coercion. Maps and slices fall back
// to reflect.DeepEqual after numeric normalisation of leaves.
func equalsAny(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if cmp, ok := compare(a, b); ok {
		return cmp == 0
	}
	// Structural deep-equal for arrays/maps/etc.
	return reflect.DeepEqual(normalize(a), normalize(b))
}

// normalize lifts every number inside a tree into float64 so DeepEqual
// does not distinguish int64 from float64 from json.Number. Strings,
// bools and nil are left as-is.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normalize(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalize(vv)
		}
		return out
	}
	if f, ok := toFloat(v); ok {
		return f
	}
	return v
}

// contains implements jsonb @> semantics: a contains b if b is
// structurally a subset of a. Atomic values are compared with
// equalsAny.
func contains(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return false
		}
		for k, vb := range bv {
			va, present := av[k]
			if !present {
				return false
			}
			if !contains(va, vb) {
				return false
			}
		}
		return true
	case []any:
		// If b is itself an array, every element of b must be present in a.
		if bv, ok := b.([]any); ok {
			for _, bb := range bv {
				found := false
				for _, aa := range av {
					if equalsAny(aa, bb) {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
			return true
		}
		// Scalar b: a contains the scalar.
		for _, e := range av {
			if equalsAny(e, b) {
				return true
			}
		}
		return false
	default:
		return equalsAny(a, b)
	}
}
