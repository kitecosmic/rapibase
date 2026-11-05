package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	`, eventJSON(event))
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

const webhookLogCols = `l.id, COALESCE(l.webhook_id, 0), COALESCE(w.name, '(borrado)'), COALESCE(l.delivery_id, ''),
	l.status, l.next_attempt_at, l.event, COALESCE(l.payload, ''), COALESCE(l.response_status, 0),
	COALESCE(l.response_body, ''), COALESCE(l.attempts, 0), COALESCE(l.success, false), COALESCE(l.error, ''), l.created_at`

func scanWebhookLog(row interface{ Scan(...any) error }) (webhooks.WebhookLog, error) {
	var l webhooks.WebhookLog
	err := row.Scan(&l.ID, &l.WebhookID, &l.WebhookName, &l.DeliveryID, &l.Status, &l.NextAttemptAt,
		&l.Event, &l.Payload, &l.ResponseStatus, &l.ResponseBody, &l.Attempts, &l.Success, &l.Error, &l.CreatedAt)
	return l, err
}

// CreateDelivery stores a new delivery, already claimed (status sending)
// by the worker that is about to send it.
func (db *DB) CreateDelivery(ctx context.Context, l *webhooks.WebhookLog) error {
	return db.Pool.QueryRow(ctx, `
		INSERT INTO _rapibase_webhook_logs
		(webhook_id, delivery_id, event, payload, status, attempts, success, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'sending', 0, false, $5, NOW())
		RETURNING id
	`, l.WebhookID, l.DeliveryID, l.Event, l.Payload, l.CreatedAt).Scan(&l.ID)
}

// FinishAttempt records the outcome of one attempt.
func (db *DB) FinishAttempt(ctx context.Context, id int64, r webhooks.AttemptResult) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE _rapibase_webhook_logs
		SET status = $2, response_status = $3, response_body = $4, error = NULLIF($5, ''),
		    attempts = $6, success = $7, next_attempt_at = $8, updated_at = NOW()
		WHERE id = $1
	`, id, r.Status, r.ResponseStatus, r.ResponseBody, r.Error, r.Attempts, r.Success, r.NextAttemptAt)
	return err
}

// ClaimDueDeliveries takes due retries (and rows left in "sending" by a
// worker that died) with SKIP LOCKED, so two pollers never send the same one.
func (db *DB) ClaimDueDeliveries(ctx context.Context, staleAfter time.Duration, limit int) ([]webhooks.WebhookLog, error) {
	rows, err := db.Pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM _rapibase_webhook_logs
			WHERE (status = 'pending' AND next_attempt_at <= NOW())
			   OR (status = 'sending' AND updated_at < NOW() - make_interval(secs => $1))
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE _rapibase_webhook_logs l SET status = 'sending', updated_at = NOW()
		FROM due LEFT JOIN _rapibase_webhooks w ON false
		WHERE l.id = due.id
		RETURNING `+webhookLogCols, staleAfter.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []webhooks.WebhookLog
	for rows.Next() {
		l, err := scanWebhookLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ErrDeliveryBusy: the delivery is being sent right now.
var ErrDeliveryBusy = fmt.Errorf("this delivery is being sent right now")

// ClaimDelivery claims one delivery for a manual resend. A row in
// "sending" with a fresh updated_at is in flight and is refused.
func (db *DB) ClaimDelivery(ctx context.Context, id int64) (*webhooks.WebhookLog, error) {
	row := db.Pool.QueryRow(ctx, `
		UPDATE _rapibase_webhook_logs l SET status = 'sending', next_attempt_at = NULL, updated_at = NOW()
		FROM (SELECT id FROM _rapibase_webhook_logs
		      WHERE id = $1 AND NOT (status = 'sending' AND updated_at > NOW() - INTERVAL '2 minutes')
		      FOR UPDATE SKIP LOCKED) t
		LEFT JOIN _rapibase_webhooks w ON false
		WHERE l.id = t.id
		RETURNING `+webhookLogCols, id)
	l, err := scanWebhookLog(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			_ = db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM _rapibase_webhook_logs WHERE id = $1)`, id).Scan(&exists)
			if exists {
				return nil, ErrDeliveryBusy
			}
		}
		return nil, err
	}
	return &l, nil
}

// ListWebhookLogs returns webhook logs with optional filters
func (db *DB) ListWebhookLogs(ctx context.Context, webhookID *int64, success *bool, limit int) ([]webhooks.WebhookLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// COALESCE: al borrar un webhook sus logs quedan con webhook_id NULL
	// (ON DELETE SET NULL) y sin él, el Scan a string rompía el listado.
	query := `SELECT ` + webhookLogCols + `
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
		l, err := scanWebhookLog(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, l)
	}

	return result, rows.Err()
}

// CleanupOldWebhookLogs deletes logs older than the specified duration
func (db *DB) CleanupOldWebhookLogs(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := db.Pool.Exec(ctx, `
		DELETE FROM _rapibase_webhook_logs WHERE created_at < $1 AND status NOT IN ('pending', 'sending')
	`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// eventJSON renders ["event"] for the jsonb containment check. Marshalled,
// not formatted, so an event name with quotes can't break the JSON.
func eventJSON(event string) string {
	b, _ := json.Marshal([]string{event})
	return string(b)
}
