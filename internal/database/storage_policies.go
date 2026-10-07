package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Storage access rules are real Postgres row-level security on
// storage_objects, the same model as Supabase. Every check made for an
// app user (anon key + JWT) runs as rapibase_authenticated with the
// user's claims, so the policies decide what they can read, upload,
// replace and delete. The service key and the dashboard use the
// privileged pool and are never filtered.
//
// Two layers of policies:
//
//   - Built-in ones (rapibase_storage_*), recreated on every start: files
//     in public buckets are readable, and — in buckets with owner_access
//     on, the default — a logged-in user can upload files owned by them
//     and read, replace and delete their own files.
//   - The app's own rules, written in SQL against the row being checked
//     (bucket_id, name, owner, size, mime_type, metadata) with auth.uid(),
//     auth.jwt(), storage.foldername(name)… and any table the user can
//     read. Permissive rules widen access; restrictive rules narrow it.

// storageBuiltinPolicies are recreated on every start; their names are
// reserved (custom rules may not use the rapibase_ prefix).
var storageBuiltinPolicies = []string{
	`CREATE POLICY rapibase_storage_public_read ON storage_objects FOR SELECT TO rapibase_authenticated
		USING (storage.bucket_is_public(bucket_id))`,
	`CREATE POLICY rapibase_storage_owner_read ON storage_objects FOR SELECT TO rapibase_authenticated
		USING (owner = auth.uid() AND storage.bucket_owner_access(bucket_id))`,
	`CREATE POLICY rapibase_storage_owner_insert ON storage_objects FOR INSERT TO rapibase_authenticated
		WITH CHECK (owner = auth.uid() AND storage.bucket_owner_access(bucket_id))`,
	`CREATE POLICY rapibase_storage_owner_update ON storage_objects FOR UPDATE TO rapibase_authenticated
		USING (owner = auth.uid() AND storage.bucket_owner_access(bucket_id))
		WITH CHECK (owner = auth.uid() AND storage.bucket_owner_access(bucket_id))`,
	`CREATE POLICY rapibase_storage_owner_delete ON storage_objects FOR DELETE TO rapibase_authenticated
		USING (owner = auth.uid() AND storage.bucket_owner_access(bucket_id))`,
}

var storageBuiltinNames = []string{
	"rapibase_storage_public_read", "rapibase_storage_owner_read", "rapibase_storage_owner_insert",
	"rapibase_storage_owner_update", "rapibase_storage_owner_delete",
}

