package filter

// PermissionChecker decides whether a given role may read each column
// referenced by a filter or projection. It is implemented by the auth
// layer (handed to the hub at construction time) so this package stays
// decoupled from how roles and grants are stored.
//
// Implementations must be safe for concurrent use.
type PermissionChecker interface {
	// CanRead reports whether role is allowed to read column on the
	// (schema, table) relation.
	CanRead(role, schema, table, column string) bool

	// ReadableColumns returns the subset of columns from the input that
	// role is allowed to read. The order of the returned slice matches
	// the order of the input.
	ReadableColumns(role, schema, table string, columns []string) []string
}

// ValidateColumns checks that every column referenced by a compiled filter
// (and any projection) is readable by role on (schema, table). Returns
// errUnknownColumn for the first violation. Used at subscribe time so the
// server rejects subscriptions that would leak data through filter
// inspection of denied columns.
func ValidateColumns(checker PermissionChecker, role, schema, table string, columns []string) error {
	if checker == nil {
		return nil
	}
	for _, c := range columns {
		if !checker.CanRead(role, schema, table, c) {
			return errUnknownColumn
		}
	}
	return nil
}
