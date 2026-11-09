package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
)

func TestCallRPCStructuredError(t *testing.T) {
	url := os.Getenv("RAPIBASE_TEST_DB")
	if url == "" {
		t.Skip("set RAPIBASE_TEST_DB to run this integration test")
	}
	db, err := database.New(url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	if _, err := db.Pool.Exec(ctx, `CREATE OR REPLACE FUNCTION rpc_err_probe() RETURNS int LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'saldo insuficiente' USING DETAIL = 'quedan 3', HINT = 'cargá saldo'; END $$`); err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DROP FUNCTION IF EXISTS rpc_err_probe()`)

	h := NewQueryHandler(db)
	app := fiber.New()
	app.Post("/rpc/:name", h.CallRPC)
	resp, err := app.Test(httptest.NewRequest("POST", "/rpc/rpc_err_probe", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if resp.StatusCode != 400 || m["code"] != "P0001" || m["error"] != "saldo insuficiente" ||
		m["detail"] != "quedan 3" || m["hint"] != "cargá saldo" {
		t.Fatalf("got %d %s", resp.StatusCode, b)
	}
}
