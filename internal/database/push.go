package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rapibase/rapibase/internal/push"
)

// GetPushConfig retrieves push configuration for a platform
func (db *DB) GetPushConfig(ctx context.Context, platform string) (*push.PushConfig, error) {
	var config push.PushConfig
	var configJSON []byte

	err := db.Pool.QueryRow(ctx, `
		SELECT id, platform, config, enabled, created_at, updated_at
		FROM _rapibase_push_config
		WHERE platform = $1
	`, platform).Scan(&config.ID, &config.Platform, &configJSON, &config.Enabled, &config.CreatedAt, &config.UpdatedAt)
	if err != nil {
		return nil, err
	}

	json.Unmarshal(configJSON, &config.Config)
	return &config, nil
}

// UpsertPushConfig creates or updates push configuration
func (db *DB) UpsertPushConfig(ctx context.Context, config *push.PushConfig) error {
	configJSON, err := json.Marshal(config.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	err = db.Pool.QueryRow(ctx, `
		INSERT INTO _rapibase_push_config (platform, config, enabled)
		VALUES ($1, $2, $3)
		ON CONFLICT (platform) DO UPDATE SET
			config = EXCLUDED.config,
			enabled = EXCLUDED.enabled,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`, config.Platform, configJSON, config.Enabled).Scan(&config.ID, &config.CreatedAt, &config.UpdatedAt)

	return err
}

// ListPushConfigs returns all push configurations
func (db *DB) ListPushConfigs(ctx context.Context) ([]push.PushConfig, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, platform, config, enabled, created_at, updated_at
		FROM _rapibase_push_config
		ORDER BY platform
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []push.PushConfig
	for rows.Next() {
		var config push.PushConfig
		var configJSON []byte

		err := rows.Scan(&config.ID, &config.Platform, &configJSON, &config.Enabled, &config.CreatedAt, &config.UpdatedAt)
		if err != nil {
			return nil, err
		}

		json.Unmarshal(configJSON, &config.Config)
		configs = append(configs, config)
	}

	return configs, rows.Err()
}

// CreatePushSubscription registers a device for a user. A device token
// belongs to one user at a time: registering it again for the same user
// updates endpoint/metadata (created=false), and registering it for another
// user moves it there (the old rows are deleted and counted in reassigned),
// so a shared phone stops getting the previous account's notifications.
func (db *DB) CreatePushSubscription(ctx context.Context, sub *push.PushSubscription) (created bool, reassigned int64, err error) {
	metadataJSON, err := json.Marshal(sub.Metadata)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		DELETE FROM _rapibase_push_subscriptions
		WHERE platform = $1 AND token = $2 AND user_id IS DISTINCT FROM $3::uuid
	`, sub.Platform, sub.Token, sub.UserID)
	if err != nil {
		return false, 0, err
	}
	reassigned = tag.RowsAffected()

	err = tx.QueryRow(ctx, `
		INSERT INTO _rapibase_push_subscriptions (user_id, platform, token, endpoint, metadata, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, platform, token) DO UPDATE SET
			endpoint = EXCLUDED.endpoint,
			metadata = EXCLUDED.metadata,
			expires_at = EXCLUDED.expires_at,
			updated_at = NOW()
		RETURNING id, created_at, updated_at, (xmax = 0)
	`, sub.UserID, sub.Platform, sub.Token, sub.Endpoint, metadataJSON, sub.ExpiresAt).
		Scan(&sub.ID, &sub.CreatedAt, &sub.UpdatedAt, &created)
	if err != nil {
		return false, 0, err
	}

	// Cap devices per user: keep the most recently refreshed ones.
	if _, err := tx.Exec(ctx, `
		DELETE FROM _rapibase_push_subscriptions
		WHERE user_id = $1::uuid AND id NOT IN (
			SELECT id FROM _rapibase_push_subscriptions
			WHERE user_id = $1::uuid
			ORDER BY updated_at DESC NULLS LAST, id DESC
			LIMIT $2)
	`, sub.UserID, push.MaxDevicesPerUser); err != nil {
		return false, 0, err
	}
	return created, reassigned, tx.Commit(ctx)
}

// AuthUserExists reports whether an app user with this id exists.
func (db *DB) AuthUserExists(ctx context.Context, userID string) (bool, error) {
	var ok bool
	err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth_users WHERE id = $1::uuid)`, userID).Scan(&ok)
	return ok, err
}

