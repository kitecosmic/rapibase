// Realtime cabling: builds the realtime.Service with the project's
// existing JWT manager and api keys, mounts the WebSocket endpoint on
// the dashboard route, and hands the constructed Service back to main
// so it can call Bootstrap and Run.
//
// Kept separate from routes.go so the realtime subsystem stays opt-in:
// if REALTIME_ENABLED=false (or the WAL bootstrap fails), the rest of
// rapibase boots normally without it.

package api

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"

	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/realtime"
	"github.com/rapibase/rapibase/internal/realtime/bus"
	"github.com/rapibase/rapibase/internal/realtime/hub"
	"github.com/rapibase/rapibase/internal/realtime/metrics"
	"github.com/rapibase/rapibase/internal/realtime/rpc"
	"github.com/rapibase/rapibase/internal/realtime/transport"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// SetupRealtime constructs the realtime service and mounts the
// WebSocket endpoint on the supplied Fiber app. Returns the service
// so the caller can run Bootstrap/Run on its own goroutine.
//
// The endpoint is mounted at /api/realtime/v1 — not under /dashboard,
// because client apps consume it directly with their api keys, the
// same way they consume the REST API at /api/v1/*. Putting it under
// /dashboard would suggest it is only for the admin UI, which it is
// not.
func SetupRealtime(ctx context.Context, app *fiber.App, db *database.DB, cfg *config.Config) (*realtime.Service, error) {
	jwtManager := auth.NewJWTManagerWithExpiry(cfg.JWTSecret, cfg.JWTExpiry)
	authValidator := realtime.NewJWTAuthValidator(jwtManager, cfg.AnonKey, cfg.ServiceKey)

	// Mirror REST row-level security on the realtime channel: events for
	// RLS-managed tables are scoped to the owning subscriber. Tables
	// without RLS config are unaffected, so realtime keeps working.
	rowAuth := newRLSRowAuthorizer(ctx, db, 30*time.Second)

	// Single-node bus by default. Multi-node deployments swap this for
	// bus.NewNATS once that implementation is wired.
	eventBus := bus.NewLocal(1024)

	// Prometheus-backed recorder. /api/realtime/metrics exposes the
	// standard text exposition format, gated behind the service key so
	// only operators (not anon clients) can scrape it.
	prom := metrics.NewProm(nil)

	rtCfg := realtime.Config{
		WAL: wal.Config{
			ConnString:      cfg.DatabaseURL,
			SlotName:        cfg.RealtimeSlotName,
			PublicationName: cfg.RealtimePublicationName,
		},
		Leader: wal.LeaderConfig{
			ConnString: cfg.DatabaseURL,
			LockKey:    cfg.RealtimeLockKey,
		},
		Hub: hub.Config{
			Shards:              64,
			SubscriberQueueSize: 1024,
			ResumeBufferSize:    4096,
		},
		Transport: transport.HandlerDeps{
			MaxPayloadBytes:     1 << 20, // 1 MiB
			SubscriberQueueSize: 1024,
			ServerVersion:       "1.0.0",
		},
		Permissions: realtime.PermissiveChecker{},
		RowAuth:     rowAuth,
		Auth:        authValidator,
		RPC:         rpc.NewRegistry(),
		Bus:         eventBus,
		Metrics:     prom,
	}

	svc, err := realtime.New(rtCfg)
	if err != nil {
		return nil, fmt.Errorf("realtime: build service: %w", err)
	}

	handlers := svc.Handler().FiberHandlers()
	app.Get("/api/realtime/v1", handlers...)

	// Mount markdown docs endpoints so the dashboard's Realtime tab
	// (and any external doc site) can render them from one source.
	MountRealtimeDocs(app)

	// Metrics endpoint: gated by header X-Service-Key (or apikey query
	// param) matching the project's service key. Scrapers configure
	// the credential once; anon clients cannot read internal counters.
	if mh := svc.MetricsHandler(); mh != nil {
		serviceKey := cfg.ServiceKey
		gateMetrics := func(c *fiber.Ctx) error {
			provided := c.Get("X-Service-Key")
			if provided == "" {
				provided = c.Query("apikey")
			}
			if provided != serviceKey {
				return fiber.NewError(fiber.StatusUnauthorized, "service key required")
			}
			return c.Next()
		}
		app.Get("/api/realtime/metrics", gateMetrics, adaptor.HTTPHandler(mh))
	}

	return svc, nil
}
