package database

import (
	"context"
	"os"
	"testing"

	"github.com/rapibase/rapibase/internal/models"
)

// TestRLS_OwnerIsolation verifies that, once RLS is enabled on a table,
// an authenticated app user only sees/affects their own rows, while the
// privileged (admin/service) path still sees everything.
//
// Requires a Postgres reachable via RAPIBASE_TEST_DB where the
// connecting role is a superuser or the table owner (the bundled
// docker-compose `rapibase` user qualifies). Skipped otherwise.
//
//	# PowerShell:
//	$env:RAPIBASE_TEST_DB="postgres://rapibase:rapibase@localhost:5432/rapibase?sslmode=disable"
//	go test ./internal/database/ -run TestRLS -v
func TestRLS_OwnerIsolation(t *testing.T) {
	url := os.Getenv("RAPIBASE_TEST_DB")
	if url == "" {
		t.Skip("set RAPIBASE_TEST_DB to run the RLS integration test")
	}

	db, err := New(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Fresh test table with a uuid owner column.
	_, _ = db.Pool.Exec(ctx, `DROP TABLE IF EXISTS rls_test_table`)
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE rls_test_table (id bigserial primary key, owner uuid, note text)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer db.Pool.Exec(ctx, `DROP TABLE IF EXISTS rls_test_table`)

	const userA = "11111111-1111-1111-1111-111111111111"
	const userB = "22222222-2222-2222-2222-222222222222"
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO rls_test_table (owner, note) VALUES ($1,'a1'),($1,'a2'),($2,'b1')`,
		userA, userB); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := db.SetTableRLS(ctx, "rls_test_table", RLSModeOwner, "owner"); err != nil {
		t.Fatalf("enable rls: %v", err)
	}

	page := models.PaginationParams{Page: 1, PageSize: 50}

	// User A sees only their 2 rows.
	aCtx := WithAuth(ctx, AuthContext{Enforce: true, ClaimsJSON: `{"sub":"` + userA + `"}`})
	if res, err := db.GetRows(aCtx, "rls_test_table", page); err != nil {
		t.Fatalf("getRows A: %v", err)
	} else if res.TotalRows != 2 {
		t.Fatalf("user A should see 2 rows, got %d", res.TotalRows)
	}

	// Admin/service path (no auth context) sees all 3.
	if res, err := db.GetRows(ctx, "rls_test_table", page); err != nil {
		t.Fatalf("getRows admin: %v", err)
	} else if res.TotalRows != 3 {
		t.Fatalf("admin should see 3 rows, got %d", res.TotalRows)
	}

	// User B inserts without specifying owner -> DEFAULT auth.uid() fills
	// it; B then sees their original row plus the new one (2 total).
	bCtx := WithAuth(ctx, AuthContext{Enforce: true, ClaimsJSON: `{"sub":"` + userB + `"}`})
	if _, err := db.InsertRow(bCtx, "rls_test_table", map[string]interface{}{"note": "b2"}); err != nil {
		t.Fatalf("insert as B: %v", err)
	}
	if res, err := db.GetRows(bCtx, "rls_test_table", page); err != nil {
		t.Fatalf("getRows B: %v", err)
	} else if res.TotalRows != 2 {
		t.Fatalf("user B should see 2 rows after insert, got %d", res.TotalRows)
	}

	// User A cannot read B's rows by filter, and cannot delete B's row.
	if err := db.DeleteRow(aCtx, "rls_test_table", 3); err == nil {
		t.Fatalf("user A should NOT be able to delete B's row (id=3)")
	}
}
