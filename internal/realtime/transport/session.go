// Package transport implements the WebSocket layer of the realtime
// service. It owns the read and write loops of every connected client,
// translates between wire bytes and Frame values via a Codec, and
// delegates state changes (subscribe / unsubscribe / broadcast / track
// / invoke) to the hub, presence and rpc packages.
//
// Nothing in this package knows about Postgres or replication — those
// concerns live in wal and hub. transport is purely the I/O boundary.
package transport

import (
	"context"
	"sync"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/hub"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// Session is the per-connection state owned by transport. It pairs a
// hub.Subscriber (visible to the hub) with the negotiated codec and
// authentication context.
type Session struct {
	id        string
	codec     protocol.Codec
	subscr    *hub.Subscriber
	createdAt time.Time

	// apiKey is the project-scoped key presented in the handshake.
	// Immutable for the lifetime of the connection; set_auth rotates
	// the JWT (role/userID), never the apiKey.
	apiKey string

	mu     sync.Mutex
	role   string
	userID string

	// Rate-limit buckets keyed by frame type (or "rpc:functionName"
	// for RPCs). Lazily created on first use so we don't allocate
	// 200 buckets per connection when only a handful of functions
	// are ever called.
	bucketMu sync.Mutex
	buckets  map[string]*tokenBucket

	closed chan struct{}
}

// NewSession builds a Session bound to a hub.Subscriber that the caller
// (handler.go) has already constructed with the right queue size.
func NewSession(id string, codec protocol.Codec, sub *hub.Subscriber, apiKey, role, userID string) *Session {
	return &Session{
		id:        id,
		codec:     codec,
		subscr:    sub,
		createdAt: time.Now().UTC(),
		apiKey:    apiKey,
		role:      role,
		userID:    userID,
		closed:    make(chan struct{}),
	}
}

// APIKey returns the project-scoped key tied to this connection.
// Routers use it when re-validating a rotated JWT via AuthValidator.
func (s *Session) APIKey() string { return s.apiKey }

// ID returns the session identifier sent in the welcome frame.
func (s *Session) ID() string { return s.id }

// Subscriber returns the hub-side subscriber handle.
func (s *Session) Subscriber() *hub.Subscriber { return s.subscr }

// Codec returns the negotiated codec.
func (s *Session) Codec() protocol.Codec { return s.codec }

// Role returns the current auth role for this session. Updated by
// set_auth frames; reads are safe from any goroutine.
func (s *Session) Role() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.role
}

// UserID returns the JWT subject (or "" for anonymous). Like Role,
// rotated atomically by SetAuth.
func (s *Session) UserID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userID
}

// SetAuth atomically updates the role and user id after a successful
// set_auth. The hub.Subscriber is also updated so subsequent fan-out
// uses the new permissions.
func (s *Session) SetAuth(role, userID string) {
	s.mu.Lock()
	s.role = role
	s.userID = userID
	s.mu.Unlock()
	s.subscr.SetRole(role, userID)
}

// Done returns a channel that closes when the session has been torn
// down. Useful for goroutines that want to cancel work bound to the
// connection.
func (s *Session) Done() <-chan struct{} { return s.closed }

// markClosed is invoked exactly once by Conn when the connection ends.
func (s *Session) markClosed() {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
}

// Allow consumes one token from the named per-session bucket,
// creating the bucket lazily with the supplied capacity and rate on
// first use. Returns (true, 0) when the frame is allowed; otherwise
// (false, retryAfter) so the caller can populate an ErrRateLimited
// response.
//
// Capacity ≤ 0 disables the limit (always allowed).
func (s *Session) Allow(key string, capacity int, ratePerSec float64) (bool, time.Duration) {
	if key == "" || capacity <= 0 {
		return true, 0
	}
	s.bucketMu.Lock()
	if s.buckets == nil {
		s.buckets = make(map[string]*tokenBucket)
	}
	b, ok := s.buckets[key]
	if !ok {
		b = newTokenBucket(capacity, ratePerSec)
		s.buckets[key] = b
	}
	s.bucketMu.Unlock()
	return b.tryConsume()
}

// withCtx returns a context that is canceled when the session closes.
// Used by RPC dispatch so a hung handler does not outlive the caller.
func (s *Session) withCtx(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-s.closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
