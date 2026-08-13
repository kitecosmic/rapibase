package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rapibase/rapibase/internal/webhooks"
)

// marshalFilter serializa las condiciones (nil → lista vacía, nunca null).
func marshalFilter(f []webhooks.FilterCond) ([]byte, error) {
	if f == nil {
		f = []webhooks.FilterCond{}
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal filter: %w", err)
	}
	return b, nil
}

// CreateWebhook creates a new webhook
func (db *DB) CreateWebhook(ctx context.Context, webhook *webhooks.Webhook) error {
	eventsJSON, err := json.Marshal(webhook.Events)
	if err != nil {
		return fmt.Errorf("failed to marshal events: %w", err)
	}

	headersJSON, err := json.Marshal(webhook.Headers)
	if err != nil {
		return fmt.Errorf("failed to marshal headers: %w", err)
	}

	filterJSON, err := marshalFilter(webhook.Filter)
	if err != nil {
		return err
	}

	err = db.Pool.QueryRow(ctx, `
		INSERT INTO _rapibase_webhooks (name, url, secret, events, headers, filter, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`, webhook.Name, webhook.URL, webhook.Secret, eventsJSON, headersJSON, filterJSON, webhook.Enabled).
		Scan(&webhook.ID, &webhook.CreatedAt, &webhook.UpdatedAt)

	return err
}

