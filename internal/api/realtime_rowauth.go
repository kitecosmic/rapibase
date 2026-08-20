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
// The rule mirrors the REST guard in middleware.RequireAPIKey: the anon
// key alone never reads table data — it needs a user JWT. A table with
// no RLS config therefore reaches authenticated subscribers (exactly
// what REST does once RLS is off) but never anonymous ones. Tables in
// mode "public" are the deliberate opt-out: they stream to anonymous
// subscribers too, which is what makes public live feeds (order
// tracking, scoreboards) work without a login.
//
// Anything this authorizer cannot evaluate fails closed — notably mode
// "custom", whose policies are arbitrary SQL living in Postgres, not in
// this snapshot. Implements hub.RowAuthorizer.
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
	authed := userID != "" && role != realtime.RoleAnon

	m, _ := a.snap.Load().(map[string]database.RLSConfig)
	cfg, ok := m[table]
	if !ok {
		// Not RLS-managed. REST would still demand a JWT alongside the
		// anon key, so realtime demands one too: authenticated
		// subscribers get the event, anonymous ones do not.
		return authed
	}

	switch cfg.Mode {
	case database.RLSModePublic:
		return true
	case database.RLSModeAuthenticated:
		return authed
	case database.RLSModeOwner:
		if cfg.OwnerColumn == "" || userID == "" {
			return false
		}
		v, present := row[cfg.OwnerColumn]
		if !present {
			// DELETE carries only the replica identity. SetTableRLS puts
			// owner tables on REPLICA IDENTITY FULL so the column is
			// there; a table configured before that change is the one
			// case that lands here, and dropping the event is the safe
			// half of the trade.
			return false
		}
		return valueEqualsString(v, userID)
	case database.RLSModeCustom:
		// Custom policies are SQL evaluated by Postgres on the REST path.
		// This authorizer cannot run them, and guessing would leak, so
		// the event is dropped for every non-service subscriber.
		return false
	}
	// Unknown mode: fail closed rather than assume it is permissive.
	return false
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
