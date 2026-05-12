// Package bus is the abstraction that lets rapibase scale horizontally
// without changing the hub.
//
// In a single-node deployment the local implementation is used: events
// produced by the WAL replicator are delivered directly to the local
// hub through Go channels. In a multi-node deployment a NATS-backed
// implementation forwards events from the leader node (the one holding
// the replication slot) to every other node, so each node fans-out only
// to its locally-connected subscribers.
//
// Bus carries decoded WAL events. Broadcast and presence messages are
// strictly per-connection state held by the hub and do not flow through
// the bus.
package bus

import (
	"context"
	"errors"

	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// Bus delivers WAL events between rapibase processes.
type Bus interface {
	// Publish enqueues an event for delivery to every subscriber.
	// Implementations should be non-blocking on the producer side and
	// drop or back-pressure on the subscriber side, never the other way
	// around.
	Publish(ctx context.Context, ev wal.Event) error

	// Subscribe returns a channel that receives every event published
	// after the call (no replay). The returned cancel function detaches
	// the subscriber and closes its channel.
	Subscribe(ctx context.Context) (<-chan wal.Event, func(), error)

	// Close releases bus resources. Already-running Run loops on this
	// bus should observe Close and exit.
	Close() error
}

// ErrClosed is returned by bus operations after Close has been called.
var ErrClosed = errors.New("bus: closed")
