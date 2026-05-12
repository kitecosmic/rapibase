package filter

import (
	"fmt"
	"regexp"
	"strings"
)

// compileTree dispatches on the shape of the node and returns a
// Predicate. Returns errInvalidNode for malformed shapes and
// errInvalidOperator for unknown ops.
func compileTree(tree any) (Predicate, error) {
	m, ok := tree.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: expected object at root, got %T", errInvalidNode, tree)
	}
	return compileNode(m)
}

func compileNode(n map[string]any) (Predicate, error) {
	op, _ := n["op"].(string)
	if op == "" {
		return nil, fmt.Errorf("%w: missing op", errInvalidNode)
	}

	switch op {
	case OpAnd, OpOr:
		conds, ok := asNodeSlice(n["conditions"])
		if !ok {
			return nil, fmt.Errorf("%w: %s requires conditions array", errInvalidNode, op)
		}
		children, err := compileChildren(conds)
		if err != nil {
			return nil, err
		}
		if op == OpAnd {
			return func(r Row) bool {
				for _, c := range children {
					if !c(r) {
						return false
					}
				}
				return true
			}, nil
		}
		return func(r Row) bool {
			for _, c := range children {
				if c(r) {
					return true
				}
			}
			return false
		}, nil

	case OpNot:
		conds, ok := asNodeSlice(n["conditions"])
		if !ok || len(conds) != 1 {
			return nil, fmt.Errorf("%w: not requires exactly one condition", errInvalidNode)
		}
		child, err := compileNode(conds[0])
		if err != nil {
			return nil, err
		}
		return func(r Row) bool { return !child(r) }, nil
	}

	// Leaf
	column, _ := n["column"].(string)
	if column == "" {
		return nil, fmt.Errorf("%w: leaf missing column", errInvalidNode)
	}
	compiler, ok := leafOps[op]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errInvalidOperator, op)
	}
	return compiler(column, n["value"])
}

// asNodeSlice coerces a conditions value (always an []any from the codec)
// into a slice of map[string]any nodes. Returns ok=false if any entry is
// not an object.
func asNodeSlice(v any) ([]map[string]any, bool) {
	raw, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		m, ok := c.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, m)
	}
	return out, true
}

func compileChildren(nodes []map[string]any) ([]Predicate, error) {
	out := make([]Predicate, 0, len(nodes))
	for _, n := range nodes {
		p, err := compileNode(n)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// leafOps wires operator names to their per-operator compile function.
// Adding a new operator means adding a const in operators.go and an
// entry here — nothing else needs to change.
var leafOps = map[string]func(column string, value any) (Predicate, error){
	OpEq:          compileEq,
	OpNeq:         compileNeq,
	OpLt:          compileLt,
	OpLte:         compileLte,
	OpGt:          compileGt,
	OpGte:         compileGte,
	OpIn:          compileIn,
	OpNotIn:       compileNotIn,
	OpIs:          compileIs,
	OpLike:        compileLike,
	OpILike:       compileILike,
	OpContains:    compileContains,
	OpContainedBy: compileContainedBy,
	OpMatch:       compileMatch,
}

func compileEq(column string, value any) (Predicate, error) {
	return func(r Row) bool {
		v, _ := r.Get(column)
		return equalsAny(v, value)
	}, nil
}

func compileNeq(column string, value any) (Predicate, error) {
	return func(r Row) bool {
		v, _ := r.Get(column)
		return !equalsAny(v, value)
	}, nil
}

func compileLt(column string, value any) (Predicate, error) {
	return compileCompare(column, value, func(c int) bool { return c < 0 })
}
func compileLte(column string, value any) (Predicate, error) {
	return compileCompare(column, value, func(c int) bool { return c <= 0 })
}
func compileGt(column string, value any) (Predicate, error) {
	return compileCompare(column, value, func(c int) bool { return c > 0 })
}
func compileGte(column string, value any) (Predicate, error) {
	return compileCompare(column, value, func(c int) bool { return c >= 0 })
}

func compileCompare(column string, value any, accept func(int) bool) (Predicate, error) {
	return func(r Row) bool {
		v, ok := r.Get(column)
		if !ok || v == nil {
			return false
		}
		cmp, ok := compare(v, value)
		if !ok {
			return false
		}
		return accept(cmp)
	}, nil
}

func compileIn(column string, value any) (Predicate, error) {
	arr, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: in requires array value", errInvalidNode)
	}
	candidates := append([]any(nil), arr...)
	return func(r Row) bool {
		v, _ := r.Get(column)
		for _, c := range candidates {
			if equalsAny(v, c) {
				return true
			}
		}
		return false
	}, nil
}

func compileNotIn(column string, value any) (Predicate, error) {
	p, err := compileIn(column, value)
	if err != nil {
		return nil, err
	}
	return func(r Row) bool { return !p(r) }, nil
}

func compileIs(column string, value any) (Predicate, error) {
	var target any
	switch v := value.(type) {
	case nil:
		target = nil
	case bool:
		target = v
	case string:
		switch strings.ToLower(v) {
		case "null":
			target = nil
		case "true":
			target = true
		case "false":
			target = false
		default:
			return nil, fmt.Errorf("%w: is requires null|true|false, got %q", errInvalidNode, v)
		}
	default:
		return nil, fmt.Errorf("%w: is requires null|true|false, got %T", errInvalidNode, value)
	}
	return func(r Row) bool {
		v, ok := r.Get(column)
		if !ok {
			v = nil
		}
		if target == nil {
			return v == nil
		}
		b, ok := v.(bool)
		if !ok {
			return false
		}
		return b == target.(bool)
	}, nil
}

func compileLike(column string, value any) (Predicate, error) {
	return compileLikeImpl(column, value, false)
}
func compileILike(column string, value any) (Predicate, error) {
	return compileLikeImpl(column, value, true)
}

func compileLikeImpl(column string, value any, caseInsensitive bool) (Predicate, error) {
	pattern, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("%w: like/ilike requires string value", errInvalidNode)
	}
	re, err := likeToRegex(pattern, caseInsensitive)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidNode, err)
	}
	return func(r Row) bool {
		v, _ := r.Get(column)
		s, ok := v.(string)
		if !ok {
			return false
		}
		return re.MatchString(s)
	}, nil
}

func compileContains(column string, value any) (Predicate, error) {
	return func(r Row) bool {
		v, ok := r.Get(column)
		if !ok || v == nil {
			return false
		}
		return contains(v, value)
	}, nil
}

func compileContainedBy(column string, value any) (Predicate, error) {
	return func(r Row) bool {
		v, ok := r.Get(column)
		if !ok || v == nil {
			return false
		}
		return contains(value, v)
	}, nil
}

func compileMatch(column string, value any) (Predicate, error) {
	needle, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("%w: match requires string", errInvalidNode)
	}
	lowerNeedle := strings.ToLower(needle)
	return func(r Row) bool {
		v, _ := r.Get(column)
		s, ok := v.(string)
		if !ok {
			return false
		}
		return strings.Contains(strings.ToLower(s), lowerNeedle)
	}, nil
}

// likeToRegex translates an SQL LIKE/ILIKE pattern into a Go regexp. The
// pattern characters % and _ become .* and . respectively; backslash
// escapes the next character. All other regex metacharacters are
// quoted so they match literally.
func likeToRegex(pattern string, caseInsensitive bool) (*regexp.Regexp, error) {
	var b strings.Builder
	if caseInsensitive {
		b.WriteString("(?i)")
	}
	b.WriteString("\\A")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		case '\\':
			if i+1 < len(pattern) {
				b.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
				i++
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("\\z")
	return regexp.Compile(b.String())
}
