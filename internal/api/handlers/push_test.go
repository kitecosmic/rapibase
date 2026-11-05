package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// pushApp mounts the client handlers behind a fake auth step. Every case
// here is answered before touching the database, so db stays nil.
func pushApp(keyType, userID string) *fiber.App {
	h := &PushHandler{}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("apiKeyType", keyType)
		if userID != "" {
			c.Locals("userID", userID)
		}
		return c.Next()
	})
	app.Post("/subscribe", h.Subscribe)
	app.Delete("/subscribe", h.Unsubscribe)
	app.Get("/notifications", h.GetNotifications)
	app.Post("/send", h.SendNotification)
	return app
}

func call(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: non-JSON response %q", method, path, raw)
	}
	return resp.StatusCode, out
}

const testUser = "7b0a4c1e-1111-4222-8333-944455556666"

func TestSubscribeValidation(t *testing.T) {
	anon := pushApp("anon", testUser)
	cases := []struct {
		name, body, field string
	}{
		{"empty body", `{}`, "platform"},
		{"bad platform", `{"platform":"windows","token":"x"}`, "platform"},
		{"android without token", `{"platform":"android"}`, "token"},
		{"ios without token", `{"platform":"ios","token":"  "}`, "token"},
		{"web without endpoint", `{"platform":"web"}`, "endpoint"},
		{"web without keys", `{"platform":"web","endpoint":"https://fcm.googleapis.com/fcm/send/abc"}`, "keys.p256dh"},
		{"web without auth", `{"platform":"web","endpoint":"https://fcm.googleapis.com/fcm/send/abc","keys":{"p256dh":"k"}}`, "keys.auth"},
		{"token of wrong type", `{"platform":"android","token":123}`, "body"},
	}
	for _, tc := range cases {
		code, out := call(t, anon, "POST", "/subscribe", tc.body)
		if code != 400 || out["field"] != tc.field {
			t.Errorf("%s: got %d %v, want 400 field=%s", tc.name, code, out, tc.field)
		}
	}
}

func TestSubscribeAuth(t *testing.T) {
	body := `{"platform":"android","token":"fcm-token"}`

	// Service key without user_id: 400 naming user_id, never a panic/500.
	code, out := call(t, pushApp("service", ""), "POST", "/subscribe", body)
	if code != 400 || out["field"] != "user_id" {
		t.Errorf("service without user_id: %d %v", code, out)
	}
	code, out = call(t, pushApp("service", ""), "POST", "/subscribe", `{"platform":"android","token":"t","user_id":"nope"}`)
	if code != 400 || out["field"] != "user_id" {
		t.Errorf("service with bad user_id: %d %v", code, out)
	}

	// Anon key without a JWT user: 401.
	code, _ = call(t, pushApp("anon", ""), "POST", "/subscribe", body)
	if code != 401 {
		t.Errorf("anon without JWT: %d", code)
	}

	// Anon key can't act on someone else.
	code, out = call(t, pushApp("anon", testUser), "POST", "/subscribe",
		`{"platform":"android","token":"t","user_id":"00000000-0000-4000-8000-000000000000"}`)
	if code != 403 || out["field"] != "user_id" {
		t.Errorf("anon with foreign user_id: %d %v", code, out)
	}
}

func TestUnsubscribeAndInboxValidation(t *testing.T) {
	code, out := call(t, pushApp("anon", testUser), "DELETE", "/subscribe", "")
	if code != 400 || out["field"] != "token" {
		t.Errorf("unsubscribe without token: %d %v", code, out)
	}
	code, out = call(t, pushApp("service", ""), "GET", "/notifications", "")
	if code != 400 || out["field"] != "user_id" {
		t.Errorf("service inbox without user_id: %d %v", code, out)
	}
}

func TestSendValidation(t *testing.T) {
	app := pushApp("service", "")
	cases := []struct{ body, field string }{
		{`{}`, "title"},
		{`{"title":"hi","user_id":"x"}`, "user_id"},
		{`{"title":"hi","user_ids":["` + testUser + `","x"]}`, "user_ids"},
		{`{"title":"hi","android":{"priority":"max"}}`, "android.priority"},
		{`{"title":"hi","data":"not-an-object"}`, "body"},
	}
	for _, tc := range cases {
		code, out := call(t, app, "POST", "/send", tc.body)
		if code != 400 || out["field"] != tc.field {
			t.Errorf("%s: got %d %v, want 400 field=%s", tc.body, code, out, tc.field)
		}
	}
}
