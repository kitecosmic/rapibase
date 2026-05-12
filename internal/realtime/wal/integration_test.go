//go:build integration

// Integration tests for the WAL pipeline against a real Postgres
// instance. Opt in with:
//
//	docker compose -f docker-compose.test.yaml up -d
//	go test -tags integration ./internal/realtime/wal/...
//	docker compose -f docker-compose.test.yaml down -v
//
// The default connection string targets the docker-compose.test.yaml
// container (port 5433). Override with RAPIBASE_TEST_DB if your
// Postgres lives elsewhere — wal_level must still be 'logical' and
// the user must have REPLICATION.

package wal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

const defaultTestConn = "postgres://rapibase_test:rapibase_test@localhost:5433/rapibase_test?sslmode=disable"

func dbURL() string {
	if v := os.Getenv("RAPIBASE_TEST_DB"); v != "" {
		return v
	}
	return defaultTestConn
}

// dialOrSkip connects to the test database, tolerating a brief
// ramp-up window when Postgres has just started in docker compose.
//
// Behaviour:
//   - "Connection refused" or similar fail-fast errors → skip
//     immediately (compose is not running).
//   - Slow first response (Postgres warming up) → retry up to 15s,
//     then skip.
//
// This way `docker compose up -d` followed by an immediate `go test`
// works without the operator having to remember `--wait`.
func dialOrSkip(t *testing.T) *pgx.Conn {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, err := pgx.Connect(ctx, dbURL())
		cancel()
		if err == nil {
			return conn
		}
		lastErr = err
		// "connection refused" means nothing is listening — no point
		// in retrying. Anything else (timeout, "the database system
		// is starting up") is worth waiting for.
		if strings.Contains(err.Error(), "connection refused") {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Skipf("postgres unavailable at %s: %v", dbURL(), lastErr)
	return nil
}

// bootstrapSlot creates the publication and slot for one test in
// isolation. Done with raw SQL to avoid an import cycle with the
// realtime parent package.
func bootstrapSlot(t *testing.T, conn *pgx.Conn, slot, pub string) {
	t.Helper()
	ctx := context.Background()
	// Best-effort cleanup of any leftovers from a previous failed run.
	dropSlotAndPub(t, conn, slot, pub)

	if _, err := conn.Exec(ctx, "CREATE PUBLICATION "+pub+" FOR ALL TABLES"); err != nil {
		t.Fatalf("create publication: %v", err)
	}
	if _, err := conn.Exec(ctx,
		"SELECT pg_create_logical_replication_slot($1, 'pgoutput')",
		slot,
	); err != nil {
		t.Fatalf("create slot: %v", err)
	}
}

// dropSlotAndPub tears down a slot+publication pair. Slot drops can
// fail transiently if a backend still holds it (the just-cancelled
// replicator) — retry briefly before giving up.
func dropSlotAndPub(t *testing.T, conn *pgx.Conn, slot, pub string) {
	t.Helper()
	ctx := context.Background()

	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slot)
		if err == nil {
			lastErr = nil
			break
		}
		// "slot does not exist" → OK, nothing to drop.
		if strings.Contains(err.Error(), "does not exist") {
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		t.Logf("warning: could not drop slot %s: %v", slot, lastErr)
	}
	if _, err := conn.Exec(ctx, "DROP PUBLICATION IF EXISTS "+pub); err != nil {
		t.Logf("warning: drop publication %s: %v", pub, err)
	}
}

// collectSink captures events produced by the replicator.
type collectSink struct {
	events chan Event
}

func (c *collectSink) OnEvent(_ context.Context, e Event) error {
	c.events <- e
	return nil
}

// runReplicator starts a replicator in a goroutine and returns the
// sink it feeds plus a stop function. The stop function cancels the
// replicator and waits for it to exit so subsequent slot drops do not
// race with an active consumer.
func runReplicator(t *testing.T, slot, pub string) (*collectSink, func()) {
	t.Helper()
	sink := &collectSink{events: make(chan Event, 256)}
	rep := NewReplicator(Config{
		ConnString:      dbURL(),
		SlotName:        slot,
		PublicationName: pub,
		StatusInterval:  1 * time.Second,
	}, sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rep.Run(ctx) }()

	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Logf("replicator exit: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Logf("warning: replicator did not exit within 3s")
		}
	}
	return sink, stop
}

