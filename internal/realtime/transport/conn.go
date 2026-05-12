package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// rawConn abstracts the WebSocket primitives transport relies on.
// Implementations wrap whatever WS library is in use (gofiber/websocket
// today). Keeping the interface narrow makes it trivial to substitute
// an in-memory implementation in tests.
type rawConn interface {
	// ReadMessage blocks until the next inbound frame arrives, or
	// until the read deadline (if any) is exceeded.
	ReadMessage() (data []byte, err error)

	// WriteMessage writes an outbound frame. Implementations must
	// serialize concurrent calls or document that the caller
	// synchronises externally (this package synchronises externally
	// via the single writeLoop goroutine).
	WriteMessage(data []byte) error

	// SetReadDeadline applies a deadline to the next (and subsequent)
	// ReadMessage call. A zero time disables the deadline.
	SetReadDeadline(t time.Time) error

	// Close terminates the underlying connection with a WebSocket
	// close code and human-readable reason. Implementations should be
	// idempotent.
	Close(code int, reason string) error
}

// Router is the part of the realtime root that dispatches inbound
// frames to the appropriate subsystem (hub, presence, rpc). transport
// holds an interface so this package does not depend on the root.
type Router interface {
	// Dispatch processes a single inbound frame. Returning a non-empty
	// reply frame (Type != "") instructs the transport to send it back
	// to the client; returning an empty frame means "no response".
	// Errors are surfaced to the client as protocol error frames.
	Dispatch(ctx context.Context, sess *Session, in protocol.Frame) (out protocol.Frame, err error)
}

// Conn pairs a Session with its rawConn and orchestrates the read /
// write loops. One Conn per WebSocket; constructed by the handler
// after a successful handshake.
type Conn struct {
	raw    rawConn
	sess   *Session
	router Router

	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	maxPayload        int
	rateLimits        RateLimits

	// replies is the channel readLoop uses to enqueue ack/error frames
	// for writeLoop. Keeping all writes on a single goroutine removes
	// the need to serialise WriteMessage manually.
	replies chan protocol.Frame

	closed atomic.Bool
}

func newConn(raw rawConn, sess *Session, router Router, opts ConnOptions) *Conn {
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 25 * time.Second
	}
	if opts.HeartbeatTimeoutFactor < 2 {
		opts.HeartbeatTimeoutFactor = 2
	}
	if opts.MaxPayloadBytes <= 0 {
		opts.MaxPayloadBytes = 1 << 20 // 1 MiB
	}
	return &Conn{
		raw:               raw,
		sess:              sess,
		router:            router,
		heartbeatInterval: opts.HeartbeatInterval,
		heartbeatTimeout:  opts.HeartbeatInterval * time.Duration(opts.HeartbeatTimeoutFactor),
		maxPayload:        opts.MaxPayloadBytes,
		rateLimits:        opts.RateLimits.withDefaults(),
		replies:           make(chan protocol.Frame, 16),
	}
}

// ConnOptions parameterises a Conn at construction.
type ConnOptions struct {
	HeartbeatInterval      time.Duration
	HeartbeatTimeoutFactor int
	MaxPayloadBytes        int
	ServerVersion          string
	StartingLSN            protocol.LSN
	RateLimits             RateLimits
}

