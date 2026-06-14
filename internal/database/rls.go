package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the subset of pgx used by the data-access helpers. Both
// *pgxpool.Pool (privileged/admin path) and pgx.Tx (the per-request
// RLS-enforced transaction) satisfy it, so the same query code runs in
// either context.
type Querier interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}

// AuthContext carries the per-request identity used to enforce
// row-level security. It is attached to the request context by the API
// key middleware (anon + valid JWT path) and read by the data layer.
type AuthContext struct {
	// Enforce is true for authenticated app users (anon key + JWT):
	// their queries run as rapibase_authenticated with the JWT claims
	// exposed to Postgres, so RLS policies apply. It is false for the
	// service key and the admin dashboard, which keep full access.
	Enforce    bool
	ClaimsJSON string
}

type authCtxKeyT struct{}

var authCtxKey authCtxKeyT

// WithAuth returns a context carrying ac, for the data layer to read.
func WithAuth(ctx context.Context, ac AuthContext) context.Context {
	return context.WithValue(ctx, authCtxKey, ac)
}

func authFromContext(ctx context.Context) (AuthContext, bool) {
	ac, ok := ctx.Value(authCtxKey).(AuthContext)
	return ac, ok
}

// runData executes fn against the RLS-enforced transaction when the
// context carries an authenticated identity, otherwise against the
// privileged pool (service key / admin dashboard).
func (db *DB) runData(ctx context.Context, fn func(Querier) error) error {
	if ac, ok := authFromContext(ctx); ok && ac.Enforce {
		return db.runAsUser(ctx, ac.ClaimsJSON, fn)
	}
	return fn(db.Pool)
}

