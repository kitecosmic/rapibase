package realtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// BootstrapConfig parameterises the realtime bootstrap step. Defaults
// match wal.Config so an operator can derive one from the other.
type BootstrapConfig struct {
	// ConnString is a regular SQL connection string (not a replication
	// one). The role used must have the REPLICATION attribute and
	// rights to CREATE PUBLICATION on the target tables — typically
	// the database owner.
	ConnString string

	// PublicationName is the publication created (if absent). Defaults
	// to "rapibase_realtime".
	PublicationName string

	// SlotName is the logical replication slot created (if absent).
	// Defaults to "rapibase".
	SlotName string

	// PluginName is the output plugin for the slot. Always "pgoutput"
	// for production use; exposed as a knob so future test fixtures
	// can use alternative decoders.
	PluginName string
}

// Bootstrap brings a fresh Postgres into the state rapibase realtime
// expects:
//
//  1. wal_level must be 'logical' (operator-set; non-recoverable
//     without a restart, so we fail fast with a helpful message).
//  2. A publication covering every table the BaaS exposes.
//  3. A persistent logical replication slot using pgoutput.
//
// Bootstrap is idempotent: running it twice against the same database
// is a no-op. Call it once per process at startup, before Service.Run.
//
// The function intentionally does not create the apikey, JWT secret
// or any other rapibase-managed table — those live in the regular
// migration pipeline (internal/database). It only manages the WAL
// plumbing that the rest of the system assumes is in place.
func Bootstrap(ctx context.Context, cfg BootstrapConfig) error {
	if cfg.ConnString == "" {
		return errors.New("realtime: Bootstrap.ConnString required")
	}
	if cfg.PublicationName == "" {
		cfg.PublicationName = "rapibase_realtime"
	}
	if cfg.SlotName == "" {
		cfg.SlotName = "rapibase"
	}
	if cfg.PluginName == "" {
		cfg.PluginName = "pgoutput"
	}

	pubIdent, err := quoteIdentifier(cfg.PublicationName)
	if err != nil {
		return fmt.Errorf("publication name: %w", err)
	}
	// Slot and plugin names are passed as $1 / $2, not interpolated,
	// so they do not need quoting. But the function still validates
	// them so a malformed name fails immediately rather than at the
	// first replication attempt.
	if err := validateIdentifier(cfg.SlotName); err != nil {
		return fmt.Errorf("slot name: %w", err)
	}

	conn, err := pgx.Connect(ctx, cfg.ConnString)
	if err != nil {
		return fmt.Errorf("realtime: bootstrap connect: %w", err)
	}
	defer conn.Close(context.Background())

	if err := requireLogicalWAL(ctx, conn); err != nil {
		return err
	}
	if err := ensurePublication(ctx, conn, cfg.PublicationName, pubIdent); err != nil {
		return err
	}
	if err := ensureSlot(ctx, conn, cfg.SlotName, cfg.PluginName); err != nil {
		return err
	}
	return nil
}

// requireLogicalWAL verifies the server is running with the right WAL
// level. Returns a helpful error explaining the operator action.
func requireLogicalWAL(ctx context.Context, conn *pgx.Conn) error {
	var level string
	if err := conn.QueryRow(ctx, "SHOW wal_level").Scan(&level); err != nil {
		return fmt.Errorf("read wal_level: %w", err)
	}
	if level != "logical" {
		return fmt.Errorf(
			"realtime: postgres wal_level is %q but must be 'logical'. "+
				"Set wal_level=logical in postgresql.conf (or via ALTER SYSTEM SET wal_level = 'logical') and restart Postgres.",
			level,
		)
	}
	return nil
}

func ensurePublication(ctx context.Context, conn *pgx.Conn, name, quoted string) error {
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_publication WHERE pubname = $1)", name).Scan(&exists); err != nil {
		return fmt.Errorf("check publication: %w", err)
	}
	if exists {
		return nil
	}
	sql := "CREATE PUBLICATION " + quoted + " FOR ALL TABLES"
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("create publication %s: %w", name, err)
	}
	return nil
}

func ensureSlot(ctx context.Context, conn *pgx.Conn, slot, plugin string) error {
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", slot).Scan(&exists); err != nil {
		return fmt.Errorf("check slot: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := conn.Exec(ctx, "SELECT pg_create_logical_replication_slot($1, $2)", slot, plugin); err != nil {
		return fmt.Errorf("create slot %s: %w", slot, err)
	}
	return nil
}

// validateIdentifier rejects anything that is not a safe Postgres
// identifier (letters, digits, underscore). Callers pass these as
// query parameters where this matters, but the function is also used
// to fail fast on misconfiguration.
func validateIdentifier(name string) error {
	if name == "" {
		return errors.New("identifier is empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("identifier exceeds 63 chars: %q", name)
	}
	for i, r := range name {
		safe := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_'
		if i == 0 && r >= '0' && r <= '9' {
			return fmt.Errorf("identifier starts with digit: %q", name)
		}
		if !safe {
			return fmt.Errorf("identifier contains invalid character %q: %q", r, name)
		}
	}
	return nil
}

// quoteIdentifier validates and quotes a name for direct interpolation
// into SQL. Used for `CREATE PUBLICATION <name>` where parameterised
// statements do not accept identifiers.
func quoteIdentifier(name string) (string, error) {
	if err := validateIdentifier(name); err != nil {
		return "", err
	}
	return `"` + name + `"`, nil
}
