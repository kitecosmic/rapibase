package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
)

// TestRequireAPIKeyOrAnonymous cubre el visitante sin sesión: la clave
// pública sin Authorization pasa como anónimo solo en los métodos
// permitidos; un token presente pero inválido nunca se degrada a anónimo.
func TestRequireAPIKeyOrAnonymous(t *testing.T) {
	cfg := &config.Config{AnonKey: "anon-key", ServiceKey: "service-key"}
	m := NewAPIKeyMiddleware(cfg, auth.NewJWTManager("test-secret-test-secret-test-secret"))

	app := fiber.New()
	app.Use(m.RequireAPIKeyOrAnonymous(fiber.MethodGet))
	handler := func(c *fiber.Ctx) error {
		if database.IsAnonymous(c.UserContext()) {
			return c.SendString("anonymous")
		}
		return c.SendString("privileged")
	}
	app.Get("/x", handler)
	app.Post("/x", handler)

	cases := []struct {
		name, method, apikey, authz string
		wantStatus                  int
		wantBody                    string
	}{
		{"anon sin sesión lee", "GET", "anon-key", "", 200, "anonymous"},
		{"anon sin sesión no escribe", "POST", "anon-key", "", 401, ""},
		{"token inválido no se degrada", "GET", "anon-key", "Bearer basura", 401, ""},
		{"service key sigue privilegiada", "GET", "service-key", "", 200, "privileged"},
		{"service key escribe", "POST", "service-key", "", 200, "privileged"},
		{"sin apikey", "GET", "", "", 401, ""},
		{"apikey desconocida", "GET", "otra", "", 401, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/x", nil)
			if tc.apikey != "" {
				req.Header.Set("apikey", tc.apikey)
			}
			if tc.authz != "" {
				req.Header.Set("Authorization", tc.authz)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantBody != "" {
				buf := make([]byte, 64)
				n, _ := resp.Body.Read(buf)
				if got := string(buf[:n]); got != tc.wantBody {
					t.Fatalf("body = %q, want %q", got, tc.wantBody)
				}
			}
		})
	}
}

// Un usuario con sesión válida no queda marcado como anónimo.
func TestRequireAPIKeyOrAnonymous_ValidJWT(t *testing.T) {
	cfg := &config.Config{AnonKey: "anon-key", ServiceKey: "service-key"}
	jm := auth.NewJWTManager("test-secret-test-secret-test-secret")
	m := NewAPIKeyMiddleware(cfg, jm)

	app := fiber.New()
	app.Use(m.RequireAPIKeyOrAnonymous(fiber.MethodGet))
	app.Post("/x", func(c *fiber.Ctx) error {
		if database.IsAnonymous(c.UserContext()) {
			return c.SendString("anonymous")
		}
		return c.SendString("user")
	})

	token, err := jm.GenerateToken("11111111-1111-1111-1111-111111111111", "a@b.c", "user", auth.AudienceApp)
	if err != nil {
		t.Fatal(err)
	}
	// POST no está en anonMethods, pero con sesión válida sí pasa.
	req := httptest.NewRequest("POST", "/x", nil)
	req.Header.Set("apikey", "anon-key")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	buf := make([]byte, 16)
	n, _ := resp.Body.Read(buf)
	if got := string(buf[:n]); got != "user" {
		t.Fatalf("body = %q, want user", got)
	}
}