// migrateStoragePolicies wires storage_objects into RLS. Called at the
// end of Migrate, once the roles and auth.* helpers exist. Non-fatal:
// without the roles (managed Postgres without CREATEROLE) the public
// storage API fails closed, like the rest of the public API.
func (db *DB) migrateStoragePolicies(ctx context.Context) error {
	stmts := []string{
		`ALTER TABLE storage_buckets ADD COLUMN IF NOT EXISTS owner_access BOOLEAN NOT NULL DEFAULT TRUE`,
		// helpers with the same names and meaning as Supabase's, so rules
		// written for it (or by an agent that knows it) work here
		`CREATE SCHEMA IF NOT EXISTS storage`,
		`GRANT USAGE ON SCHEMA storage TO PUBLIC`,
		`CREATE OR REPLACE FUNCTION storage.foldername(name text) RETURNS text[] LANGUAGE sql IMMUTABLE AS $$
			SELECT CASE WHEN array_length(p, 1) > 1 THEN p[1:array_length(p, 1) - 1] ELSE '{}'::text[] END
			FROM string_to_array(name, '/') AS p
		$$`,
		`CREATE OR REPLACE FUNCTION storage.filename(name text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
			SELECT (string_to_array(name, '/'))[array_length(string_to_array(name, '/'), 1)]
		$$`,
		`CREATE OR REPLACE FUNCTION storage.extension(name text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
			SELECT CASE WHEN storage.filename(name) LIKE '%.%'
				THEN lower(regexp_replace(storage.filename(name), '^.*\.', '')) ELSE '' END
		$$`,
		// SECURITY DEFINER: policies read bucket settings without granting
		// the app roles access to storage_buckets.
		`CREATE OR REPLACE FUNCTION storage.bucket_is_public(bucket text) RETURNS boolean
			LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
			SELECT COALESCE((SELECT b.public FROM storage_buckets b WHERE b.id = bucket), false)
		$$`,
		`CREATE OR REPLACE FUNCTION storage.bucket_owner_access(bucket text) RETURNS boolean
			LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
			SELECT COALESCE((SELECT b.owner_access FROM storage_buckets b WHERE b.id = bucket), false)
		$$`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON storage_objects TO rapibase_authenticated`,
		`ALTER TABLE storage_objects ENABLE ROW LEVEL SECURITY`,
	}
	for _, name := range storageBuiltinNames {
		stmts = append(stmts, "DROP POLICY IF EXISTS "+name+" ON storage_objects")
	}
	stmts = append(stmts, storageBuiltinPolicies...)

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage policies: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// AuthFromContext exposes the request identity set by the API key
// middleware (ok=false for the service key and the dashboard).
func AuthFromContext(ctx context.Context) (AuthContext, bool) {
	ac, ok := authFromContext(ctx)
	return ac, ok && ac.Enforce
}

// errDryRun rolls back a permission probe after it ran.
var errDryRun = errors.New("dry run")

// probeAsUser runs fn as the app user and always rolls back: it answers
// "would Postgres let this user do it?" without changing anything.
func (db *DB) probeAsUser(ctx context.Context, claimsJSON string, fn func(Querier) error) error {
	err := db.runAsUser(ctx, claimsJSON, func(q Querier) error {
		if err := fn(q); err != nil {
			return err
		}
		return errDryRun
	})
	if errors.Is(err, errDryRun) {
		return nil
	}
	return err
}

// isRLSDenied reports a "new row violates row-level security policy" or
// missing-privilege error: a denial, not a failure.
func isRLSDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

// StorageRow is the candidate state of an object for INSERT/UPDATE checks.
type StorageRow struct {
	Owner    *string
	Size     int64
	MimeType string
	Metadata map[string]interface{}
}

// StorageCanRead: can the user see the recorded object?
func (db *DB) StorageCanRead(ctx context.Context, claimsJSON, bucket, name string) (bool, error) {
	var found bool
	err := db.probeAsUser(ctx, claimsJSON, func(q Querier) error {
		return q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storage_objects WHERE bucket_id = $1 AND name = $2)`,
			bucket, name).Scan(&found)
	})
	if isRLSDenied(err) {
		return false, nil
	}
	return found, err
}

// StorageCanInsert: may the user create this new object?
func (db *DB) StorageCanInsert(ctx context.Context, claimsJSON, bucket, name string, row StorageRow) (bool, error) {
	err := db.probeAsUser(ctx, claimsJSON, func(q Querier) error {
		_, err := q.Exec(ctx, `
			INSERT INTO storage_objects (bucket_id, name, owner, size, mime_type, metadata)
			VALUES ($1, $2, $3, $4, $5, COALESCE($6::jsonb, '{}'::jsonb))`,
			bucket, name, row.Owner, row.Size, row.MimeType, jsonOrNil(row.Metadata))
		return err
	})
	if isRLSDenied(err) {
		return false, nil
	}
	return err == nil, err
}

// StorageCanUpdate: may the user replace this existing object with row?
// A nil Owner keeps the current owner, nil Metadata the current metadata.
func (db *DB) StorageCanUpdate(ctx context.Context, claimsJSON, bucket, name string, row StorageRow) (bool, error) {
	var n int64
	err := db.probeAsUser(ctx, claimsJSON, func(q Querier) error {
		tag, err := q.Exec(ctx, `
			UPDATE storage_objects SET
				owner = COALESCE($3, owner), size = $4, mime_type = $5,
				metadata = COALESCE($6::jsonb, metadata), updated_at = NOW()
			WHERE bucket_id = $1 AND name = $2`,
			bucket, name, row.Owner, row.Size, row.MimeType, jsonOrNil(row.Metadata))
		n = tag.RowsAffected()
		return err
	})
	if isRLSDenied(err) {
		return false, nil
	}
	return n > 0, err
}

