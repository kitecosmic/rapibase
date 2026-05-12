package hub

import (
	"sync/atomic"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// Subscriber represents one client connection's view inside the hub.
// It is created by the transport on connection upgrade and handed to
// Attach calls; the hub never creates Subscribers itself.
//
// The struct is intentionally opaque: external packages cannot poke at
// the outbound queue directly. To send a frame to a subscriber the hub
// calls (*Subscriber).enqueue, which the transport drains.
type Subscriber struct {
	id       string
	role     string
	userID   string
	origin   protocol.FrameOrigin
	outbound chan protocol.Frame

	// State updated atomically so the transport can read without
	// taking a hub lock.
	closed atomic.Bool
}

// NewSubscriber constructs a Subscriber tied to an outbound channel
// whose buffer size is set by the hub. The transport reads from
// Outbound() in its write loop.
func NewSubscriber(id, role, userID string, queueSize int) *Subscriber {
	return &Subscriber{
		id:       id,
		role:     role,
		userID:   userID,
		origin:   protocol.FrameOrigin{SessionID: id, UserID: userID},
		outbound: make(chan protocol.Frame, queueSize),
	}
}

// ID returns the subscriber's stable session identifier.
func (s *Subscriber) ID() string { return s.id }

// Role returns the auth role used to filter events for this subscriber.
func (s *Subscriber) Role() string { return s.role }

// UserID returns the JWT user id, or empty for anonymous connections.
func (s *Subscriber) UserID() string { return s.userID }

// Origin returns the FrameOrigin attached to broadcast frames from this
// subscriber.
func (s *Subscriber) Origin() protocol.FrameOrigin { return s.origin }

// Outbound exposes the read end of the queue for the transport's write
// loop. The hub remains the only writer.
func (s *Subscriber) Outbound() <-chan protocol.Frame { return s.outbound }

// Close marks the subscriber as closed and closes the outbound channel.
// Idempotent. Should be called by the transport when the connection
// drops; the hub will also call it on slow-consumer eviction.
func (s *Subscriber) Close() {
	if s.closed.CompareAndSwap(false, true) {
		close(s.outbound)
	}
}

// IsClosed reports whether the subscriber has been Close()d.
func (s *Subscriber) IsClosed() bool { return s.closed.Load() }

// enqueue is called by the hub on the fan-out path. Returns false if the
// queue is full (the hub will then evict the subscriber). The hub holds
// no locks while calling this so eviction does not deadlock.
func (s *Subscriber) enqueue(f protocol.Frame) bool {
	if s.closed.Load() {
		return false
	}
	select {
	case s.outbound <- f:
		return true
	default:
		return false
	}
}

// SetRole updates the auth role after a successful set_auth frame. The
// transport calls this synchronously while no fan-out is in flight for
// this subscriber, so no lock is needed.
func (s *Subscriber) SetRole(role, userID string) {
	s.role = role
	s.userID = userID
	s.origin.UserID = userID
}
