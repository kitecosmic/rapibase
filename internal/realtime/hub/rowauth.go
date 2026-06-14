package hub

// RowAuthorizer decides whether a subscriber may receive a specific
// postgres_changes event row. Logical replication sees every change
// before Postgres RLS is applied, so the hub must re-apply ownership
// here to mirror the REST row-level security.
//
// Implementations must be safe for concurrent use. A nil RowAuthorizer
// in Config means "no row filtering": every matching subscriber gets the
// event, preserving pre-RLS realtime behaviour for tables that have not
// opted into RLS.
type RowAuthorizer interface {
	// Authorize reports whether a subscriber with (role, userID) may
	// receive a change on (schema, table) carrying row (the new image
	// for INSERT/UPDATE, the old image for DELETE).
	Authorize(role, userID, schema, table string, row map[string]any) bool
}