// waitEvent reads from the sink with a generous timeout. The first
// event after start_replication can take ~500ms while the slot
// flushes initial state, so we use a 5s default.
func waitEvent(t *testing.T, sink *collectSink, timeout time.Duration) Event {
	t.Helper()
	select {
	case ev := <-sink.events:
		return ev
	case <-time.After(timeout):
		t.Fatal("timeout waiting for WAL event")
		return Event{}
	}
}

// waitFor finds the first event matching predicate or fails. Useful
// when the slot may emit extra setup events ahead of the one we
// care about.
func waitFor(t *testing.T, sink *collectSink, timeout time.Duration, match func(Event) bool) Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case ev := <-sink.events:
			if match(ev) {
				return ev
			}
		case <-time.After(time.Until(deadline)):
			t.Fatal("timeout waiting for matching event")
			return Event{}
		}
	}
	t.Fatal("timeout waiting for matching event")
	return Event{}
}

// uniqueName returns a name suffixed with the current nanosecond so
// parallel runs (or stale state) cannot collide.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

// ---- tests ---------------------------------------------------------

func TestIntegration_Insert(t *testing.T) {
	ctx := context.Background()
	conn := dialOrSkip(t)
	defer conn.Close(ctx)

	slot, pub := uniqueName("rt_slot_ins"), uniqueName("rt_pub_ins")
	table := uniqueName("rt_tbl_ins")

	bootstrapSlot(t, conn, slot, pub)
	defer dropSlotAndPub(t, conn, slot, pub)

	if _, err := conn.Exec(ctx, "CREATE TABLE "+table+" (id BIGSERIAL PRIMARY KEY, text TEXT, n INT)"); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "DROP TABLE IF EXISTS "+table)

	sink, stop := runReplicator(t, slot, pub)
	defer stop()

	// Let the replicator subscribe before generating WAL.
	time.Sleep(750 * time.Millisecond)

	if _, err := conn.Exec(ctx, "INSERT INTO "+table+" (text, n) VALUES ($1, $2)", "hola", 42); err != nil {
		t.Fatal(err)
	}

	ev := waitFor(t, sink, 5*time.Second, func(e Event) bool {
		return e.Type == protocol.EventInsert && e.Table == table
	})
	if ev.New == nil {
		t.Fatalf("INSERT should have New: %+v", ev)
	}
	if ev.New["text"] != "hola" {
		t.Fatalf("text mismatch: got %v", ev.New["text"])
	}
	if ev.New["n"] != int64(42) {
		t.Fatalf("n should be int64(42): got %v (%T)", ev.New["n"], ev.New["n"])
	}
	if ev.LSN == "" {
		t.Fatal("event should carry an LSN")
	}
}

func TestIntegration_Update(t *testing.T) {
	ctx := context.Background()
	conn := dialOrSkip(t)
	defer conn.Close(ctx)

	slot, pub := uniqueName("rt_slot_upd"), uniqueName("rt_pub_upd")
	table := uniqueName("rt_tbl_upd")

	bootstrapSlot(t, conn, slot, pub)
	defer dropSlotAndPub(t, conn, slot, pub)

	if _, err := conn.Exec(ctx, "CREATE TABLE "+table+" (id INT PRIMARY KEY, status TEXT)"); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "DROP TABLE IF EXISTS "+table)

	// Seed a row before the replicator starts so the UPDATE arrives clean.
	if _, err := conn.Exec(ctx, "INSERT INTO "+table+" (id, status) VALUES (1, 'pending')"); err != nil {
		t.Fatal(err)
	}

	sink, stop := runReplicator(t, slot, pub)
	defer stop()
	time.Sleep(750 * time.Millisecond)

	if _, err := conn.Exec(ctx, "UPDATE "+table+" SET status = 'done' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}

	ev := waitFor(t, sink, 5*time.Second, func(e Event) bool {
		return e.Type == protocol.EventUpdate && e.Table == table
	})
	if ev.New == nil || ev.New["status"] != "done" {
		t.Fatalf("new status wrong: %+v", ev.New)
	}
	if ev.New["id"] != int64(1) {
		t.Fatalf("id %v", ev.New["id"])
	}
}

