package handlers

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRPCErrorEstructurado(t *testing.T) {
	pg := &pgconn.PgError{Code: "P0001", Message: "saldo insuficiente", Detail: "quedan 3", Hint: "cargá saldo"}
	status, body := rpcError(fmt.Errorf("call rpc: %w", pg))
	if status != 400 || body["code"] != "P0001" || body["error"] != "saldo insuficiente" ||
		body["message"] != "saldo insuficiente" || body["detail"] != "quedan 3" || body["hint"] != "cargá saldo" {
		t.Fatalf("got %d %v", status, body)
	}

	status, body = rpcError(&pgconn.PgError{Code: "42501", Message: "permission denied for function f"})
	if status != 403 || body["code"] != "42501" {
		t.Fatalf("permiso: got %d %v", status, body)
	}
	if _, ok := body["detail"]; ok {
		t.Fatalf("detail vacío no debería aparecer: %v", body)
	}

	status, body = rpcError(errors.New("invalid function name"))
	if status != 400 || body["error"] != "invalid function name" || len(body) != 1 {
		t.Fatalf("no-pg: got %d %v", status, body)
	}
}
