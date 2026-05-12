package wal

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// LeaderConfig parameterises leader election for the replicator. In
// multi-node deployments only one rapibase instance may hold the
// replication slot; election uses a Postgres advisory lock so no
// additional coordination service is required.
type LeaderConfig struct {
	// ConnString is a libpq-style connection string used to open the
	// election connection. It is a regular SQL connection (not a
	// replication one) because pg_try_advisory_lock is plain SQL.
	ConnString string

	// LockKey is the bigint argument to pg_try_advisory_lock. Using a
	// stable, project-derived value lets the operator run multiple
	// rapibase clusters against the same Postgres without collisions.
	LockKey int64

	// CheckInterval is how often non-leaders re-attempt the lock and
	// how often the leader confirms it still holds the lock. Defaults
	// to 5s when zero.
	CheckInterval time.Duration

	// ReconnectBackoff is how long to wait before reconnecting after
	// a dropped connection. Defaults to 2s when zero.
	ReconnectBackoff time.Duration
}

// Leader runs the advisory-lock loop and signals state transitions
// through a callback. The callback is invoked synchronously from the
// election goroutine, so implementations must return quickly (calling
// Replicator.Run from the callback is fine because Run blocks the
// caller's own goroutine).
type Leader struct {
	cfg      LeaderConfig
	onChange func(isLeader bool)

	state atomic.Bool
}

// NewLeader constructs a Leader. The callback is invoked on every
// state transition: true when this node becomes leader, false when it
// loses leadership (e.g. connection drop).
func NewLeader(cfg LeaderConfig, onChange func(isLeader bool)) *Leader {
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 5 * time.Second
	}
	if cfg.ReconnectBackoff == 0 {
		cfg.ReconnectBackoff = 2 * time.Second
	}
	return &Leader{cfg: cfg, onChange: onChange}
}

// Run blocks until ctx is canceled, holding the advisory lock when
// possible and emitting onChange transitions. A connection drop
// produces a transition to non-leader followed by reconnect and a new
// election attempt.
func (l *Leader) Run(ctx context.Context) error {
	if l.cfg.LockKey == 0 {
		return errors.New("wal: LeaderConfig.LockKey is required")
	}

	for {
		if err := ctx.Err(); err != nil {
			l.setLeader(false)
			return err
		}

		err := l.session(ctx)
		l.setLeader(false)

		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Non-context error → back off and reconnect.
		_ = err // logging is the caller's responsibility
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(l.cfg.ReconnectBackoff):
		}
	}
}

// session is one connect-and-poll cycle. It returns when the
// connection drops, the context is canceled, or the lock cannot be
// acquired anymore.
func (l *Leader) session(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, l.cfg.ConnString)
	if err != nil {
		return fmt.Errorf("wal: leader connect: %w", err)
	}
	defer conn.Close(context.Background())

	ticker := time.NewTicker(l.cfg.CheckInterval)
	defer ticker.Stop()

	// Try to acquire immediately so we don't waste the CheckInterval
	// on startup.
	if err := l.tryAcquire(ctx, conn); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := l.tryAcquire(ctx, conn); err != nil {
				return err
			}
		}
	}
}

func (l *Leader) tryAcquire(ctx context.Context, conn *pgx.Conn) error {
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", l.cfg.LockKey).Scan(&ok); err != nil {
		return fmt.Errorf("wal: try_advisory_lock: %w", err)
	}
	l.setLeader(ok)
	return nil
}

// setLeader records the new state and fires the callback only on
// transitions. Idempotent on no-op updates.
func (l *Leader) setLeader(now bool) {
	prev := l.state.Swap(now)
	if prev == now {
		return
	}
	if l.onChange != nil {
		l.onChange(now)
	}
}

// IsLeader reports the current leadership state. Useful for /healthz
// and metrics endpoints; reads are lock-free.
func (l *Leader) IsLeader() bool { return l.state.Load() }
