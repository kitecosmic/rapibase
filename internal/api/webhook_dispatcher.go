package api

import (
	"context"
	"encoding/json"

	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/webhooks"
)

// WebhookDispatcher wraps the webhooks.Dispatcher for use in handlers
type WebhookDispatcher struct {
	dispatcher *webhooks.Dispatcher
}

// NewWebhookDispatcher creates a new webhook dispatcher
func NewWebhookDispatcher(db *database.DB) *WebhookDispatcher {
	d := webhooks.NewDispatcher(db, 10)
	d.Start() // retries stored in the database
	w := &WebhookDispatcher{dispatcher: d}
	// New app users fire INSERT:auth_users. AuthUserPublic has no
	// password hash or tokens, so the payload is safe to send out.
	db.OnAuthUserCreated = func(u *database.AuthUserPublic) {
		b, err := json.Marshal(u)
		if err != nil {
			return
		}
		var row map[string]interface{}
		if json.Unmarshal(b, &row) != nil {
			return
		}
		if row["role"] == "" {
			delete(row, "role")
		}
		w.Dispatch(context.Background(), webhooks.EventInsert, webhooks.AuthUsersTable, row, nil)
	}
	return w
}

// Inner returns the underlying dispatcher (manual resends).
func (w *WebhookDispatcher) Inner() *webhooks.Dispatcher { return w.dispatcher }

// Dispatch sends a webhook event
func (w *WebhookDispatcher) Dispatch(ctx context.Context, eventType, tableName string, data, oldData map[string]interface{}) {
	if w.dispatcher != nil {
		w.dispatcher.Dispatch(ctx, eventType, tableName, data, oldData)
	}
}