// ListSubscriptions returns registered devices, newest first. userID ""
// lists every user's devices.
func (db *DB) ListSubscriptions(ctx context.Context, userID string, limit int) ([]push.PushSubscription, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, user_id, platform, token, endpoint, metadata, created_at, updated_at, expires_at
		FROM _rapibase_push_subscriptions`
	args := []interface{}{limit}
	if userID != "" {
		q += ` WHERE user_id = $2::uuid`
		args = append(args, userID)
	}
	q += ` ORDER BY updated_at DESC NULLS LAST, id DESC LIMIT $1`
	rows, err := db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subs, err := scanSubscriptions(rows)
	if subs == nil {
		subs = []push.PushSubscription{}
	}
	return subs, err
}

// GetSubscriptionsByUserID returns all subscriptions for a user
func (db *DB) GetSubscriptionsByUserID(ctx context.Context, userID string) ([]push.PushSubscription, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, platform, token, endpoint, metadata, created_at, updated_at, expires_at
		FROM _rapibase_push_subscriptions
		WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubscriptions(rows)
}

// GetAllSubscriptions returns all subscriptions for a platform
func (db *DB) GetAllSubscriptions(ctx context.Context, platform string) ([]push.PushSubscription, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, platform, token, endpoint, metadata, created_at, updated_at, expires_at
		FROM _rapibase_push_subscriptions
		WHERE platform = $1 AND (expires_at IS NULL OR expires_at > NOW())
	`, platform)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubscriptions(rows)
}