// StorageCanDelete: may the user delete this object?
func (db *DB) StorageCanDelete(ctx context.Context, claimsJSON, bucket, name string) (bool, error) {
	var n int64
	err := db.probeAsUser(ctx, claimsJSON, func(q Querier) error {
		tag, err := q.Exec(ctx, `DELETE FROM storage_objects WHERE bucket_id = $1 AND name = $2`, bucket, name)
		n = tag.RowsAffected()
		return err
	})
	if isRLSDenied(err) {
		return false, nil
	}
	return n > 0, err
}

// queryObjectsAs runs a read of storage_objects as the app user, so only
// the rows their rules allow come back.
func (db *DB) queryObjectsAs(ctx context.Context, claimsJSON, sql string, args ...interface{}) ([]StorageObject, error) {
	var out []StorageObject
	err := db.probeAsUser(ctx, claimsJSON, func(q Querier) error {
		rows, err := q.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		out, err = scanObjects(rows)
		return err
	})
	if isRLSDenied(err) {
		return []StorageObject{}, nil
	}
	return out, err
}

func (db *DB) ListStorageObjectsAs(ctx context.Context, claimsJSON, bucketID, prefix string, limit, offset int) ([]StorageObject, error) {
	return db.queryObjectsAs(ctx, claimsJSON, `
		SELECT `+objectCols+` FROM storage_objects
		WHERE bucket_id = $1 AND name LIKE $2
		ORDER BY name LIMIT $3 OFFSET $4`, bucketID, likePrefix(prefix), limit, offset)
}

func (db *DB) SearchStorageObjectsAs(ctx context.Context, claimsJSON, bucketID, key, value string) ([]StorageObject, error) {
	return db.queryObjectsAs(ctx, claimsJSON, `
		SELECT `+objectCols+` FROM storage_objects
		WHERE bucket_id = $1 AND metadata->>$2 = $3
		ORDER BY created_at DESC`, bucketID, key, value)
}

func (db *DB) ListStorageObjectsByOwnerAs(ctx context.Context, claimsJSON, ownerID string) ([]StorageObject, error) {
	return db.queryObjectsAs(ctx, claimsJSON, `
		SELECT `+objectCols+` FROM storage_objects
		WHERE owner = $1 ORDER BY created_at DESC`, ownerID)
}

