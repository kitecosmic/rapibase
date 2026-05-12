package transport

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	fiberws "github.com/gofiber/websocket/v2"
	"github.com/google/uuid"

	"github.com/rapibase/rapibase/internal/realtime/hub"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// HandlerDeps are the runtime collaborators a Handler needs. Wired by
// the realtime root package at startup. Defaults are applied on
// NewHandler so callers do not have to fill every field.
type HandlerDeps struct {
	Hub             *hub.Hub
	Router          Router
	Auth            AuthValidator
	HeartbeatPolicy ConnOptions

	// MaxPayloadBytes is the upper bound on a single inbound frame.
	MaxPayloadBytes int

	// SubscriberQueueSize is the bounded outbound queue size used
	// when constructing each Subscriber. Should match hub.Config.
	SubscriberQueueSize int

	// ServerVersion is reported in the welcome frame.
	ServerVersion string

	// RateLimits gates inbound frames per-connection. Zero fields get
	// sensible defaults; the realtime root injects RPCRateFor so the
	// transport can ask the rpc.Registry for per-function overrides
	// without depending on that package.
	RateLimits RateLimits
}

// Handler is the entry point of the transport layer. It is constructed
// once at startup and exposes:
//   - FiberHandlers() that returns the chain to mount on the Fiber
//     route, which performs auth, upgrades to a WebSocket, and starts
//     the per-connection Serve loop.
//   - Handle(raw, role, userID, codec) for tests and for callers that
//     want to bring their own WebSocket transport.
type Handler struct {
	deps HandlerDeps
}

// NewHandler builds a Handler with sensible defaults.
func NewHandler(deps HandlerDeps) *Handler {
	if deps.MaxPayloadBytes == 0 {
		deps.MaxPayloadBytes = 1 << 20
	}
	if deps.SubscriberQueueSize == 0 {
		deps.SubscriberQueueSize = 1024
	}
	if deps.HeartbeatPolicy.HeartbeatInterval == 0 {
		deps.HeartbeatPolicy.HeartbeatInterval = 25 * time.Second
	}
	if deps.HeartbeatPolicy.HeartbeatTimeoutFactor == 0 {
		deps.HeartbeatPolicy.HeartbeatTimeoutFactor = 2
	}
	if deps.HeartbeatPolicy.MaxPayloadBytes == 0 {
		deps.HeartbeatPolicy.MaxPayloadBytes = deps.MaxPayloadBytes
	}
	if deps.ServerVersion == "" {
		deps.ServerVersion = "1.0.0"
	}
	return &Handler{deps: deps}
}

// Handle is the post-upgrade entry point. It constructs the Subscriber,
// Session and Conn, runs Serve until the connection ends, and finally
// detaches the subscriber from every channel it touched.
//
// Callers wiring up a custom transport (or unit tests) use this method
// directly; the Fiber-side adapter calls it after upgrading the
// connection.
func (h *Handler) Handle(ctx context.Context, raw rawConn, apiKey, role, userID string, codec protocol.Codec) error {
	if raw == nil {
		return errors.New("transport: nil rawConn")
	}
	if codec == nil {
		return errors.New("transport: nil codec")
	}

	sessionID := uuid.NewString()
	sub := hub.NewSubscriber(sessionID, role, userID, h.deps.SubscriberQueueSize)
	defer sub.Close()
	defer h.deps.Hub.DetachAll(sub)

	sess := NewSession(sessionID, codec, sub, apiKey, role, userID)

	opts := h.deps.HeartbeatPolicy
	opts.ServerVersion = h.deps.ServerVersion
	opts.RateLimits = h.deps.RateLimits
	conn := newConn(raw, sess, h.deps.Router, opts)
	return conn.Serve(ctx, opts)
}

// FiberHandlers returns the slice of fiber.Handler values to mount on
// the realtime route. The first handler validates auth and stashes
// negotiated context; the second performs the WebSocket upgrade and
// runs Handle.
//
// Subprotocols MUST be declared on the websocket config: without
// them, fiberws does not echo back the Sec-WebSocket-Protocol header
// in the upgrade response, and well-behaved clients (including the
// ws/wscat reference implementation) refuse the connection with
// "Server sent no subprotocol".
//
// Example wiring in routes.go:
//
//	app.Get("/api/realtime/v1", h.FiberHandlers()...)
func (h *Handler) FiberHandlers() []fiber.Handler {
	return []fiber.Handler{
		h.authMiddleware,
		fiberws.New(h.serveWS, fiberws.Config{
			Subprotocols: []string{
				protocol.SubprotocolMsgpack,
				protocol.SubprotocolJSON,
			},
		}),
	}
}

