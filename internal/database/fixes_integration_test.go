package database

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rapibase/rapibase/internal/models"
)

func testDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	url := os.Getenv("RAPIBASE_TEST_DB")
	if url == "" {
		t.Skip("set RAPIBASE_TEST_DB to run this integration test")
	}
	db, err := New(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, context.Background()
}

// Una consulta que se pasa del tope la corta Postgres (no queda corriendo
// ni bloqueando) y la conexión vuelve al pool sin el statement_timeout.
func TestExecuteQueryWithTimeout(t *testing.T) {
	db, ctx := testDB(t)

	start := time.Now()
	_, err := db.ExecuteQueryWithTimeout(ctx, "SELECT pg_sleep(10)", nil, time.Second)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("want query_canceled (57014), got %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("tardó %s: el tope no cortó la consulta", d)
	}
	if !strings.Contains(err.Error(), "límite") {
		t.Fatalf("el error debería explicar el límite: %v", err)
	}

	// Ninguna conexión del pool quedó con el tope puesto.
	for i := 0; i < 10; i++ {
		res, err := db.ExecuteQuery(ctx, "SHOW statement_timeout", nil)
		if err != nil {
			t.Fatal(err)
		}
		if v := res.Rows[0]["statement_timeout"]; v != "0" {
			t.Fatalf("statement_timeout quedó en %v", v)
		}
	}

	// Y en el servidor no quedó nada corriendo.
	res, err := db.ExecuteQuery(ctx,
		"SELECT count(*) AS n FROM pg_stat_activity WHERE query = 'SELECT pg_sleep(10)' AND state = 'active'", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Rows[0]["n"]; n != int64(0) {
		t.Fatalf("la consulta sigue corriendo en el servidor: %v", n)
	}

	// Sin pasarse, funciona normal y devuelve filas.
	res, err = db.ExecuteQueryWithTimeout(ctx, "SELECT 1 AS uno", nil, time.Second)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("consulta corta: %v %v", res, err)
	}
}

// Cuando se corta el contexto (cliente que se fue), pgx le pide a Postgres
// que cancele la consulta en vez de solo cerrar el socket.
func TestContextCancelStopsServerQuery(t *testing.T) {
	db, ctx := testDB(t)
	running := func() int64 {
		res, err := db.ExecuteQuery(ctx,
			"SELECT count(*) AS n FROM pg_stat_activity WHERE query LIKE '%ctx_cancel_probe%' AND state = 'active' AND pid <> pg_backend_pid()", nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Rows[0]["n"].(int64)
	}

	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		_, _ = db.ExecuteQuery(cctx, "SELECT pg_sleep(30) AS ctx_cancel_probe", nil)
		close(done)
	}()

	// Esperar a que la consulta esté corriendo de verdad en el servidor.
	deadline := time.Now().Add(15 * time.Second)
	for running() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("la consulta nunca llegó a correr")
		}
		time.Sleep(200 * time.Millisecond)
	}

	cancel()
	<-done
	deadline = time.Now().Add(8 * time.Second)
	for running() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("la consulta siguió corriendo en el servidor tras cancelar el contexto")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestMultiStatementWithParamsExplained(t *testing.T) {
	db, ctx := testDB(t)
	_, err := db.ExecuteQuery(ctx, "SELECT $1::int; SELECT 2", []interface{}{1})
	if err == nil || !strings.Contains(err.Error(), "una sola sentencia") {
		t.Fatalf("want explained error, got %v", err)
	}
}

// read_only: todos leen, nadie escribe por la API; cambiar de modo después
// devuelve la escritura y quita la lectura anónima que no corresponde.
func TestRLSReadOnly(t *testing.T) {
	db, ctx := testDB(t)
	_, _ = db.Pool.Exec(ctx, `DROP TABLE IF EXISTS ro_test_table`)
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE ro_test_table (id bigserial primary key, owner uuid, name text)`); err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DROP TABLE IF EXISTS ro_test_table`)
	defer db.Pool.Exec(ctx, `DELETE FROM _rapibase_rls WHERE table_name = 'ro_test_table'`)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO ro_test_table (name) VALUES ('a'), ('b')`); err != nil {
		t.Fatal(err)
	}

	const userA = "11111111-1111-1111-1111-111111111111"
	anon := WithAuth(ctx, AuthContext{Enforce: true, Anonymous: true, ClaimsJSON: `{"role":"anon"}`})
	user := WithAuth(ctx, AuthContext{Enforce: true, ClaimsJSON: `{"sub":"` + userA + `"}`})
	page := models.PaginationParams{Page: 1, PageSize: 50}

	// Arranca en public (escritura habilitada) para probar que read_only la quita.
	if err := db.SetTableRLS(ctx, "ro_test_table", RLSModePublic, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SetTableRLS(ctx, "ro_test_table", RLSModeReadOnly, ""); err != nil {
		t.Fatalf("set read_only: %v", err)
	}

	for name, c := range map[string]context.Context{"anon": anon, "user": user} {
		res, err := db.GetRows(c, "ro_test_table", page)
		if err != nil || res.TotalRows != 2 {
			t.Fatalf("%s read: %v %v", name, res, err)
		}
		if _, err := db.InsertRow(c, "ro_test_table", map[string]interface{}{"name": "x"}); err == nil {
			t.Fatalf("%s should NOT insert in read_only", name)
		}
	}
	if _, err := db.UpdateRow(user, "ro_test_table", 1, map[string]interface{}{"name": "z"}); err == nil {
		t.Fatalf("user should NOT update in read_only")
	}
	if err := db.DeleteRow(user, "ro_test_table", 1); err == nil {
		t.Fatalf("user should NOT delete in read_only")
	}
	// El camino privilegiado (service key / functions) sí escribe.
	if _, err := db.InsertRow(ctx, "ro_test_table", map[string]interface{}{"name": "srv"}); err != nil {
		t.Fatalf("service insert: %v", err)
	}

	// Cambiar a owner: el usuario vuelve a escribir y el visitante ya no lee.
	if err := db.SetTableRLS(ctx, "ro_test_table", RLSModeOwner, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertRow(user, "ro_test_table", map[string]interface{}{"name": "mio"}); err != nil {
		t.Fatalf("user insert after owner: %v", err)
	}
	if _, err := db.GetRows(anon, "ro_test_table", page); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("anon read after owner: want ErrNotPublic, got %v", err)
	}
}
