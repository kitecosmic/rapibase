package api

import (
	"context"

	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/webhooks"
)

// WebhookDispatcher wraps the webhooks.Dispatcher for use in handlers
type WebhookDispatcher struct {
	dispatcher *webhooks.Dispatcher
}

// NewWebhookDispatcher creates a new webhook dispatcher
func NewWebhookDispatcher(db *database.DB) *WebhookDispatcher {
	return &WebhookDispatcher{
		dispatcher: webhooks.NewDispatcher(db, 10),
	}
}

// Dispatch sends a webhook event
func (w *WebhookDispatcher) Dispatch(ctx context.Context, eventType, tableName string, data, oldData map[string]interface{}) {
	if w.dispatcher != nil {
		w.dispatcher.Dispatch(ctx, eventType, tableName, data, oldData)
	}
}
