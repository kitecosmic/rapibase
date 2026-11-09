package database

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/rapibase/rapibase/internal/models"
)

// TestAnonymousVisitor cubre al visitante sin sesión (clave pública sin
// JWT): lee tablas en modo RLS "public", no ve las demás, no escribe, y
// solo llama funciones con GRANT EXECUTE explícito a rapibase_anon.
// También fija que "sin filas" se serializa como [] y no como null.
//
// Requiere RAPIBASE_TEST_DB (ver TestRLS_OwnerIsolation).
func TestAnonymousVisitor(t *testing.T) {
	url := os.Getenv("RAPIBASE_TEST_DB")
	if url == "" {
		t.Skip("set RAPIBASE_TEST_DB to run the anonymous-visitor integration test")
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
		for _, s := range []string{
			`DROP FUNCTION IF EXISTS anon_open_fn()`,
			`DROP FUNCTION IF EXISTS anon_closed_fn()`,
			`DROP FUNCTION IF EXISTS anon_empty_fn()`,
			`DROP TABLE IF EXISTS anon_pub_table`,
			`DROP TABLE IF EXISTS anon_priv_table`,
		} {
			_, _ = db.Pool.Exec(ctx, s)
		}
	}
	cleanup()
	defer cleanup()

	for _, s := range []string{
		`CREATE TABLE anon_pub_table (id bigserial primary key, name text)`,
		`INSERT INTO anon_pub_table (name) VALUES ('a'), ('b')`,
		`CREATE TABLE anon_priv_table (id bigserial primary key, owner uuid, note text)`,
		`INSERT INTO anon_priv_table (owner, note) VALUES ('11111111-1111-1111-1111-111111111111', 'x')`,
		`CREATE FUNCTION anon_open_fn() RETURNS TABLE(n int) LANGUAGE sql AS $$ SELECT 1 $$`,
		`GRANT EXECUTE ON FUNCTION anon_open_fn() TO rapibase_anon`,
		`CREATE FUNCTION anon_closed_fn() RETURNS TABLE(n int) LANGUAGE sql AS $$ SELECT 1 $$`,
		`CREATE FUNCTION anon_empty_fn() RETURNS TABLE(n int) LANGUAGE sql AS $$ SELECT 1 WHERE false $$`,
		`GRANT EXECUTE ON FUNCTION anon_empty_fn() TO rapibase_anon`,
	} {
		if _, err := db.Pool.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	if err := db.SetTableRLS(ctx, "anon_pub_table", RLSModePublic, ""); err != nil {
		t.Fatalf("rls public: %v", err)
	}
	if err := db.SetTableRLS(ctx, "anon_priv_table", RLSModeOwner, "owner"); err != nil {
		t.Fatalf("rls owner: %v", err)
	}

	anon := WithAuth(ctx, AuthContext{Enforce: true, Anonymous: true, ClaimsJSON: `{"role":"anon"}`})
	page := models.PaginationParams{Page: 1, PageSize: 50}

	// Tabla public: el visitante la lee.
	res, err := db.GetRows(anon, "anon_pub_table", page)
	if err != nil {
		t.Fatalf("anon read public table: %v", err)
	}
	if res.TotalRows != 2 {
		t.Fatalf("anon should see 2 rows, got %d", res.TotalRows)
	}

	// Filtro sin resultados: "data" es [] y no null.
	empty := page
	empty.Filters = []models.FilterCondition{{Column: "name", Operator: "eq", Value: "nadie"}}
	res, err = db.GetRows(anon, "anon_pub_table", empty)
	if err != nil {
		t.Fatalf("anon empty read: %v", err)
	}
	if b, _ := json.Marshal(res.Data); string(b) != "[]" {
		t.Fatalf("empty data should serialize as [], got %s", b)
	}

	// Tabla owner: cerrada al visitante, con error ErrNotPublic.
	if _, err := db.GetRows(anon, "anon_priv_table", page); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("anon read owner table: want ErrNotPublic, got %v", err)
	}

	// Escribir: el rol anónimo no tiene INSERT ni en la tabla public.
	if _, err := db.InsertRow(anon, "anon_pub_table", map[string]interface{}{"name": "c"}); err == nil {
		t.Fatalf("anon should NOT be able to insert")
	}

	// RPC: con GRANT explícito sí; sin GRANT (solo el de PUBLIC) no.
	if rows, err := db.CallFunction(anon, "anon_open_fn", nil); err != nil || len(rows) != 1 {
		t.Fatalf("anon call granted fn: rows=%v err=%v", rows, err)
	}
	if _, err := db.CallFunction(anon, "anon_closed_fn", nil); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("anon call ungranted fn: want ErrNotPublic, got %v", err)
	}

	// RPC sin filas: [] y no null.
	rows, err := db.CallFunction(anon, "anon_empty_fn", nil)
	if err != nil {
		t.Fatalf("anon call empty fn: %v", err)
	}
	if b, _ := json.Marshal(rows); string(b) != "[]" {
		t.Fatalf("empty rpc should serialize as [], got %s", b)
	}

	// Un usuario con sesión sigue pudiendo llamar la función sin GRANT
	// explícito (el comportamiento de siempre no cambia).
	user := WithAuth(ctx, AuthContext{Enforce: true, ClaimsJSON: `{"sub":"11111111-1111-1111-1111-111111111111"}`})
	if _, err := db.CallFunction(user, "anon_closed_fn", nil); err != nil {
		t.Fatalf("authenticated call ungranted fn: %v", err)
	}
}
