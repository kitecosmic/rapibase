package transport

import (
	"math"
	"sync"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// RateLimits configures the per-connection token buckets that gate
// inbound frame types. Zero on any field disables the limit for that
// frame type — useful for trusted internal callers.
//
// Capacity is the burst allowance (how many frames can arrive
// instantly); RatePerSec is the steady-state refill rate. The bucket
// uses simple token-bucket semantics: every frame consumes one token,
// rejected frames respond with ErrRateLimited carrying RetryMs.
type RateLimits struct {
	// BroadcastBurst and BroadcastPerSec gate the FrameBroadcastIn
	// frames a single connection may send. Defaults (when both are
	// zero in HandlerDeps): 200 burst, 100/s sustained.
	BroadcastBurst  int
	BroadcastPerSec int

	// SubscribeBurst and SubscribePerSec gate subscribe / unsubscribe
	// frames. Defaults: 30 burst, 0.5/s (= 30 per minute).
	SubscribeBurst  int
	SubscribePerSec float64

	// PresenceBurst and PresencePerSec gate presence_track frames.
	// Defaults: 20 burst, 10/s.
	PresenceBurst  int
	PresencePerSec int

	// RPCDefaultBurst and RPCDefaultPerSec gate RPC frames when the
	// invoked function's Definition.RatePerSec is zero. Defaults:
	// 120 burst, 60/s.
	RPCDefaultBurst  int
	RPCDefaultPerSec int

	// RPCRateFor returns the per-second rate override for a named RPC
	// function, or 0 to fall through to RPCDefaultPerSec. Wired by
	// the realtime root over rpc.Registry.Lookup at construction so
	// transport does not depend on the rpc package.
	RPCRateFor func(function string) int
}

// withDefaults returns a copy of r with sensible defaults applied for
// any zero field. The defaults are conservative: they let a normal
// chat-like app run unimpeded while making a misbehaving client
// expensive enough to detect.
func (r RateLimits) withDefaults() RateLimits {
	if r.BroadcastBurst == 0 {
		r.BroadcastBurst = 200
	}
	if r.BroadcastPerSec == 0 {
		r.BroadcastPerSec = 100
	}
	if r.SubscribeBurst == 0 {
		r.SubscribeBurst = 30
	}
	if r.SubscribePerSec == 0 {
		r.SubscribePerSec = 0.5
	}
	if r.PresenceBurst == 0 {
		r.PresenceBurst = 20
	}
	if r.PresencePerSec == 0 {
		r.PresencePerSec = 10
	}
	if r.RPCDefaultBurst == 0 {
		r.RPCDefaultBurst = 120
	}
	if r.RPCDefaultPerSec == 0 {
		r.RPCDefaultPerSec = 60
	}
	return r
}

// tokenBucket is a thread-safe token bucket. Construction takes
// floating-point rates because subscribe limits live below 1/s.
type tokenBucket struct {
	capacity float64
	rate     float64 // tokens per second

	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

func newTokenBucket(capacity int, ratePerSec float64) *tokenBucket {
	if capacity < 0 {
		capacity = 0
	}
	if ratePerSec < 0 {
		ratePerSec = 0
	}
	return &tokenBucket{
		capacity:   float64(capacity),
		rate:       ratePerSec,
		tokens:     float64(capacity),
		lastRefill: time.Now(),
	}
}

// tryConsume attempts to take one token. Returns (true, 0) on
// success, or (false, retryAfter) when the bucket is empty.
func (b *tokenBucket) tryConsume() (bool, time.Duration) {
	if b == nil || b.capacity <= 0 {
		return true, 0 // disabled
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if b.rate > 0 {
		elapsed := now.Sub(b.lastRefill).Seconds()
		b.tokens = math.Min(b.capacity, b.tokens+elapsed*b.rate)
	}
	b.lastRefill = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	if b.rate <= 0 {
		// Hard capacity, no refill — once exhausted, never recovers
		// until process restart. RetryMs is "long enough to give up".
		return false, time.Hour
	}
	deficit := 1 - b.tokens
	retry := time.Duration(deficit / b.rate * float64(time.Second))
	return false, retry
}

// rateLimitFor decides which bucket and capacity/rate the supplied
// inbound frame must consume from. Returns ("", 0, 0) if the frame
// type is not rate-limited (acks, heartbeats, etc.).
func rateLimitFor(in protocol.Frame, limits RateLimits) (key string, capacity int, rate float64) {
	switch in.Type {
	case protocol.FrameBroadcastIn:
		return "broadcast", limits.BroadcastBurst, float64(limits.BroadcastPerSec)
	case protocol.FrameSubscribe, protocol.FrameUnsubscribe:
		return "subscribe", limits.SubscribeBurst, limits.SubscribePerSec
	case protocol.FramePresenceTrack, protocol.FramePresenceUntrack:
		return "presence", limits.PresenceBurst, float64(limits.PresencePerSec)
	case protocol.FrameRPC:
		// Per-function override when the registry declared one.
		rate := float64(limits.RPCDefaultPerSec)
		if limits.RPCRateFor != nil {
			if r := limits.RPCRateFor(in.Function); r > 0 {
				rate = float64(r)
			}
		}
		return "rpc:" + in.Function, limits.RPCDefaultBurst, rate
	}
	return "", 0, 0
}
