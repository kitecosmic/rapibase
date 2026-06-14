package api

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/realtime"
)

// rlsRowAuthorizer enforces REST row-level security on the realtime
// postgres_changes channel. It keeps an in-memory snapshot of each
// table's RLS mode (refreshed periodically from _rapibase_rls) and
// decides, per event, whether a subscriber may receive it.
//
// Tables with no RLS config are allowed through unchanged, so enabling
// this authorizer never breaks realtime for tables that have not opted
// into RLS — it only scopes the ones an admin has already secured for
// REST. Implements hub.RowAuthorizer.
type rlsRowAuthorizer struct {
	db   *database.DB
	snap atomic.Value // map[string]database.RLSConfig keyed by table name
}

func newRLSRowAuthorizer(ctx context.Context, db *database.DB, refresh time.Duration) *rlsRowAuthorizer {
	a := &rlsRowAuthorizer{db: db}
	a.snap.Store(map[string]database.RLSConfig{})
	a.reload(ctx)
	if refresh <= 0 {
		refresh = 30 * time.Second
	}
	go a.loop(ctx, refresh)
	return a
}

func (a *rlsRowAuthorizer) loop(ctx context.Context, refresh time.Duration) {
	t := time.NewTicker(refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.reload(ctx)
		}
	}
}

func (a *rlsRowAuthorizer) reload(ctx context.Context) {
	cfgs, err := a.db.ListRLSConfig(ctx)
	if err != nil {
		return // keep the previous snapshot on a transient error
	}
	m := make(map[string]database.RLSConfig, len(cfgs))
	for _, c := range cfgs {
		m[c.Table] = c
	}
	a.snap.Store(m)
}

// Authorize reports whether a subscriber may receive a change event.
func (a *rlsRowAuthorizer) Authorize(role, userID, schema, table string, row map[string]any) bool {
	// Service key has full access, like the REST/admin path.
	if role == realtime.RoleService {
		return true
	}

	m, _ := a.snap.Load().(map[string]database.RLSConfig)
	cfg, ok := m[table]
	if !ok {
		// Not RLS-managed: deliver as before (non-breaking default).
		return true
	}

	switch cfg.Mode {
	case database.RLSModePublic:
		return true
	case database.RLSModeAuthenticated:
		return userID != "" && role != realtime.RoleAnon
	case database.RLSModeOwner:
		if cfg.OwnerColumn == "" || userID == "" {
			return false
		}
		v, present := row[cfg.OwnerColumn]
		if !present {
			return false
		}
		return valueEqualsString(v, userID)
	}
	return true
}

// valueEqualsString compares a WAL-decoded column value to the JWT
// subject. uuid/text columns arrive as strings under pgoutput; the other
// cases are defensive.
func valueEqualsString(v any, s string) bool {
	switch t := v.(type) {
	case string:
		return t == s
	case []byte:
		return string(t) == s
	case fmt.Stringer:
		return t.String() == s
	default:
		return fmt.Sprintf("%v", t) == s
	}
}
