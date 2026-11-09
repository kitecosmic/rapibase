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

func TestGetAuthUserByIDAPI(t *testing.T) {
	url := os.Getenv("RAPIBASE_TEST_DB")
	if url == "" {
		t.Skip("set RAPIBASE_TEST_DB to run this integration test")
	}
	db, err := database.New(url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	_, _ = db.Pool.Exec(ctx, `DELETE FROM auth_users WHERE email = 'get-by-id@test.local'`)
	u, err := db.CreateAuthUserAdmin(ctx, "get-by-id@test.local", "secreto123", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM auth_users WHERE id = $1`, u.ID)

	h := &AuthUsersHandler{db: db}
	app := fiber.New()
	app.Get("/users/:id", func(c *fiber.Ctx) error {
		c.Locals("apiKeyType", c.Get("X-Kind"))
		return h.GetUserByIDAPI(c)
	})
	get := func(id, kind string) (int, map[string]any) {
		req := httptest.NewRequest("GET", "/users/"+id, nil)
		req.Header.Set("X-Kind", kind)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return resp.StatusCode, m
	}

	status, body := get(u.ID, "service")
	user, _ := body["user"].(map[string]any)
	if status != 200 || user["email"] != "get-by-id@test.local" || user["role"] == "" {
		t.Fatalf("got %d %v", status, body)
	}
	if _, leaked := user["password_hash"]; leaked {
		t.Fatal("no debe exponer el hash")
	}
	if status, _ := get("00000000-0000-0000-0000-000000000000", "service"); status != 404 {
		t.Fatalf("inexistente: %d", status)
	}
	if status, _ := get("no-es-uuid", "service"); status != 404 {
		t.Fatalf("id inválido: %d", status)
	}
	if status, _ := get(u.ID, "anon"); status != 403 {
		t.Fatalf("con clave pública: %d", status)
	}
}