// GetSubscriptionsByUserIDs returns subscriptions for multiple users
func (db *DB) GetSubscriptionsByUserIDs(ctx context.Context, userIDs []string) ([]push.PushSubscription, error) {
	if len(userIDs) == 0 {
		return []push.PushSubscription{}, nil
	}

	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, platform, token, endpoint, metadata, created_at, updated_at, expires_at
		FROM _rapibase_push_subscriptions
		WHERE user_id = ANY($1) AND (expires_at IS NULL OR expires_at > NOW())
	`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubscriptions(rows)
}

// GetSubscriptionsByFilter returns subscriptions for users matching filter conditions
func (db *DB) GetSubscriptionsByFilter(ctx context.Context, filter *push.UserFilter) ([]push.PushSubscription, error) {
	if filter == nil {
		return []push.PushSubscription{}, nil
	}

	where, args := userFilterSQL(filter, "au", 1)
	query := `
		SELECT ps.id, ps.user_id, ps.platform, ps.token, ps.endpoint, ps.metadata, ps.created_at, ps.updated_at, ps.expires_at
		FROM _rapibase_push_subscriptions ps
		INNER JOIN auth_users au ON ps.user_id = au.id
		WHERE (ps.expires_at IS NULL OR ps.expires_at > NOW())` + where

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubscriptions(rows)
}

// userFilterSQL turns a UserFilter into " AND ..." conditions over the
// auth_users alias, with placeholders numbered from argStart.
func userFilterSQL(filter *push.UserFilter, alias string, argStart int) (string, []interface{}) {
	var sb strings.Builder
	args := []interface{}{}
	n := argStart
	if filter == nil {
		return "", args
	}
	if filter.Role != nil {
		fmt.Fprintf(&sb, " AND %s.role = $%d", alias, n)
		args = append(args, *filter.Role)
		n++
	}
	if filter.EmailVerified != nil {
		if *filter.EmailVerified {
			fmt.Fprintf(&sb, " AND %s.email_confirmed_at IS NOT NULL", alias)
		} else {
			fmt.Fprintf(&sb, " AND %s.email_confirmed_at IS NULL", alias)
		}
	}
	if filter.CreatedAfter != nil {
		fmt.Fprintf(&sb, " AND %s.created_at >= $%d", alias, n)
		args = append(args, *filter.CreatedAfter)
		n++
	}
	if filter.CreatedBefore != nil {
		fmt.Fprintf(&sb, " AND %s.created_at <= $%d", alias, n)
		args = append(args, *filter.CreatedBefore)
		n++
	}
	if len(filter.Metadata) > 0 {
		metadataJSON, _ := json.Marshal(filter.Metadata)
		fmt.Fprintf(&sb, " AND %s.metadata @> $%d::jsonb", alias, n)
		args = append(args, string(metadataJSON))
	}
	return sb.String(), args
}

// NotificationTarget says who a send goes to; exactly one mode applies.
type NotificationTarget struct {
	UserIDs []string         // user_id / user_ids
	Filter  *push.UserFilter // filter
	All     bool             // broadcast
	Label   string           // "user_id" | "user_ids" | "filter" | "broadcast"
	Inbox   bool             // false = history row only, no per-user inbox rows
}

// CreateNotificationBatch stores one send: an inbox row per recipient
// (every matching app user, with or without devices) sharing a batch_id.
// With no recipients, or inbox=false, a single history row without user_id
// is kept so the dashboard still shows the send. Returns the batch id and
// how many inbox rows were created.
func (db *DB) CreateNotificationBatch(ctx context.Context, n *push.Notification, t NotificationTarget) (string, int64, error) {
	dataJSON, err := json.Marshal(n.Data)
	if err != nil || n.Data == nil {
		dataJSON = []byte("{}")
	}
	batch := uuid.NewString()
	base := []interface{}{n.Title, n.Body, dataJSON, n.SentAt, batch, t.Label}

	var inboxRows int64
	if t.Inbox {
		q := `INSERT INTO _rapibase_notifications (user_id, title, body, data, sent_at, batch_id, target)
			SELECT au.id, $1::text, $2::text, $3::jsonb, $4::timestamptz, $5::uuid, $6::text FROM auth_users au WHERE TRUE`
		args := append([]interface{}{}, base...)
		switch {
		case t.Filter != nil:
			where, fargs := userFilterSQL(t.Filter, "au", len(args)+1)
			q += where
			args = append(args, fargs...)
		case !t.All:
			q += fmt.Sprintf(" AND au.id = ANY($%d::uuid[])", len(args)+1)
			args = append(args, t.UserIDs)
		}
		tag, err := db.Pool.Exec(ctx, q, args...)
		if err != nil {
			return "", 0, err
		}
		inboxRows = tag.RowsAffected()
	}
	if inboxRows == 0 {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO _rapibase_notifications (user_id, title, body, data, sent_at, batch_id, target)
			VALUES (NULL, $1::text, $2::text, $3::jsonb, $4::timestamptz, $5::uuid, $6::text)`, base...); err != nil {
			return "", 0, err
		}
	}
	return batch, inboxRows, nil
}