// runAsUser runs fn inside a transaction that has switched to the
// low-privilege rapibase_authenticated role and exposed the JWT claims
// via current_setting('request.jwt.claims'). Both are LOCAL to the
// transaction, so they never leak to other pooled connections. If the
// role is missing (e.g. it could not be provisioned on a managed
// Postgres), SET LOCAL ROLE fails and the request fails closed rather
// than running with full privileges.
func (db *DB) runAsUser(ctx context.Context, claimsJSON string, fn func(Querier) error) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rapibase_authenticated"); err != nil {
		return fmt.Errorf("rls: switch to authenticated role: %w", err)
	}
	// set_config(..., true) == SET LOCAL, but takes the value as a bound
	// parameter so the claims JSON cannot be used for SQL injection.
	if _, err := tx.Exec(ctx, "SELECT set_config('request.jwt.claims', $1, true)", claimsJSON); err != nil {
		return fmt.Errorf("rls: set jwt claims: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// rapibasePolicyNames are every policy rapibase may create on a user
// table, across all modes. Dropped before (re)applying a mode and on
// disable, so switching modes never leaves a stale permissive policy.
var rapibasePolicyNames = []string{
	"rapibase_owner_sel", "rapibase_owner_ins", "rapibase_owner_upd", "rapibase_owner_del",
	"rapibase_auth_all", "rapibase_public_sel", "rapibase_public_write",
}

// RLS modes accepted by SetTableRLS.
const (
	RLSModeOwner         = "owner"         // each user only their own rows (ownerColumn = auth.uid())
	RLSModeAuthenticated = "authenticated" // any logged-in user, all rows
	RLSModePublic        = "public"        // anyone can read; logged-in users can write
	RLSModeCustom        = "custom"        // grant + RLS on, no built-in policy; admin writes their own
)

// SetTableRLS configures row-level security for a table according to a
// mode. It always grants the authenticated role DML and enables RLS;
// the policies differ per mode. Existing rapibase policies are dropped
// first so re-running or switching modes is idempotent.
//
//   - owner:         ownerColumn (a uuid column) must equal auth.uid();
//                    the column is defaulted to auth.uid() on insert.
//   - authenticated: any logged-in user can read/write every row.
//   - public:        anyone (even without a token) can read; logged-in
//                    users can write.
//
// The privileged pool role (admin + service key paths) is never the
// "authenticated"/"anon" role, so it always keeps full access.
func (db *DB) SetTableRLS(ctx context.Context, table, mode, ownerColumn string) error {
	if !isValidIdentifier(table) {
		return fmt.Errorf("invalid table name")
	}
	qt := quoteIdentifier(table)

	stmts := []string{
		fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON %s TO rapibase_authenticated", qt),
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO rapibase_authenticated",
		// ENABLE (not FORCE): RLS applies to the non-owner roles on the
		// public path, while the privileged pool role (table owner /
		// superuser) used by admin + service-key keeps full access.
		fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY", qt),
	}
	for _, p := range rapibasePolicyNames {
		stmts = append(stmts, fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s", p, qt))
	}

	switch mode {
	case RLSModeOwner:
		if !isValidIdentifier(ownerColumn) {
			return fmt.Errorf("owner mode requires a valid owner_column")
		}
		qc := quoteIdentifier(ownerColumn)
		stmts = append(stmts,
			// Default the owner column to the caller's id so inserts need
			// not send it and the WITH CHECK passes (like Supabase).
			fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT auth.uid()", qt, qc),
			fmt.Sprintf("CREATE POLICY rapibase_owner_sel ON %s FOR SELECT TO rapibase_authenticated USING (%s = auth.uid())", qt, qc),
			fmt.Sprintf("CREATE POLICY rapibase_owner_ins ON %s FOR INSERT TO rapibase_authenticated WITH CHECK (%s = auth.uid())", qt, qc),
			fmt.Sprintf("CREATE POLICY rapibase_owner_upd ON %s FOR UPDATE TO rapibase_authenticated USING (%s = auth.uid()) WITH CHECK (%s = auth.uid())", qt, qc, qc),
			fmt.Sprintf("CREATE POLICY rapibase_owner_del ON %s FOR DELETE TO rapibase_authenticated USING (%s = auth.uid())", qt, qc),
		)
	case RLSModeAuthenticated:
		stmts = append(stmts,
			fmt.Sprintf("CREATE POLICY rapibase_auth_all ON %s FOR ALL TO rapibase_authenticated USING (true) WITH CHECK (true)", qt),
		)
	case RLSModePublic:
		stmts = append(stmts,
			fmt.Sprintf("GRANT SELECT ON %s TO rapibase_anon", qt),
			fmt.Sprintf("CREATE POLICY rapibase_public_sel ON %s FOR SELECT TO rapibase_anon, rapibase_authenticated USING (true)", qt),
			fmt.Sprintf("CREATE POLICY rapibase_public_write ON %s FOR ALL TO rapibase_authenticated USING (true) WITH CHECK (true)", qt),
		)
	case RLSModeCustom:
		// Grant + enable RLS only (done in base above), with NO built-in
		// policy. The admin writes their own policies via SQL. Until they
		// add one, the table is default-deny for the public path.
	default:
		return fmt.Errorf("invalid mode %q (use owner, authenticated, or public)", mode)
	}

	for _, s := range stmts {
		if _, err := db.Pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("set rls (%s): %w", mode, err)
		}
	}

	// Record the config so the realtime layer can mirror it.
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO _rapibase_rls (table_name, mode, owner_column) VALUES ($1, $2, $3)
		 ON CONFLICT (table_name) DO UPDATE SET mode = EXCLUDED.mode, owner_column = EXCLUDED.owner_column, updated_at = NOW()`,
		table, mode, nullStr(ownerColumn),
	); err != nil {
		return fmt.Errorf("record rls config: %w", err)
	}
	return nil
}

// RLSConfig is the persisted RLS configuration for one table.
type RLSConfig struct {
	Table       string `json:"table"`
	Mode        string `json:"mode"`
	OwnerColumn string `json:"owner_column"`
}

// ListRLSConfig returns the RLS configuration for every managed table.
func (db *DB) ListRLSConfig(ctx context.Context) ([]RLSConfig, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT table_name, mode, COALESCE(owner_column, '') FROM _rapibase_rls`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RLSConfig
	for rows.Next() {
		var c RLSConfig
		if err := rows.Scan(&c.Table, &c.Mode, &c.OwnerColumn); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DisableTableRLS removes the owner policies, turns RLS off and revokes
// the authenticated role's access. The public API path then fails
// closed for this table (no privilege) until it is enabled again.
func (db *DB) DisableTableRLS(ctx context.Context, table string) error {
	if !isValidIdentifier(table) {
		return fmt.Errorf("invalid table name")
	}
	qt := quoteIdentifier(table)
	var stmts []string
	for _, p := range rapibasePolicyNames {
		stmts = append(stmts, fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s", p, qt))
	}
	stmts = append(stmts,
		fmt.Sprintf("ALTER TABLE %s DISABLE ROW LEVEL SECURITY", qt),
		fmt.Sprintf("REVOKE ALL ON %s FROM rapibase_authenticated", qt),
		fmt.Sprintf("REVOKE ALL ON %s FROM rapibase_anon", qt),
	)
	for _, s := range stmts {
		if _, err := db.Pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("disable rls: %w", err)
		}
	}

	if _, err := db.Pool.Exec(ctx, `DELETE FROM _rapibase_rls WHERE table_name = $1`, table); err != nil {
		return fmt.Errorf("clear rls config: %w", err)
	}
	return nil
}

// GetTableRLSStatus reports whether RLS is enabled/forced on a table and
// lists the policy names defined on it.
func (db *DB) GetTableRLSStatus(ctx context.Context, table string) (map[string]interface{}, error) {
	if !isValidIdentifier(table) {
		return nil, fmt.Errorf("invalid table name")
	}

	var enabled, forced bool
	err := db.Pool.QueryRow(ctx, `
		SELECT c.relrowsecurity, c.relforcerowsecurity
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = $1
	`, table).Scan(&enabled, &forced)
	if err != nil {
		return nil, err
	}

	rows, err := db.Pool.Query(ctx, `
		SELECT policyname, cmd, COALESCE(roles::text, ''), COALESCE(qual, ''), COALESCE(with_check, '')
		FROM pg_policies
		WHERE schemaname = 'public' AND tablename = $1
		ORDER BY policyname
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	policies := []string{}
	details := []map[string]interface{}{}
	for rows.Next() {
		var name, cmd, roles, qual, withCheck string
		if err := rows.Scan(&name, &cmd, &roles, &qual, &withCheck); err != nil {
			return nil, err
		}
		policies = append(policies, name)
		details = append(details, map[string]interface{}{
			"name":       name,
			"command":    cmd,
			"roles":      roles,
			"using":      qual,
			"with_check": withCheck,
		})
	}

	// The configured mode/owner come from our registry (empty when the
	// table was never configured through rapibase).
	var mode, ownerColumn string
	_ = db.Pool.QueryRow(ctx,
		`SELECT mode, COALESCE(owner_column, '') FROM _rapibase_rls WHERE table_name = $1`, table,
	).Scan(&mode, &ownerColumn)

	return map[string]interface{}{
		"table":          table,
		"enabled":        enabled,
		"forced":         forced,
		"policies":       policies,
		"policy_details": details,
		"mode":           mode,
		"owner_column":   ownerColumn,
	}, nil
}
