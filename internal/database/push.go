package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

// CreatePushSubscription creates a new push subscription
func (db *DB) CreatePushSubscription(ctx context.Context, sub *push.PushSubscription) error {
	metadataJSON, err := json.Marshal(sub.Metadata)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	err = db.Pool.QueryRow(ctx, `
		INSERT INTO _rapibase_push_subscriptions (user_id, platform, token, endpoint, metadata, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, platform, token) DO UPDATE SET
			endpoint = EXCLUDED.endpoint,
			metadata = EXCLUDED.metadata,
			expires_at = EXCLUDED.expires_at
		RETURNING id, created_at
	`, sub.UserID, sub.Platform, sub.Token, sub.Endpoint, metadataJSON, sub.ExpiresAt).
		Scan(&sub.ID, &sub.CreatedAt)

	return err
}

// GetSubscriptionsByUserID returns all subscriptions for a user
func (db *DB) GetSubscriptionsByUserID(ctx context.Context, userID string) ([]push.PushSubscription, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, platform, token, endpoint, metadata, created_at, expires_at
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
		SELECT id, user_id, platform, token, endpoint, metadata, created_at, expires_at
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
		SELECT id, user_id, platform, token, endpoint, metadata, created_at, expires_at
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

	// Build dynamic query to filter auth_users
	query := `
		SELECT ps.id, ps.user_id, ps.platform, ps.token, ps.endpoint, ps.metadata, ps.created_at, ps.expires_at
		FROM _rapibase_push_subscriptions ps
		INNER JOIN auth_users au ON ps.user_id = au.id::text
		WHERE (ps.expires_at IS NULL OR ps.expires_at > NOW())
	`
	args := []interface{}{}
	argNum := 1

	if filter.Role != nil {
		query += fmt.Sprintf(" AND au.role = $%d", argNum)
		args = append(args, *filter.Role)
		argNum++
	}

	if filter.EmailVerified != nil {
		query += fmt.Sprintf(" AND au.email_confirmed_at IS %s", map[bool]string{true: "NOT NULL", false: "NULL"}[*filter.EmailVerified])
	}

	if filter.CreatedAfter != nil {
		query += fmt.Sprintf(" AND au.created_at >= $%d", argNum)
		args = append(args, *filter.CreatedAfter)
		argNum++
	}

	if filter.CreatedBefore != nil {
		query += fmt.Sprintf(" AND au.created_at <= $%d", argNum)
		args = append(args, *filter.CreatedBefore)
		argNum++
	}

	// Metadata filters using JSONB containment
	if len(filter.Metadata) > 0 {
		metadataJSON, _ := json.Marshal(filter.Metadata)
		query += fmt.Sprintf(" AND au.metadata @> $%d::jsonb", argNum)
		args = append(args, string(metadataJSON))
		argNum++
	}

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSubscriptions(rows)
}

// DeleteSubscription deletes a subscription by ID
func (db *DB) DeleteSubscription(ctx context.Context, id int64) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM _rapibase_push_subscriptions WHERE id = $1`, id)
	return err
}

// DeleteUserSubscription deletes a user's subscription by token
func (db *DB) DeleteUserSubscription(ctx context.Context, userID, token string) error {
	_, err := db.Pool.Exec(ctx, `
		DELETE FROM _rapibase_push_subscriptions 
		WHERE user_id = $1 AND token = $2
	`, userID, token)
	return err
}

// scanSubscriptions helper to scan subscription rows
func scanSubscriptions(rows interface {
	Next() bool
	Scan(...interface{}) error
}) ([]push.PushSubscription, error) {
	var subs []push.PushSubscription
	for rows.Next() {
		var sub push.PushSubscription
		var metadataJSON []byte

		err := rows.Scan(&sub.ID, &sub.UserID, &sub.Platform, &sub.Token, &sub.Endpoint, &metadataJSON, &sub.CreatedAt, &sub.ExpiresAt)
		if err != nil {
			return nil, err
		}

		json.Unmarshal(metadataJSON, &sub.Metadata)
		subs = append(subs, sub)
	}

	return subs, nil
}

// CreateNotification creates a new notification
func (db *DB) CreateNotification(ctx context.Context, notif *push.Notification) error {
	dataJSON, err := json.Marshal(notif.Data)
	if err != nil {
		dataJSON = []byte("{}")
	}

	err = db.Pool.QueryRow(ctx, `
		INSERT INTO _rapibase_notifications (user_id, title, body, data, sent_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`, notif.UserID, notif.Title, notif.Body, dataJSON, notif.SentAt).
		Scan(&notif.ID, &notif.CreatedAt)

	return err
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

// ListAllNotifications returns all notifications (for admin)
func (db *DB) ListAllNotifications(ctx context.Context, limit int) ([]push.Notification, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, title, body, data, sent_at, read_at, created_at
		FROM _rapibase_notifications
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
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
