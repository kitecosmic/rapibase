package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/webhooks"
)

// TestWebhookDeliveries_Integration exercises the persistent delivery queue
// and the access log filters against a real Postgres (RAPIBASE_TEST_DB).
func TestWebhookDeliveries_Integration(t *testing.T) {
	url := os.Getenv("RAPIBASE_TEST_DB")
	if url == "" {
		t.Skip("set RAPIBASE_TEST_DB to run the webhook integration test")
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
	// Migrate twice: every statement must be idempotent.
	if err := db.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	wh := &webhooks.Webhook{Name: "t", URL: "http://127.0.0.1:1/x", Events: []string{"INSERT:auth_users"}, Enabled: true}
	if err := db.CreateWebhook(ctx, wh); err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM _rapibase_webhooks WHERE id = $1`, wh.ID)
	if got, err := db.GetEnabledWebhooksByEvent(ctx, "INSERT:auth_users"); err != nil || len(got) == 0 {
		t.Fatalf("by event: %v %v", got, err)
	}

	l := &webhooks.WebhookLog{WebhookID: wh.ID, DeliveryID: "evt_test", Event: "INSERT:auth_users", Payload: `{"id":"evt_test"}`, CreatedAt: time.Now()}
	if err := db.CreateDelivery(ctx, l); err != nil || l.ID == 0 {
		t.Fatalf("create delivery: %v", err)
	}

	// Fresh "sending" row: not claimable by the poller, refused for resend.
	if due, err := db.ClaimDueDeliveries(ctx, time.Minute, 10); err != nil || len(due) != 0 {
		t.Fatalf("fresh sending must not be claimed: %v %v", due, err)
	}
	if _, err := db.ClaimDelivery(ctx, l.ID); err != ErrDeliveryBusy {
		t.Fatalf("want ErrDeliveryBusy, got %v", err)
	}

	// Failed attempt with a retry in the past → the poller claims it.
	past := time.Now().Add(-time.Second)
	if err := db.FinishAttempt(ctx, l.ID, webhooks.AttemptResult{Status: webhooks.StatusPending, ResponseStatus: 503, Error: "HTTP 503", Attempts: 1, NextAttemptAt: &past}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	due, err := db.ClaimDueDeliveries(ctx, time.Minute, 10)
	if err != nil || len(due) != 1 || due[0].ID != l.ID || due[0].Status != "sending" || due[0].Attempts != 1 || due[0].DeliveryID != "evt_test" {
		t.Fatalf("claim due: %+v %v", due, err)
	}
	// Claimed once: a second poll gets nothing.
	if again, _ := db.ClaimDueDeliveries(ctx, time.Minute, 10); len(again) != 0 {
		t.Fatalf("claimed twice: %+v", again)
	}

	// Stale "sending" (worker died) is picked up again.
	_, _ = db.Pool.Exec(ctx, `UPDATE _rapibase_webhook_logs SET updated_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, l.ID)
	if stale, _ := db.ClaimDueDeliveries(ctx, time.Minute, 10); len(stale) != 1 {
		t.Fatalf("stale sending not reclaimed")
	}

	// Delivered → listed with its status; manual resend allowed.
	if err := db.FinishAttempt(ctx, l.ID, webhooks.AttemptResult{Status: webhooks.StatusDelivered, ResponseStatus: 200, Attempts: 2, Success: true}); err != nil {
		t.Fatal(err)
	}
	logs, err := db.ListWebhookLogs(ctx, &wh.ID, nil, 10)
	if err != nil || len(logs) != 1 || logs[0].Status != "delivered" || logs[0].WebhookName != "t" || logs[0].Error != "" {
		t.Fatalf("list: %+v %v", logs, err)
	}
	if c, err := db.ClaimDelivery(ctx, l.ID); err != nil || c.Status != "sending" {
		t.Fatalf("manual claim: %+v %v", c, err)
	}

	// Cleanup never deletes deliveries still waiting.
	_, _ = db.Pool.Exec(ctx, `UPDATE _rapibase_webhook_logs SET status = 'pending', created_at = NOW() - INTERVAL '90 days' WHERE id = $1`, l.ID)
	if _, err := db.CleanupOldWebhookLogs(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM _rapibase_webhook_logs WHERE id = $1`, l.ID).Scan(&n)
	if n != 1 {
		t.Fatal("cleanup deleted a pending delivery")
	}
	_, _ = db.Pool.Exec(ctx, `DELETE FROM _rapibase_webhook_logs WHERE id = $1`, l.ID)

	// Access log filters.
	_, _ = db.Pool.Exec(ctx, `DELETE FROM _rapibase_access_log WHERE user_agent = 'itest'`)
	if err := db.InsertAccessLogs(ctx, []AccessLogEntry{
		{IP: "1.1.1.1", Method: "GET", Path: "/api/v1/rest/pets", Status: 200, UserAgent: "itest"},
		{IP: "1.1.1.1", Method: "POST", Path: "/api/v1/auth/signin", Status: 401, UserAgent: "itest"},
		{IP: "2.2.2.2", Method: "GET", Path: "/api/v1/rest/100%_off", Status: 404, UserAgent: "itest"},
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM _rapibase_access_log WHERE user_agent = 'itest'`)
	count := func(f AccessLogFilter) int {
		rows, err := db.ListAccessLogs(ctx, 500, 0, f)
		if err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
		n := 0
		for _, r := range rows {
			if r["user_agent"] == "itest" {
				n++
			}
		}
		return n
	}
	for _, c := range []struct {
		f    AccessLogFilter
		want int
	}{
		{AccessLogFilter{Path: "REST/pets"}, 1},
		{AccessLogFilter{Path: "/api/v1"}, 3},
		{AccessLogFilter{Path: "100%_"}, 1},
		{AccessLogFilter{Path: "%"}, 1}, // literal %, not a wildcard
		{AccessLogFilter{Status: "4xx"}, 2},
		{AccessLogFilter{Status: "401"}, 1},
		{AccessLogFilter{Status: "2xx", IP: "1.1.1.1"}, 1},
		{AccessLogFilter{Method: "post"}, 1},
	} {
		if got := count(c.f); got != c.want {
			t.Errorf("%+v: got %d, want %d", c.f, got, c.want)
		}
	}
	if _, err := db.ListAccessLogs(ctx, 10, 0, AccessLogFilter{Status: "abc"}); err == nil {
		t.Error("invalid status must error")
	}
}
