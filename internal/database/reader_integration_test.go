package database

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestReadOnlySQL cubre query_sql: lee todo (RLS no filtra, como
// query_rows) pero no escribe ni puede volver al superusuario, ni con una
// sola sentencia ni encadenando varias.
//
// Requiere RAPIBASE_TEST_DB (ver TestRLS_OwnerIsolation).
func TestReadOnlySQL(t *testing.T) {
	url := os.Getenv("RAPIBASE_TEST_DB")
	if url == "" {
		t.Skip("set RAPIBASE_TEST_DB to run the read-only SQL integration test")
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
	cleanup := func() {
		_, _ = db.Pool.Exec(ctx, `DROP TABLE IF EXISTS ro_items`)
	}
	cleanup()
	defer cleanup()
	for _, s := range []string{
		`CREATE TABLE ro_items (id bigserial primary key, name text)`,
		`INSERT INTO ro_items (name) VALUES ('a'), ('b'), ('c')`,
		// RLS activado sin políticas: nadie sin BYPASSRLS ve filas
		`ALTER TABLE ro_items ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE ro_items FORCE ROW LEVEL SECURITY`,
	} {
		if _, err := db.Pool.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	run := func(sql string, params ...any) ([]map[string]any, error) {
		res, err := db.ExecuteReadOnly(ctx, sql, params, 10*time.Second)
		if err != nil {
			return nil, err
		}
		return res.Rows, nil
	}

	rows, err := run(`SELECT count(*) AS n FROM ro_items WHERE name <> $1`, "c")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(rows) != 1 || rows[0]["n"] != int64(2) {
		t.Fatalf("select: got %v", rows)
	}
	if rows, err := run(`SELECT current_user AS u, current_setting('transaction_read_only') AS ro`); err != nil ||
		rows[0]["u"] != RoleReader || rows[0]["ro"] != "on" {
		t.Fatalf("identity: %v %v", rows, err)
	}
	// sin filas: [] y columnas
	if rows, err := run(`SELECT id FROM ro_items WHERE false`); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty result: %v %v", rows, err)
	}

	mustFail := func(name, sql string) {
		t.Helper()
		_, err := run(sql)
		if err == nil {
			t.Errorf("%s: should have failed: %s", name, sql)
			return
		}
		t.Logf("%s: %v", name, err)
	}
	mustFail("insert", `INSERT INTO ro_items (name) VALUES ('x')`)
	mustFail("cte write", `WITH d AS (DELETE FROM ro_items RETURNING id) SELECT count(*) FROM d`)
	mustFail("ddl", `CREATE TABLE ro_evil (id int)`)
	mustFail("temp table", `CREATE TEMP TABLE ro_tmp (id int)`)
	mustFail("two statements", `SELECT 1; DELETE FROM ro_items`)
	mustFail("set rw", `SET TRANSACTION READ WRITE; DELETE FROM ro_items`)
	mustFail("nextval", `SELECT nextval('ro_items_id_seq')`)
	mustFail("read server file", `SELECT pg_read_file('/etc/hostname')`)
	mustFail("program", `COPY (SELECT 1) TO PROGRAM 'true'`)
	mustFail("terminate", `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid <> pg_backend_pid()`)
	// volver al superusuario: el usuario de la sesión ya no lo es
	mustFail("set role", `SET ROLE rapibase`)
	mustFail("escape via set_config", `SELECT set_config('role', 'rapibase', true), query_to_xml('SELECT pg_read_file(''/etc/hostname'')', true, false, '')`)
	mustFail("escape via session auth", `SET SESSION AUTHORIZATION rapibase`)

	// los datos siguen intactos
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM ro_items`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("rows changed: %d %v", n, err)
	}
	// y el error de escritura dice qué usar
	_, err = run(`UPDATE ro_items SET name = 'z'`)
	if err == nil || !strings.Contains(err.Error(), "execute_sql") {
		t.Errorf("write error should point to execute_sql: %v", err)
	}
	if _, err := run(`SELECT 1; SELECT 2`); err == nil || !strings.Contains(err.Error(), "one statement") {
		t.Errorf("multi-statement error should say one statement per call: %v", err)
	}
	// el tope de tiempo lo corta Postgres
	if _, err := db.ExecuteReadOnly(ctx, `SELECT pg_sleep(3)`, nil, time.Second); err == nil {
		t.Error("timeout should cancel the query")
	}
	// una sentencia que falla no deja la conexión rota para la siguiente
	if _, err := run(`SELECT 1 AS ok`); err != nil {
		t.Errorf("after errors: %v", err)
	}
	if errors.Is(err, ErrReaderUnavailable) {
		t.Fatal("reader unavailable")
	}
}