func TestIntegration_Delete(t *testing.T) {
	ctx := context.Background()
	conn := dialOrSkip(t)
	defer conn.Close(ctx)

	slot, pub := uniqueName("rt_slot_del"), uniqueName("rt_pub_del")
	table := uniqueName("rt_tbl_del")

	bootstrapSlot(t, conn, slot, pub)
	defer dropSlotAndPub(t, conn, slot, pub)

	if _, err := conn.Exec(ctx, "CREATE TABLE "+table+" (id INT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "DROP TABLE IF EXISTS "+table)

	if _, err := conn.Exec(ctx, "INSERT INTO "+table+" VALUES (99)"); err != nil {
		t.Fatal(err)
	}

	sink, stop := runReplicator(t, slot, pub)
	defer stop()
	time.Sleep(750 * time.Millisecond)

	if _, err := conn.Exec(ctx, "DELETE FROM "+table+" WHERE id = 99"); err != nil {
		t.Fatal(err)
	}

	ev := waitFor(t, sink, 5*time.Second, func(e Event) bool {
		return e.Type == protocol.EventDelete && e.Table == table
	})
	// REPLICA IDENTITY DEFAULT carries the primary key in Old.
	if ev.Old == nil || ev.Old["id"] != int64(99) {
		t.Fatalf("old payload wrong: %+v", ev.Old)
	}
}

func TestIntegration_Truncate(t *testing.T) {
	ctx := context.Background()
	conn := dialOrSkip(t)
	defer conn.Close(ctx)

	slot, pub := uniqueName("rt_slot_trc"), uniqueName("rt_pub_trc")
	table := uniqueName("rt_tbl_trc")

	bootstrapSlot(t, conn, slot, pub)
	defer dropSlotAndPub(t, conn, slot, pub)

	if _, err := conn.Exec(ctx, "CREATE TABLE "+table+" (id INT)"); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "DROP TABLE IF EXISTS "+table)
	if _, err := conn.Exec(ctx, "INSERT INTO "+table+" VALUES (1), (2), (3)"); err != nil {
		t.Fatal(err)
	}

	sink, stop := runReplicator(t, slot, pub)
	defer stop()
	time.Sleep(750 * time.Millisecond)

	if _, err := conn.Exec(ctx, "TRUNCATE "+table); err != nil {
		t.Fatal(err)
	}

	ev := waitFor(t, sink, 5*time.Second, func(e Event) bool {
		return e.Type == protocol.EventTruncate && e.Table == table
	})
	if ev.LSN == "" {
		t.Fatal("truncate event missing LSN")
	}
}

// TestIntegration_TransactionGrouping verifies that every change inside
// a single transaction shares the same commit LSN — the property the
// hub's resume buffer relies on.
func TestIntegration_TransactionGrouping(t *testing.T) {
	ctx := context.Background()
	conn := dialOrSkip(t)
	defer conn.Close(ctx)

	slot, pub := uniqueName("rt_slot_txn"), uniqueName("rt_pub_txn")
	table := uniqueName("rt_tbl_txn")

	bootstrapSlot(t, conn, slot, pub)
	defer dropSlotAndPub(t, conn, slot, pub)

	if _, err := conn.Exec(ctx, "CREATE TABLE "+table+" (id INT PRIMARY KEY, n INT)"); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "DROP TABLE IF EXISTS "+table)

	sink, stop := runReplicator(t, slot, pub)
	defer stop()
	time.Sleep(750 * time.Millisecond)

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := tx.Exec(ctx, "INSERT INTO "+table+" (id, n) VALUES ($1, $2)", i, i*10); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Collect three events; they must share an LSN.
	got := make([]Event, 0, 3)
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < 3 && time.Now().Before(deadline) {
		select {
		case ev := <-sink.events:
			if ev.Type == protocol.EventInsert && ev.Table == table {
				got = append(got, ev)
			}
		case <-time.After(time.Until(deadline)):
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 inserts, got %d", len(got))
	}
	if got[0].LSN != got[1].LSN || got[1].LSN != got[2].LSN {
		t.Fatalf("inserts in same txn should share LSN: %v %v %v",
			got[0].LSN, got[1].LSN, got[2].LSN)
	}
}