// GetWebhook retrieves a webhook by ID
func (db *DB) GetWebhook(ctx context.Context, id int64) (*webhooks.Webhook, error) {
	var webhook webhooks.Webhook
	var eventsJSON, headersJSON, filterJSON []byte

	err := db.Pool.QueryRow(ctx, `
		SELECT id, name, url, secret, events, headers, filter, enabled, created_at, updated_at
		FROM _rapibase_webhooks
		WHERE id = $1
	`, id).Scan(
		&webhook.ID, &webhook.Name, &webhook.URL, &webhook.Secret,
		&eventsJSON, &headersJSON, &filterJSON, &webhook.Enabled,
		&webhook.CreatedAt, &webhook.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	json.Unmarshal(eventsJSON, &webhook.Events)
	json.Unmarshal(headersJSON, &webhook.Headers)
	json.Unmarshal(filterJSON, &webhook.Filter)

	return &webhook, nil
}

// ListWebhooks returns all webhooks
func (db *DB) ListWebhooks(ctx context.Context) ([]webhooks.Webhook, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, name, url, secret, events, headers, filter, enabled, created_at, updated_at
		FROM _rapibase_webhooks
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []webhooks.Webhook
	for rows.Next() {
		var webhook webhooks.Webhook
		var eventsJSON, headersJSON, filterJSON []byte

		err := rows.Scan(
			&webhook.ID, &webhook.Name, &webhook.URL, &webhook.Secret,
			&eventsJSON, &headersJSON, &filterJSON, &webhook.Enabled,
			&webhook.CreatedAt, &webhook.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		json.Unmarshal(eventsJSON, &webhook.Events)
		json.Unmarshal(headersJSON, &webhook.Headers)
		json.Unmarshal(filterJSON, &webhook.Filter)
		result = append(result, webhook)
	}

	return result, rows.Err()
}

// UpdateWebhook updates a webhook
func (db *DB) UpdateWebhook(ctx context.Context, webhook *webhooks.Webhook) error {
	eventsJSON, err := json.Marshal(webhook.Events)
	if err != nil {
		return fmt.Errorf("failed to marshal events: %w", err)
	}

	headersJSON, err := json.Marshal(webhook.Headers)
	if err != nil {
		return fmt.Errorf("failed to marshal headers: %w", err)
	}

	filterJSON, err := marshalFilter(webhook.Filter)
	if err != nil {
		return err
	}

	result, err := db.Pool.Exec(ctx, `
		UPDATE _rapibase_webhooks
		SET name = $1, url = $2, secret = $3, events = $4, headers = $5, filter = $6, enabled = $7, updated_at = NOW()
		WHERE id = $8
	`, webhook.Name, webhook.URL, webhook.Secret, eventsJSON, headersJSON, filterJSON, webhook.Enabled, webhook.ID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("webhook not found")
	}

	return nil
}

// DeleteWebhook deletes a webhook
func (db *DB) DeleteWebhook(ctx context.Context, id int64) error {
	result, err := db.Pool.Exec(ctx, `DELETE FROM _rapibase_webhooks WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("webhook not found")
	}

	return nil
}

// ToggleWebhook enables or disables a webhook
func (db *DB) ToggleWebhook(ctx context.Context, id int64, enabled bool) error {
	result, err := db.Pool.Exec(ctx, `
		UPDATE _rapibase_webhooks SET enabled = $1, updated_at = NOW() WHERE id = $2
	`, enabled, id)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("webhook not found")
	}

	return nil
}

// GetEnabledWebhooksByEvent returns all enabled webhooks subscribed to an event
func (db *DB) GetEnabledWebhooksByEvent(ctx context.Context, event string) ([]webhooks.Webhook, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, name, url, secret, events, headers, filter, enabled, created_at, updated_at
		FROM _rapibase_webhooks
		WHERE enabled = true AND events @> $1::jsonb
	`, fmt.Sprintf(`["%s"]`, event))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []webhooks.Webhook
	for rows.Next() {
		var webhook webhooks.Webhook
		var eventsJSON, headersJSON, filterJSON []byte

		err := rows.Scan(
			&webhook.ID, &webhook.Name, &webhook.URL, &webhook.Secret,
			&eventsJSON, &headersJSON, &filterJSON, &webhook.Enabled,
			&webhook.CreatedAt, &webhook.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		json.Unmarshal(eventsJSON, &webhook.Events)
		json.Unmarshal(headersJSON, &webhook.Headers)
		json.Unmarshal(filterJSON, &webhook.Filter)
		result = append(result, webhook)
	}

	return result, rows.Err()
}

// CreateWebhookLog creates a webhook delivery log
func (db *DB) CreateWebhookLog(ctx context.Context, log *webhooks.WebhookLog) error {
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO _rapibase_webhook_logs 
		(webhook_id, event, payload, response_status, response_body, attempts, success, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, log.WebhookID, log.Event, log.Payload, log.ResponseStatus,
		log.ResponseBody, log.Attempts, log.Success, log.Error, log.CreatedAt)

	return err
}

// ListWebhookLogs returns webhook logs with optional filters
func (db *DB) ListWebhookLogs(ctx context.Context, webhookID *int64, success *bool, limit int) ([]webhooks.WebhookLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// COALESCE: al borrar un webhook sus logs quedan con webhook_id NULL
	// (ON DELETE SET NULL) y sin él, el Scan a string rompía el listado.
	query := `
		SELECT l.id, COALESCE(l.webhook_id, 0), COALESCE(w.name, '(borrado)'), l.event, l.payload,
		       COALESCE(l.response_status, 0), COALESCE(l.response_body, ''), l.attempts, l.success,
		       COALESCE(l.error, ''), l.created_at
		FROM _rapibase_webhook_logs l
		LEFT JOIN _rapibase_webhooks w ON w.id = l.webhook_id
		WHERE 1=1
	`
	args := []interface{}{}
	argNum := 1

	if webhookID != nil {
		query += fmt.Sprintf(" AND l.webhook_id = $%d", argNum)
		args = append(args, *webhookID)
		argNum++
	}

	if success != nil {
		query += fmt.Sprintf(" AND l.success = $%d", argNum)
		args = append(args, *success)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY l.created_at DESC LIMIT $%d", argNum)
	args = append(args, limit)

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []webhooks.WebhookLog
	for rows.Next() {
		var log webhooks.WebhookLog
		var webhookName *string

		err := rows.Scan(
			&log.ID, &log.WebhookID, &webhookName, &log.Event, &log.Payload,
			&log.ResponseStatus, &log.ResponseBody, &log.Attempts,
			&log.Success, &log.Error, &log.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		if webhookName != nil {
			log.WebhookName = *webhookName
		}
		result = append(result, log)
	}

	return result, rows.Err()
}

// CleanupOldWebhookLogs deletes logs older than the specified duration
func (db *DB) CleanupOldWebhookLogs(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := db.Pool.Exec(ctx, `
		DELETE FROM _rapibase_webhook_logs WHERE created_at < $1
	`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
