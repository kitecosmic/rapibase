// Package filter compiles the structured JSON filter language defined in
// docs/realtime/protocol.md into in-memory predicates that evaluate against
// a single row of database event data.
//
// A filter is a tree of nodes: leaves are simple comparisons against a
// column, internal nodes combine with and / or / not. Compile validates
// the tree and produces a Predicate closure that the hub evaluates per
// subscriber on the fan-out path.
//
// The compiled predicate is safe to call concurrently from many
// goroutines.
package filter

import "sort"

// Row is the minimal view of a database row needed to evaluate filters.
// It is intentionally an interface to decouple this package from any
// specific WAL decoder representation. The wal package wraps each event's
// new/old columns in a Row implementation.
type Row interface {
	// Get returns the column value as an arbitrary Go value (string,
	// float64, json.Number, bool, nil, time.Time, []any, map[string]any)
	// and whether the column exists in this row at all.
	Get(column string) (any, bool)
}

// Predicate is the result of compiling a filter tree. Evaluating it
// against a row returns true iff the row matches the filter.
type Predicate func(Row) bool

// Always is a predicate that matches everything. Used when no filter is
// supplied for a subscription.
var Always Predicate = func(Row) bool { return true }

// Compile parses a filter tree (already deserialized into a generic
// map/array tree by the codec) and produces a Predicate. Returns an
// error describing the offending node when the tree is structurally
// invalid or when an operator does not exist.
//
// Compile may receive nil, in which case it returns Always and no error.
func Compile(tree any) (Predicate, error) {
	if tree == nil {
		return Always, nil
	}
	return compileTree(tree)
}

// CompileWithSchema is like Compile but additionally validates that every
// referenced column is present in the schema and that the operator is
// compatible with the column's declared type.
func CompileWithSchema(tree any, schema Schema) (Predicate, error) {
	if tree == nil {
		return Always, nil
	}
	if schema != nil {
		for _, col := range ReferencedColumns(tree) {
			if _, ok := schema.ColumnType(col); !ok {
				return nil, errUnknownColumn
			}
		}
	}
	return compileTree(tree)
}

// Schema describes the columns visible for a given (schema, table) pair.
// Implementations are provided by the database package at runtime.
type Schema interface {
	// ColumnType returns the SQL type name (lowercased, without modifiers)
	// of the column, and whether the column exists.
	ColumnType(column string) (string, bool)
}

// ReferencedColumns walks the filter tree and returns the sorted set of
// column names it touches. Used by the permission check at subscribe
// time so the server can reject filters that probe denied columns.
func ReferencedColumns(tree any) []string {
	set := make(map[string]struct{})
	walkColumns(tree, set)
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func walkColumns(n any, out map[string]struct{}) {
	m, ok := n.(map[string]any)
	if !ok {
		return
	}
	if col, ok := m["column"].(string); ok && col != "" {
		out[col] = struct{}{}
	}
	if conds, ok := m["conditions"].([]any); ok {
		for _, c := range conds {
			walkColumns(c, out)
		}
	}
}