// DeleteSubscription deletes a subscription by ID
func (db *DB) DeleteSubscription(ctx context.Context, id int64) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM _rapibase_push_subscriptions WHERE id = $1`, id)
	return err
}

// DeleteSubscriptionsByToken removes a device token. userID "" removes it
// from whichever user holds it (service key); otherwise only from that user.
func (db *DB) DeleteSubscriptionsByToken(ctx context.Context, userID, token string) (int64, error) {
	q := `DELETE FROM _rapibase_push_subscriptions WHERE token = $1`
	args := []interface{}{token}
	if userID != "" {
		q += ` AND user_id = $2::uuid`
		args = append(args, userID)
	}
	tag, err := db.Pool.Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// scanSubscriptions helper to scan subscription rows
func scanSubscriptions(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]push.PushSubscription, error) {
	var subs []push.PushSubscription
	for rows.Next() {
		var sub push.PushSubscription
		var metadataJSON []byte

		err := rows.Scan(&sub.ID, &sub.UserID, &sub.Platform, &sub.Token, &sub.Endpoint, &metadataJSON, &sub.CreatedAt, &sub.UpdatedAt, &sub.ExpiresAt)
		if err != nil {
			return nil, err
		}

		json.Unmarshal(metadataJSON, &sub.Metadata)
		subs = append(subs, sub)
	}

	return subs, rows.Err()
}

// GetUserNotifications returns notifications for a user
func (db *DB) GetUserNotifications(ctx context.Context, userID string, unreadOnly bool, limit int) ([]push.Notification, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	query := `
		SELECT id, user_id, title, body, data, sent_at, read_at, created_at
		FROM _rapibase_notifications
		WHERE user_id = $1
	`
	if unreadOnly {
		query += " AND read_at IS NULL"
	}
	query += " ORDER BY created_at DESC LIMIT $2"

	rows, err := db.Pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifs []push.Notification
	for rows.Next() {
		var notif push.Notification
		var dataJSON []byte

		err := rows.Scan(&notif.ID, &notif.UserID, &notif.Title, &notif.Body, &dataJSON, &notif.SentAt, &notif.ReadAt, &notif.CreatedAt)
		if err != nil {
			return nil, err
		}

		json.Unmarshal(dataJSON, &notif.Data)
		notifs = append(notifs, notif)
	}

	return notifs, rows.Err()
}

// MarkNotificationRead marks a notification as read
func (db *DB) MarkNotificationRead(ctx context.Context, userID string, notifID int64) error {
	now := time.Now()
	result, err := db.Pool.Exec(ctx, `
		UPDATE _rapibase_notifications
		SET read_at = $1
		WHERE id = $2 AND user_id = $3 AND read_at IS NULL
	`, now, notifID, userID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("notification not found or already read")
	}

	return nil
}

// MarkAllNotificationsRead marks all user notifications as read
func (db *DB) MarkAllNotificationsRead(ctx context.Context, userID string) (int64, error) {
	now := time.Now()
	result, err := db.Pool.Exec(ctx, `
		UPDATE _rapibase_notifications
		SET read_at = $1
		WHERE user_id = $2 AND read_at IS NULL
	`, now, userID)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected(), nil
}

// GetUnreadNotificationCount returns the count of unread notifications
func (db *DB) GetUnreadNotificationCount(ctx context.Context, userID string) (int64, error) {
	var count int64
	err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM _rapibase_notifications
		WHERE user_id = $1 AND read_at IS NULL
	`, userID).Scan(&count)

	return count, err
}

// ListAllNotifications returns the send history for the dashboard: one row
// per send (batch), with how many inboxes it reached.
func (db *DB) ListAllNotifications(ctx context.Context, limit int) ([]push.Notification, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := db.Pool.Query(ctx, `
		WITH g AS (
			SELECT MIN(id) AS id, COUNT(user_id) AS recipients
			FROM _rapibase_notifications
			GROUP BY COALESCE(batch_id::text, 'id:' || id::text)
			ORDER BY MIN(id) DESC
			LIMIT $1
		)
		SELECT n.id, CASE WHEN g.recipients = 1 THEN n.user_id END, n.title, n.body, n.data,
		       n.sent_at, n.read_at, n.created_at, n.batch_id::text, COALESCE(n.target, ''), g.recipients
		FROM g JOIN _rapibase_notifications n ON n.id = g.id
		ORDER BY n.id DESC
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notifs := []push.Notification{}
	for rows.Next() {
		var notif push.Notification
		var dataJSON []byte
		var recipients int64

		err := rows.Scan(&notif.ID, &notif.UserID, &notif.Title, &notif.Body, &dataJSON, &notif.SentAt, &notif.ReadAt, &notif.CreatedAt,
			&notif.BatchID, &notif.Target, &recipients)
		if err != nil {
			return nil, err
		}
		notif.Recipients = &recipients

		json.Unmarshal(dataJSON, &notif.Data)
		notifs = append(notifs, notif)
	}

	return notifs, rows.Err()
}
