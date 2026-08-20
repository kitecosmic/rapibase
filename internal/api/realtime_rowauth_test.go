package api

import (
	"testing"

	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/realtime"
)

// newTestAuthorizer builds an authorizer over a fixed snapshot, skipping
// the database entirely — Authorize only ever reads the snapshot.
func newTestAuthorizer(cfgs ...database.RLSConfig) *rlsRowAuthorizer {
	a := &rlsRowAuthorizer{}
	m := make(map[string]database.RLSConfig, len(cfgs))
	for _, c := range cfgs {
		m[c.Table] = c
	}
	a.snap.Store(m)
	return a
}

// The contract under test: the anon key on its own never reads table
// data over realtime, exactly as middleware.RequireAPIKey enforces on
// REST. Modes the authorizer cannot evaluate fail closed.
func TestRowAuthorizer_Authorize(t *testing.T) {
	const (
		owner = "11111111-1111-1111-1111-111111111111"
		other = "22222222-2222-2222-2222-222222222222"
	)
	auth := newTestAuthorizer(
		database.RLSConfig{Table: "public_feed", Mode: database.RLSModePublic},
		database.RLSConfig{Table: "members", Mode: database.RLSModeAuthenticated},
		database.RLSConfig{Table: "notes", Mode: database.RLSModeOwner, OwnerColumn: "owner_id"},
		database.RLSConfig{Table: "ledger", Mode: database.RLSModeCustom},
		database.RLSConfig{Table: "weird", Mode: "something_new"},
	)

	tests := []struct {
		name  string
		role  string
		user  string
		table string
		row   map[string]any
		want  bool
	}{
		// The service key keeps full access on every mode, like REST.
		{"service key sees unconfigured table", realtime.RoleService, "", "unconfigured", nil, true},
		{"service key sees custom mode", realtime.RoleService, "", "ledger", nil, true},

		// A table nobody configured: authenticated yes, anonymous no.
		// This is the hole that used to let anyone holding the public
		// anon key stream every change in the database.
		{"anon denied on unconfigured table", realtime.RoleAnon, "", "unconfigured", nil, false},
		{"authenticated allowed on unconfigured table", realtime.RoleAuthenticated, owner, "unconfigured", nil, true},

		// Mode public is the deliberate opt-out for login-free feeds.
		{"anon allowed on public table", realtime.RoleAnon, "", "public_feed", nil, true},

		{"anon denied on authenticated table", realtime.RoleAnon, "", "members", nil, false},
		{"authenticated allowed on authenticated table", realtime.RoleAuthenticated, owner, "members", nil, true},

		// Owner mode scopes row by row.
		{"owner sees own row", realtime.RoleAuthenticated, owner, "notes",
			map[string]any{"owner_id": owner}, true},
		{"owner does not see another's row", realtime.RoleAuthenticated, other, "notes",
			map[string]any{"owner_id": owner}, false},
		{"owner column missing from the image", realtime.RoleAuthenticated, owner, "notes",
			map[string]any{"id": 1}, false},
		{"anon never sees an owner table", realtime.RoleAnon, "", "notes",
			map[string]any{"owner_id": owner}, false},

		// Custom policies are SQL this authorizer cannot run. Delivering
		// them would leak a table the admin deliberately secured.
		{"custom mode denied even when authenticated", realtime.RoleAuthenticated, owner, "ledger", nil, false},

		// An unrecognised mode must not be assumed permissive.
		{"unknown mode denied", realtime.RoleAuthenticated, owner, "weird", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := auth.Authorize(tc.role, tc.user, "public", tc.table, tc.row)
			if got != tc.want {
				t.Errorf("Authorize(role=%q, user=%q, table=%q) = %v, want %v",
					tc.role, tc.user, tc.table, got, tc.want)
			}
		})
	}
}

// A role string that is not "anon" but carries no user id must not pass
// as authenticated: both halves of the check matter.
func TestRowAuthorizer_RoleWithoutUserIDIsNotAuthenticated(t *testing.T) {
	auth := newTestAuthorizer()
	if auth.Authorize(realtime.RoleAuthenticated, "", "public", "unconfigured", nil) {
		t.Error("a subscriber with no user id must not be treated as authenticated")
	}
}