// authMiddleware validates apikey / token from the query string and
// negotiates the codec from the WebSocket subprotocol offered in the
// upgrade request. It also requires fiberws.IsWebSocketUpgrade.
func (h *Handler) authMiddleware(c *fiber.Ctx) error {
	if !fiberws.IsWebSocketUpgrade(c) {
		return fiber.ErrUpgradeRequired
	}
	apiKey := c.Query("apikey")
	if apiKey == "" {
		// Also allow Authorization: Bearer for parity with the rest
		// of the API. The dashboard always uses query because browser
		// WebSocket clients cannot set headers, but server-side
		// clients are happier with headers.
		apiKey = bearerToken(c.Get("Authorization"))
	}
	if apiKey == "" {
		return fiber.NewError(fiber.StatusUnauthorized, "missing api key")
	}
	token := c.Query("token")

	role, userID, err := h.deps.Auth.Validate(apiKey, token)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}

	codec := negotiateSubprotocol(c.Get("Sec-WebSocket-Protocol"))
	if codec == nil {
		return fiber.NewError(fiber.StatusBadRequest, "no supported subprotocol")
	}

	c.Locals(localAPIKeyKey, apiKey)
	c.Locals(localRoleKey, role)
	c.Locals(localUserIDKey, userID)
	c.Locals(localCodecKey, codec)
	return c.Next()
}

// serveWS runs on the per-connection goroutine spawned by
// fiberws.New. The locals stashed by authMiddleware are read here;
// they cannot escape this connection.
func (h *Handler) serveWS(c *fiberws.Conn) {
	apiKey, _ := c.Locals(localAPIKeyKey).(string)
	role, _ := c.Locals(localRoleKey).(string)
	userID, _ := c.Locals(localUserIDKey).(string)
	codec, _ := c.Locals(localCodecKey).(protocol.Codec)

	raw := &fiberRawConn{c: c, msgType: fiberws.BinaryMessage}
	if codec != nil && codec.Subprotocol() == protocol.SubprotocolJSON {
		raw.msgType = fiberws.TextMessage
	}
	_ = h.Handle(context.Background(), raw, apiKey, role, userID, codec)
}

// fiberRawConn adapts a *fiberws.Conn to the rawConn interface.
type fiberRawConn struct {
	c       *fiberws.Conn
	msgType int
}

func (f *fiberRawConn) ReadMessage() ([]byte, error) {
	_, data, err := f.c.ReadMessage()
	return data, err
}

func (f *fiberRawConn) WriteMessage(data []byte) error {
	return f.c.WriteMessage(f.msgType, data)
}

func (f *fiberRawConn) SetReadDeadline(t time.Time) error {
	return f.c.SetReadDeadline(t)
}

func (f *fiberRawConn) Close(code int, reason string) error {
	// fiberws.Conn.Close does not take a code; use WriteControl to
	// send a close frame first, then Close the connection.
	_ = f.c.WriteControl(
		fiberws.CloseMessage,
		fiberws.FormatCloseMessage(code, reason),
		time.Now().Add(time.Second),
	)
	return f.c.Close()
}

// bearerToken extracts the bearer credential from an Authorization
// header. Returns "" when the header is missing or malformed.
func bearerToken(h string) string {
	const prefix = "Bearer "
	if len(h) <= len(prefix) {
		return ""
	}
	if h[:len(prefix)] != prefix {
		return ""
	}
	return h[len(prefix):]
}

// Keys used by authMiddleware to pass negotiated state to serveWS via
// fiber.Ctx.Locals. Prefixed to avoid collision with operator-set
// locals on the same context.
const (
	localAPIKeyKey = "rapibase.realtime.apiKey"
	localRoleKey   = "rapibase.realtime.role"
	localUserIDKey = "rapibase.realtime.userID"
	localCodecKey  = "rapibase.realtime.codec"
)
