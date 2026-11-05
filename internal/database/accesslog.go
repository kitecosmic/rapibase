package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

// AccessLogFilter narrows the dashboard listing. Empty fields don't filter.
type AccessLogFilter struct {
	IP     string // exact client IP
	Path   string // substring of the path, case-insensitive
	Status string // "404" exact, or a class: "2xx", "4xx", "5xx"
	Method string // GET, POST, ...
}

// ListAccessLogs returns recent entries (newest first) for the dashboard.
func (db *DB) ListAccessLogs(ctx context.Context, limit, offset int, f AccessLogFilter) ([]map[string]interface{}, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var where []string
	var args []interface{}
	add := func(cond string, v interface{}) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.IP != "" {
		add("ip = $%d", f.IP)
	}
	if f.Path != "" {
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Path)
		add("path ILIKE '%%' || $%d || '%%'", esc)
	}
	if st := strings.ToLower(strings.TrimSpace(f.Status)); st != "" {
		if len(st) == 3 && st[1:] == "xx" && st[0] >= '1' && st[0] <= '5' {
			add("status / 100 = $%d", int(st[0]-'0'))
		} else if n, err := strconv.Atoi(st); err == nil {
			add("status = $%d", n)
		} else {
			return nil, fmt.Errorf("status must be a code like 404 or a class like 4xx")
		}
	}
	if f.Method != "" {
		add("method = $%d", strings.ToUpper(f.Method))
	}

	query := `SELECT id, ts, ip, method, path, status, latency_ms, key_type, user_id, user_role, user_agent
	          FROM _rapibase_access_log`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
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
