package functions

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// Lo que devuelve un handler y lo que tiene que llegarle al cliente.
func TestWriteResultSoloRespuestasExplicitas(t *testing.T) {
	casos := []struct {
		nombre string
		valor  interface{}
		status int
		cuerpo string
	}{
		// El caso del bug: un dato normal con la clave "status".
		{"dato con status texto", map[string]interface{}{"status": "activo", "nombre": "x"}, 200, `{"nombre":"x","status":"activo"}`},
		{"dato con status numérico y más claves", map[string]interface{}{"status": float64(3), "items": []interface{}{}}, 200, `{"items":[],"status":3}`},
		{"dato con body y más claves", map[string]interface{}{"body": "hola", "titulo": "t"}, 200, `{"body":"hola","titulo":"t"}`},
		{"status fuera de rango", map[string]interface{}{"status": float64(1)}, 200, `{"status":1}`},
		// Respuestas explícitas de verdad.
		{"json con status", map[string]interface{}{"status": float64(201), "json": map[string]interface{}{"id": float64(1)}}, 201, `{"id":1}`},
		{"solo json", map[string]interface{}{"json": []interface{}{}}, 200, `[]`},
		{"body texto", map[string]interface{}{"status": float64(404), "body": "no"}, 404, `no`},
		{"solo status", map[string]interface{}{"status": float64(204)}, 204, ``},
		{"headers + json", map[string]interface{}{"headers": map[string]interface{}{"X-A": "1"}, "json": "ok"}, 200, `"ok"`},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(ctx *fiber.Ctx) error { return writeResult(ctx, c.valor) })
			resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != c.status || string(b) != c.cuerpo {
				t.Fatalf("got %d %q, want %d %q", resp.StatusCode, b, c.status, c.cuerpo)
			}
		})
	}
}