// Serve runs the read and write loops until either ends or ctx is
// canceled. Returns the first non-nil error observed in any loop. On
// return, the underlying connection has been closed.
func (c *Conn) Serve(ctx context.Context, opts ConnOptions) error {
	// Send welcome first. A failure here means we cannot communicate
	// at all, so close hard.
	welcome := protocol.Frame{
		Type:                protocol.FrameWelcome,
		SessionID:           c.sess.ID(),
		ServerVersion:       opts.ServerVersion,
		HeartbeatIntervalMs: int(c.heartbeatInterval.Milliseconds()),
		MaxPayloadBytes:     c.maxPayload,
		LSN:                 opts.StartingLSN,
	}
	if err := c.writeFrame(welcome); err != nil {
		_ = c.raw.Close(protocol.CloseInternalError, "welcome failed")
		return fmt.Errorf("transport: write welcome: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer c.sess.markClosed()

	// First-error capture across the read/write goroutines.
	// sync.Once guarantees exactly one writer wins and never panics
	// — atomic.Value.CompareAndSwap rejects nil-as-old when the
	// value has never been stored, which previously crashed the
	// process on every clean disconnect.
	var (
		firstErr   error
		firstErrMu sync.Mutex
		firstErrOK sync.Once
	)
	setErr := func(err error) {
		if err == nil {
			return
		}
		firstErrOK.Do(func() {
			firstErrMu.Lock()
			firstErr = err
			firstErrMu.Unlock()
		})
	}
	readFirstErr := func() error {
		firstErrMu.Lock()
		defer firstErrMu.Unlock()
		return firstErr
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		setErr(c.readLoop(ctx))
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		setErr(c.writeLoop(ctx))
	}()

	wg.Wait()

	e := readFirstErr()
	c.closeRaw(e)
	if e != nil {
		return e
	}
	return ctx.Err()
}

func (c *Conn) closeRaw(firstErr error) {
	if c.closed.Swap(true) {
		return
	}
	code := 1000
	reason := ""
	if err := firstErr; err != nil {
		switch {
		case errors.Is(err, errFrameTooLarge):
			code = protocol.ClosePayloadTooLarge
			reason = "payload too large"
		case errors.Is(err, errReadTimeout):
			code = protocol.CloseHeartbeatTimeout
			reason = "heartbeat timeout"
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// normal shutdown
		default:
			code = protocol.CloseInternalError
			reason = err.Error()
		}
	}
	_ = c.raw.Close(code, reason)
}

// readLoop pulls frames off the wire, decodes them, and dispatches
// each through the Router. Ack / error frames returned by Dispatch are
// shipped to writeLoop via the replies channel.
func (c *Conn) readLoop(ctx context.Context) error {
	codec := c.sess.Codec()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := c.raw.SetReadDeadline(time.Now().Add(c.heartbeatTimeout)); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}
		data, err := c.raw.ReadMessage()
		if err != nil {
			if isTimeout(err) {
				return errReadTimeout
			}
			return err
		}
		if len(data) > c.maxPayload {
			return errFrameTooLarge
		}

		in, err := codec.Decode(data)
		if err != nil {
			c.enqueueReply(ctx, errorFrame("", protocol.ErrInvalidPayload, err.Error()))
			continue
		}

		// Heartbeats from the client get a fast-path ack — no router
		// involvement, and the read deadline above already records
		// liveness.
		if in.Type == protocol.FrameHeartbeatIn {
			c.enqueueReply(ctx, protocol.Frame{Type: protocol.FrameAck, Ref: in.Ref})
			continue
		}

		// Per-connection rate limit. Rejected frames respond with
		// ErrRateLimited and never reach the router, so a misbehaving
		// client cannot push load past the bucket capacity.
		if key, cap, rate := rateLimitFor(in, c.rateLimits); key != "" {
			if ok, retry := c.sess.Allow(key, cap, rate); !ok {
				c.enqueueReply(ctx, protocol.Frame{
					Type:      protocol.FrameError,
					Ref:       in.Ref,
					Code:      string(protocol.ErrRateLimited),
					Message:   "rate limit exceeded",
					Retryable: true,
					RetryMs:   int(retry / time.Millisecond),
				})
				continue
			}
		}

		out, derr := c.router.Dispatch(ctx, c.sess, in)
		if derr != nil {
			c.enqueueReply(ctx, errorFrameFromError(in.Ref, derr))
			continue
		}
		if out.Type != "" {
			c.enqueueReply(ctx, out)
		}
	}
}

// writeLoop drains the subscriber's outbound queue, the local replies
// channel, and the heartbeat ticker. Single goroutine == no need to
// serialise WriteMessage externally.
func (c *Conn) writeLoop(ctx context.Context) error {
	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()

	outbound := c.sess.Subscriber().Outbound()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case f, ok := <-outbound:
			if !ok {
				return errSubscriberClosed
			}
			if err := c.writeFrame(f); err != nil {
				return err
			}

		case f := <-c.replies:
			if err := c.writeFrame(f); err != nil {
				return err
			}

		case <-ticker.C:
			if err := c.writeFrame(protocol.Frame{Type: protocol.FrameHeartbeatOut}); err != nil {
				return err
			}
		}
	}
}

// writeFrame encodes and writes a single frame. Called only from
// writeLoop (and once from Serve for the welcome), so no locking
// needed.
func (c *Conn) writeFrame(f protocol.Frame) error {
	bs, err := c.sess.Codec().Encode(f)
	if err != nil {
		return fmt.Errorf("encode %s: %w", f.Type, err)
	}
	return c.raw.WriteMessage(bs)
}

// enqueueReply blocks until the reply is accepted by writeLoop or the
// context is canceled, whichever happens first.
func (c *Conn) enqueueReply(ctx context.Context, f protocol.Frame) {
	select {
	case c.replies <- f:
	case <-ctx.Done():
	}
}

// errorFrame builds a protocol error frame for a refused inbound.
func errorFrame(ref string, code protocol.ErrorCode, message string) protocol.Frame {
	return protocol.Frame{
		Type:    protocol.FrameError,
		Ref:     ref,
		Code:    string(code),
		Message: message,
	}
}

// errorFrameFromError tries to map an internal error to a protocol
// error code. Unknown errors fall through to ErrInternal.
func errorFrameFromError(ref string, err error) protocol.Frame {
	if pe, ok := err.(*protocol.Error); ok && pe != nil {
		return protocol.Frame{
			Type:      protocol.FrameError,
			Ref:       ref,
			Code:      string(pe.Code),
			Message:   pe.Message,
			Retryable: pe.Retryable,
			RetryMs:   pe.RetryMs,
		}
	}
	return errorFrame(ref, protocol.ErrInternal, err.Error())
}

// isTimeout returns true if the error looks like a read timeout from
// the underlying connection. We try the standard `Timeout() bool`
// interface so the check works across net.Error implementations.
func isTimeout(err error) bool {
	type timeouter interface{ Timeout() bool }
	var t timeouter
	if errors.As(err, &t) {
		return t.Timeout()
	}
	return false
}

var (
	errFrameTooLarge    = errors.New("transport: frame too large")
	errReadTimeout      = errors.New("transport: heartbeat read timeout")
	errSubscriberClosed = errors.New("transport: subscriber outbound closed")
)