func (db *DB) GetStorageObjectAs(ctx context.Context, claimsJSON, bucketID, name string) (*StorageObject, error) {
	rows, err := db.queryObjectsAs(ctx, claimsJSON, `
		SELECT `+objectCols+` FROM storage_objects WHERE bucket_id = $1 AND name = $2`, bucketID, name)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func jsonOrNil(m map[string]interface{}) any {
	if m == nil {
		return nil
	}
	return m
}

// ---- custom rules ---------------------------------------------------------

// StoragePolicy is one access rule on storage_objects.
type StoragePolicy struct {
	Name        string `json:"name"`
	Bucket      string `json:"bucket,omitempty"` // "" = every bucket
	Operation   string `json:"operation"`        // read | upload | update | delete | all
	Restrictive bool   `json:"restrictive"`
	// Condition is the SQL exactly as it was written (without the bucket
	// guard); Using/WithCheck are what Postgres actually stores.
	Condition string `json:"condition"`
	Using     string `json:"using,omitempty"`
	WithCheck string `json:"with_check,omitempty"`
	Builtin   bool   `json:"builtin"`
}

// policyNote is kept as the policy's COMMENT so rules read back exactly
// as written.
type policyNote struct {
	Bucket    string `json:"bucket,omitempty"`
	Condition string `json:"condition"`
}

var policyNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{2,62}$`)

var operationCmd = map[string]string{
	"read": "SELECT", "upload": "INSERT", "update": "UPDATE", "delete": "DELETE", "all": "ALL",
}

var cmdOperation = map[string]string{
	"SELECT": "read", "INSERT": "upload", "UPDATE": "update", "DELETE": "delete", "ALL": "all",
}

// bucketScopeRe recognises the bucket guard CreateStoragePolicy adds, to
// show rules per bucket.
var bucketScopeRe = regexp.MustCompile(`^\(?\(?\(?bucket_id\)?::text = '([a-z0-9.-]+)'::text\)`)

// ListStoragePolicies returns every policy on storage_objects.
func (db *DB) ListStoragePolicies(ctx context.Context) ([]StoragePolicy, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT p.policyname, p.cmd, p.permissive, COALESCE(p.qual, ''), COALESCE(p.with_check, ''),
		       COALESCE(obj_description(pol.oid, 'pg_policy'), '')
		FROM pg_policies p
		JOIN pg_policy pol ON pol.polname = p.policyname AND pol.polrelid = 'public.storage_objects'::regclass
		WHERE p.schemaname = 'public' AND p.tablename = 'storage_objects'
		ORDER BY p.policyname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StoragePolicy{}
	for rows.Next() {
		var p StoragePolicy
		var cmd, permissive, comment string
		if err := rows.Scan(&p.Name, &cmd, &permissive, &p.Using, &p.WithCheck, &comment); err != nil {
			return nil, err
		}
		p.Operation = cmdOperation[cmd]
		p.Restrictive = permissive == "RESTRICTIVE"
		p.Builtin = strings.HasPrefix(p.Name, "rapibase_")
		var note policyNote
		if json.Unmarshal([]byte(comment), &note) == nil && note.Condition != "" {
			p.Bucket, p.Condition = note.Bucket, note.Condition
		} else {
			// written by hand in SQL: show what Postgres has
			expr := p.Using
			if expr == "" {
				expr = p.WithCheck
			}
			p.Condition = expr
			if m := bucketScopeRe.FindStringSubmatch(expr); m != nil {
				p.Bucket = m[1]
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrPolicy is a rule the caller got wrong (bad name, bad SQL…).
type ErrPolicy struct{ Msg string }

func (e *ErrPolicy) Error() string { return e.Msg }

// CreateStoragePolicy adds (or replaces) a rule. bucket != "" scopes it
// to that bucket. The expression is plain SQL evaluated per file row;
// Postgres validates it, and a broken one is reported without changing
// anything.
func (db *DB) CreateStoragePolicy(ctx context.Context, name, bucket, operation, expression string, restrictive bool) error {
	if !policyNameRe.MatchString(name) || strings.HasPrefix(name, "rapibase_") {
		return &ErrPolicy{"rule names are 3-63 lowercase letters, digits or _, start with a letter, and can't start with rapibase_"}
	}
	cmd, ok := operationCmd[operation]
	if !ok {
		return &ErrPolicy{"operation must be read, upload, update, delete or all"}
	}
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return &ErrPolicy{"the rule needs an SQL condition, e.g. owner = auth.uid()"}
	}
	if strings.Contains(expression, ";") {
		return &ErrPolicy{"the condition is a single SQL expression (no ;)"}
	}
	cond := "(" + expression + ")"
	if bucket != "" {
		cond = fmt.Sprintf("bucket_id = %s AND %s", quoteLiteral(bucket), cond)
	}
	kind := "PERMISSIVE"
	if restrictive {
		kind = "RESTRICTIVE"
	}
	var clause string
	switch cmd {
	case "SELECT", "DELETE":
		clause = "USING (" + cond + ")"
	case "INSERT":
		clause = "WITH CHECK (" + cond + ")"
	default:
		clause = "USING (" + cond + ") WITH CHECK (" + cond + ")"
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	qn := quoteIdentifier(name)
	if _, err := tx.Exec(ctx, "DROP POLICY IF EXISTS "+qn+" ON storage_objects"); err != nil {
		return err
	}
	stmt := fmt.Sprintf("CREATE POLICY %s ON storage_objects AS %s FOR %s TO rapibase_authenticated %s", qn, kind, cmd, clause)
	if _, err := tx.Exec(ctx, stmt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return &ErrPolicy{"invalid rule: " + pgErr.Message}
		}
		return err
	}
	note, _ := json.Marshal(policyNote{Bucket: bucket, Condition: expression})
	if _, err := tx.Exec(ctx, "COMMENT ON POLICY "+qn+" ON storage_objects IS "+quoteLiteral(string(note))); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteStoragePolicy removes a custom rule.
func (db *DB) DeleteStoragePolicy(ctx context.Context, name string) error {
	if strings.HasPrefix(name, "rapibase_") {
		return &ErrPolicy{"built-in rules can't be deleted; turn off owner_access on the bucket instead"}
	}
	var exists bool
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_policies
		WHERE schemaname = 'public' AND tablename = 'storage_objects' AND policyname = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	_, err := db.Pool.Exec(ctx, "DROP POLICY "+quoteIdentifier(name)+" ON storage_objects")
	return err
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

