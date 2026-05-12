package realtime

import "github.com/rapibase/rapibase/internal/realtime/filter"

// PermissiveChecker is a filter.PermissionChecker that allows every
// role to read every column. It is the default for projects that have
// not yet configured row-level security; once RLS lands in rapibase,
// a database-backed checker replaces this one.
//
// The type is exported so external operators wiring rapibase from
// their own main can pick it without having to copy the boilerplate.
type PermissiveChecker struct{}

// CanRead implements filter.PermissionChecker. Always true.
func (PermissiveChecker) CanRead(_, _, _, _ string) bool { return true }

// ReadableColumns implements filter.PermissionChecker. Echoes its
// input.
func (PermissiveChecker) ReadableColumns(_, _, _ string, cols []string) []string {
	out := make([]string, len(cols))
	copy(out, cols)
	return out
}

// Static interface assertion.
var _ filter.PermissionChecker = PermissiveChecker{}
