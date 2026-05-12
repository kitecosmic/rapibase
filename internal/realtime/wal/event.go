// Package wal captures change data from a Postgres database using logical
// replication (pgoutput) and emits structured Events for the hub to
// fan-out.
//
// The package is independent from protocol and hub: it produces a typed
// Event stream and lets callers translate that into wire frames. This
// separation keeps the WAL pipeline reusable for non-WebSocket consumers
// (webhooks, push notifications, server-side hooks) which already exist
// in the rapibase codebase.
package wal

import (
	"encoding/json"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// Event is the decoded form of a single row change captured from the WAL.
// One transaction may produce many events; each carries the commit LSN
// and timestamp shared by every change in that transaction.
type Event struct {
	// LSN of the *commit* of the transaction that contains this change.
	// Subsequent events from the same transaction share the same LSN,
	// which makes resume semantics straightforward.
	LSN protocol.LSN

	// CommitTS is the wall-clock time the upstream Postgres assigned to
	// the commit.
	CommitTS time.Time

	// Type discriminates which of New/Old are populated.
	Type protocol.EventType

	// Schema and Table are unqualified names of the affected relation.
	Schema string
	Table  string

	// New is the post-image of the row for INSERT / UPDATE, nil otherwise.
	New Row

	// Old is the pre-image of the row for UPDATE / DELETE (only the
	// columns covered by REPLICA IDENTITY), nil otherwise.
	Old Row
}

// Row is the decoded post- or pre-image of a row, keyed by column name.
// Values use Go primitives that map naturally to JSON: string, float64,
// bool, nil, time.Time, []byte for bytea, json.RawMessage for jsonb.
//
// Implements filter.Row.
type Row map[string]any

// Get returns the column value and whether it was present in the captured
// image. A nil value with present=true represents SQL NULL.
func (r Row) Get(column string) (any, bool) {
	v, ok := r[column]
	return v, ok
}

// MarshalJSON serializes the row as an object so it can be embedded
// verbatim into a postgres_changes frame.
func (r Row) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return json.Marshal(map[string]any(r))
}
