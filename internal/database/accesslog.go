package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AccessLogEntry is one recorded HTTP request on the API surface.
type AccessLogEntry struct {
	IP        string
	Method    string
	Path      string
	Status    int
	LatencyMS int64
	KeyType   string // "anon", "service", or "" when no api key
	UserID    string // JWT subject (app UUID or dashboard user id) if any
	UserRole  string
	UserAgent string
}

// InsertAccessLogs writes a batch of entries in a single round-trip.
func (db *DB) InsertAccessLogs(ctx context.Context, entries []AccessLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, e := range entries {
		batch.Queue(
			`INSERT INTO _rapibase_access_log
			 (ip, method, path, status, latency_ms, key_type, user_id, user_role, user_agent)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			e.IP, e.Method, e.Path, e.Status, e.LatencyMS,
			nullStr(e.KeyType), nullStr(e.UserID), nullStr(e.UserRole), nullStr(e.UserAgent),
		)
	}
	br := db.Pool.SendBatch(ctx, batch)
	defer br.Close()
	for range entries {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// DeleteAccessLogsOlderThan prunes entries beyond the retention window.
func (db *DB) DeleteAccessLogsOlderThan(ctx context.Context, days int) error {
	if days <= 0 {
		return nil
	}
	// days is a validated int from config, not user input — safe to interpolate.
	_, err := db.Pool.Exec(ctx,
		fmt.Sprintf(`DELETE FROM _rapibase_access_log WHERE ts < NOW() - INTERVAL '%d days'`, days),
	)
	return err
}

// ListAccessLogs returns recent entries (newest first), optionally
// filtered by exact client IP, for the dashboard.
func (db *DB) ListAccessLogs(ctx context.Context, limit, offset int, ip string) ([]map[string]interface{}, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, ts, ip, method, path, status, latency_ms, key_type, user_id, user_role, user_agent
	          FROM _rapibase_access_log`
	var args []interface{}
	if ip != "" {
		query += ` WHERE ip = $1`
		args = append(args, ip)
	}
	query += fmt.Sprintf(` ORDER BY ts DESC LIMIT %d OFFSET %d`, limit, offset)

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgxRowsToMaps(rows)
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
