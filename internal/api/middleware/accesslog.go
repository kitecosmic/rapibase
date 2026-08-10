package middleware

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
)

// AccessLogger records every API request (IP, method, path, status,
// latency, caller identity) to stdout and to the _rapibase_access_log
// table. DB writes are batched on a background goroutine so they never
// add latency to the request path; if the buffer fills, entries are
// dropped rather than blocking.
type AccessLogger struct {
	db            *database.DB
	ch            chan database.AccessLogEntry
	retentionDays int
}

func NewAccessLogger(db *database.DB, retentionDays int) *AccessLogger {
	return &AccessLogger{
		db:            db,
		ch:            make(chan database.AccessLogEntry, 2048),
		retentionDays: retentionDays,
	}
}

// Middleware returns the Fiber handler. It only records the security-
// relevant surface (/api and /mcp), skipping SPA static assets. Caller
// identity is read after c.Next() because the auth middlewares set it
// downstream.
func (l *AccessLogger) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		path := c.Path()
		if !strings.HasPrefix(path, "/api") && !strings.HasPrefix(path, "/mcp") {
			return c.Next()
		}
		// The Docker HEALTHCHECK hits /api/v1/health every 30s: recording it
		// would drown the log in noise (2,880 identical lines/day) without
		// adding any security signal — the endpoint is public and dataless.
		if path == "/api/v1/health" {
			return c.Next()
		}

		start := time.Now()
		err := c.Next()

		// On a handler error the global ErrorHandler runs *after* this
		// middleware returns, so c.Response() still shows 200 — derive
		// the real status from the fiber error instead.
		status := c.Response().StatusCode()
		if err != nil {
			if fe, ok := err.(*fiber.Error); ok {
				status = fe.Code
			} else {
				status = fiber.StatusInternalServerError
			}
		}

		entry := database.AccessLogEntry{
			IP:        c.IP(),
			Method:    c.Method(),
			Path:      path,
			Status:    status,
			LatencyMS: time.Since(start).Milliseconds(),
			KeyType:   localStr(c, "apiKeyType"),
			UserID:    localStr(c, "userID"),
			UserRole:  localStr(c, "userRole"),
			UserAgent: c.Get("User-Agent"),
		}

		log.Printf("access ip=%s method=%s path=%s status=%d lat=%dms key=%s uid=%s role=%s",
			orDash(entry.IP), entry.Method, entry.Path, entry.Status, entry.LatencyMS,
			orDash(entry.KeyType), orDash(entry.UserID), orDash(entry.UserRole))

		select {
		case l.ch <- entry:
		default:
			// Buffer full — drop rather than block the request.
		}
		return err
	}
}

// Run drains buffered entries into the DB in batches and prunes old
// rows until ctx is cancelled. Start it once on its own goroutine.
func (l *AccessLogger) Run(ctx context.Context) {
	flush := time.NewTicker(2 * time.Second)
	prune := time.NewTicker(6 * time.Hour)
	defer flush.Stop()
	defer prune.Stop()

	buf := make([]database.AccessLogEntry, 0, 256)
	doFlush := func() {
		if len(buf) == 0 {
			return
		}
		if err := l.db.InsertAccessLogs(ctx, buf); err != nil {
			log.Printf("⚠️  access log: batch insert failed (%d entries): %v", len(buf), err)
		}
		buf = buf[:0]
	}

	for {
		select {
		case <-ctx.Done():
			doFlush()
			return
		case e := <-l.ch:
			buf = append(buf, e)
			if len(buf) >= 256 {
				doFlush()
			}
		case <-flush.C:
			doFlush()
		case <-prune.C:
			if err := l.db.DeleteAccessLogsOlderThan(ctx, l.retentionDays); err != nil {
				log.Printf("⚠️  access log: prune failed: %v", err)
			}
		}
	}
}

func localStr(c *fiber.Ctx, key string) string {
	if v, ok := c.Locals(key).(string); ok {
		return v
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
