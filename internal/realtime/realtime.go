package realtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/rapibase/rapibase/internal/realtime/bus"
	"github.com/rapibase/rapibase/internal/realtime/filter"
	"github.com/rapibase/rapibase/internal/realtime/hub"
	"github.com/rapibase/rapibase/internal/realtime/metrics"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/rpc"
	"github.com/rapibase/rapibase/internal/realtime/transport"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// Config aggregates everything needed to construct a Service.
type Config struct {
	// WAL describes the Postgres replication input.
	WAL wal.Config

	// Leader configures leader election for multi-node deployments.
	// LockKey 0 disables election (single-node mode).
	Leader wal.LeaderConfig

	// Hub tunes fan-out internals.
	Hub hub.Config

	// Transport configures the WebSocket handler.
	Transport transport.HandlerDeps

	// Permissions is the source of truth for which columns each role
	// can read. Used by both filter compilation and the hub's per-event
	// projection.
	Permissions filter.PermissionChecker

	// RowAuth optionally restricts which subscribers receive each event
	// by row ownership, mirroring REST RLS. Nil = no per-row filtering.
	RowAuth hub.RowAuthorizer

	// Auth validates the apikey + JWT presented at the handshake.
	Auth transport.AuthValidator

	// RPC registry. Operators populate this before starting Service.
	RPC *rpc.Registry

	// Metrics recorder. NoOp by default.
	Metrics metrics.Recorder

	// Bus is the inter-node event transport. Use bus.NewLocal for
	// single-node, bus.NewNATS for clusters.
	Bus bus.Bus
}

// Service is the composed realtime subsystem. One instance per
// process. Static dependencies (hub, invoker, handler) are constructed
// in New; the WAL replicator and leader are created in Run so their
// lifecycles are bound to a single Run invocation.
type Service struct {
	cfg     Config
	hub     *hub.Hub
	invoker *rpc.Invoker
	handler *transport.Handler
}

// New composes a Service from the supplied Config. It does not start
// any goroutine — call Run for that.
func New(cfg Config) (*Service, error) {
	if cfg.Bus == nil {
		return nil, errors.New("realtime: Config.Bus is required")
	}
	if cfg.Permissions == nil {
		return nil, errors.New("realtime: Config.Permissions is required")
	}
	if cfg.Auth == nil {
		return nil, errors.New("realtime: Config.Auth is required")
	}
	if cfg.RPC == nil {
		cfg.RPC = rpc.NewRegistry()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NoOp{}
	}

	cfg.Hub.Permissions = cfg.Permissions
	cfg.Hub.RowAuth = cfg.RowAuth
	cfg.Hub.Metrics = cfg.Metrics
	h := hub.New(cfg.Hub, cfg.Bus)
	inv := rpc.NewInvoker(cfg.RPC, 0)
	inv.SetHook(rpcHook{m: cfg.Metrics})

	cfg.Transport.Hub = h
	cfg.Transport.Auth = cfg.Auth
	if cfg.Transport.Router == nil {
		cfg.Transport.Router = &router{hub: h, invoker: inv, auth: cfg.Auth}
	}
	// Let the transport's per-RPC rate limit pull overrides from the
	// rpc registry. The registry stays the source of truth for
	// function metadata; transport never holds a direct reference.
	if cfg.Transport.RateLimits.RPCRateFor == nil {
		reg := cfg.RPC
		cfg.Transport.RateLimits.RPCRateFor = func(fn string) int {
			if def, ok := reg.Lookup(fn); ok {
				return def.RatePerSec
			}
			return 0
		}
	}
	hdl := transport.NewHandler(cfg.Transport)

	return &Service{
		cfg:     cfg,
		hub:     h,
		invoker: inv,
		handler: hdl,
	}, nil
}

// Run starts every background loop owned by the service and blocks
// until ctx is canceled. Returns the first error from any subsystem
// (or context.Canceled on clean shutdown).
//
// Lifecycle:
//   - hub.Run consumes events from the bus and fans them out.
//   - When Leader.LockKey is non-zero, a leader election loop runs;
//     the replicator is started only when this node holds the lock,
//     and stopped on a transition to non-leader. The advisory lock
//     guarantees at most one rapibase node decodes a given slot.
//   - Otherwise (single-node) the replicator runs directly.
//
// Cancelling ctx initiates an orderly shutdown: leader steps down,
// replicator drains, hub closes. Run returns once every goroutine
// exits.
func (s *Service) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error { return s.hub.Run(ctx) })

	dec := wal.NewPgoutputDecoder()
	rep := wal.NewReplicator(s.cfg.WAL, walSink{bus: s.cfg.Bus}, dec)
	rep.SetLagGauge(walGauge{m: s.cfg.Metrics})

	if s.cfg.Leader.LockKey == 0 {
		// Single-node: replicator runs unconditionally.
		g.Go(func() error { return rep.Run(ctx) })
		return g.Wait()
	}

	// Multi-node: leader controls replicator start/stop.
	var (
		mu         sync.Mutex
		replCancel context.CancelFunc
		replDone   chan struct{}
	)
	startReplicator := func() {
		mu.Lock()
		defer mu.Unlock()
		if replCancel != nil {
			return
		}
		rctx, cancel := context.WithCancel(ctx)
		replCancel = cancel
		done := make(chan struct{})
		replDone = done
		go func() {
			defer close(done)
			_ = rep.Run(rctx)
		}()
	}
	stopReplicator := func() {
		mu.Lock()
		cancel := replCancel
		done := replDone
		replCancel = nil
		replDone = nil
		mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	}

	leader := wal.NewLeader(s.cfg.Leader, func(isLeader bool) {
		if isLeader {
			startReplicator()
		} else {
			stopReplicator()
		}
	})

	g.Go(func() error {
		err := leader.Run(ctx)
		stopReplicator()
		return err
	})
	return g.Wait()
}

// Bootstrap is a convenience wrapper that runs realtime.Bootstrap
// against the supplied connection string, using the service's
// configured PublicationName / SlotName. Call once per process at
// startup, before Run.
func (s *Service) Bootstrap(ctx context.Context, connString string) error {
	return Bootstrap(ctx, BootstrapConfig{
		ConnString:      connString,
		PublicationName: s.cfg.WAL.PublicationName,
		SlotName:        s.cfg.WAL.SlotName,
	})
}

// Handler returns the transport.Handler suitable to mount on the
// api/routes layer.
func (s *Service) Handler() *transport.Handler { return s.handler }

// Hub returns the underlying hub. Exposed for tests and admin tools.
func (s *Service) Hub() *hub.Hub { return s.hub }

// MetricsHandler returns an http.Handler emitting the Prometheus
// text exposition format for the configured recorder, or nil when
// the recorder is not a *metrics.Prom (e.g. NoOp in tests).
func (s *Service) MetricsHandler() http.Handler {
	if p, ok := s.cfg.Metrics.(*metrics.Prom); ok {
		return p.Handler()
	}
	return nil
}

// walSink adapts the bus.Bus into the wal.EventSink contract.
type walSink struct{ bus bus.Bus }

func (s walSink) OnEvent(ctx context.Context, ev wal.Event) error {
	return s.bus.Publish(ctx, ev)
}

// rpcHook adapts the rpc.Invoker observability hook to a
// metrics.Recorder. Lives here to keep the rpc package
// metrics-agnostic.
type rpcHook struct{ m metrics.Recorder }

func (h rpcHook) OnComplete(function, status string, d time.Duration) {
	if h.m == nil {
		return
	}
	h.m.Inc(metrics.MetricRPCCalls, metrics.Labels{
		"function": function,
		"status":   status,
	})
	h.m.Observe(metrics.MetricRPCDuration, d, metrics.Labels{
		"function": function,
	})
}

// walGauge adapts wal.LagGauge to a metrics.Recorder.
type walGauge struct{ m metrics.Recorder }

func (g walGauge) SetLagBytes(bytes uint64) {
	if g.m == nil {
		return
	}
	g.m.Gauge(metrics.MetricWALLagBytes, float64(bytes), nil)
}

// router is the inbound-frame dispatcher transport calls into. It
// translates each frame type into the appropriate subsystem call and
// maps subsystem errors into protocol-level error frames.
type router struct {
	hub     *hub.Hub
	invoker *rpc.Invoker
	auth    transport.AuthValidator
}

// Dispatch implements transport.Router.
func (r *router) Dispatch(ctx context.Context, sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	switch in.Type {
	case protocol.FrameSubscribe:
		return r.handleSubscribe(ctx, sess, in)
	case protocol.FrameUnsubscribe:
		return r.handleUnsubscribe(sess, in)
	case protocol.FrameBroadcastIn:
		return r.handleBroadcast(sess, in)
	case protocol.FramePresenceTrack:
		return r.handlePresenceTrack(sess, in)
	case protocol.FramePresenceUntrack:
		return r.handlePresenceUntrack(sess, in)
	case protocol.FrameRPC:
		return r.handleRPC(ctx, sess, in)
	case protocol.FrameResume:
		return r.handleResume(sess, in)
	case protocol.FrameSetAuth:
		return r.handleSetAuth(sess, in)
	default:
		return protocol.Frame{}, protocol.NewError(
			protocol.ErrInvalidPayload,
			fmt.Sprintf("unknown frame type %q", in.Type),
		)
	}
}

func (r *router) handleSubscribe(ctx context.Context, sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	if in.Channel == "" {
		return protocol.Frame{}, protocol.NewError(protocol.ErrInvalidPayload, "channel is required")
	}
	if err := r.hub.Attach(ctx, sess.Subscriber(), in.Channel, in.Config); err != nil {
		return protocol.Frame{}, mapHubErr(err)
	}
	return ackFrame(in.Ref, in.Channel), nil
}

func (r *router) handleUnsubscribe(sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	if in.Channel == "" {
		return protocol.Frame{}, protocol.NewError(protocol.ErrInvalidPayload, "channel is required")
	}
	r.hub.Detach(sess.Subscriber(), in.Channel)
	return ackFrame(in.Ref, in.Channel), nil
}

func (r *router) handleBroadcast(sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	if in.Channel == "" || in.Event == "" {
		return protocol.Frame{}, protocol.NewError(protocol.ErrInvalidPayload, "channel and event are required")
	}
	// force=false → respects each subscriber's BroadcastConfig.Self;
	// only server-emitted broadcasts use force=true.
	r.hub.Broadcast(in.Channel, sess.Subscriber(), in.Event, in.Payload, false)
	// Acks for client broadcasts are opt-in via the subscribe config's
	// BroadcastConfig.Ack; the client signals readiness for an ack by
	// setting Ref on the inbound frame. Honouring the protocol field
	// without round-tripping into subscribe config keeps the wire
	// simple.
	if in.Ref == "" {
		return protocol.Frame{}, nil
	}
	return ackFrame(in.Ref, in.Channel), nil
}

func (r *router) handlePresenceTrack(sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	if in.Channel == "" {
		return protocol.Frame{}, protocol.NewError(protocol.ErrInvalidPayload, "channel is required")
	}
	r.hub.TrackPresence(in.Channel, sess.Subscriber(), in.Payload)
	return ackFrame(in.Ref, in.Channel), nil
}

func (r *router) handlePresenceUntrack(sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	if in.Channel == "" {
		return protocol.Frame{}, protocol.NewError(protocol.ErrInvalidPayload, "channel is required")
	}
	r.hub.UntrackPresence(in.Channel, sess.Subscriber())
	return ackFrame(in.Ref, in.Channel), nil
}

func (r *router) handleRPC(ctx context.Context, sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	if in.Function == "" {
		return protocol.Frame{}, protocol.NewError(protocol.ErrInvalidPayload, "function is required")
	}
	res := r.invoker.Call(ctx, rpc.CallContext{
		Function: in.Function,
		Args:     in.Args,
		Role:     sess.Role(),
		UserID:   sess.UserID(),
	})
	if res.OK {
		return protocol.Frame{
			Type:    protocol.FrameRPCResponse,
			Ref:     in.Ref,
			Channel: in.Channel,
			OK:      true,
			Result:  res.Value,
		}, nil
	}
	return protocol.Frame{}, mapRPCErr(res.Err)
}

func (r *router) handleResume(sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	if in.Channel == "" {
		return protocol.Frame{}, protocol.NewError(protocol.ErrInvalidPayload, "channel is required")
	}
	if err := r.hub.Resume(sess.Subscriber(), in.Channel, in.FromLSN); err != nil {
		return protocol.Frame{}, mapHubErr(err)
	}
	return ackFrame(in.Ref, in.Channel), nil
}

func (r *router) handleSetAuth(sess *transport.Session, in protocol.Frame) (protocol.Frame, error) {
	role, userID, err := r.auth.Validate(sess.APIKey(), in.Token)
	if err != nil {
		return protocol.Frame{}, protocol.NewError(protocol.ErrUnauthorized, err.Error())
	}
	sess.SetAuth(role, userID)
	revoked := r.hub.ReValidateSubscriber(sess.Subscriber())

	ack := protocol.Frame{
		Type: protocol.FrameAck,
		Ref:  in.Ref,
	}
	if len(revoked) > 0 {
		ack.Detail = map[string]any{
			"lost_channels": revoked,
		}
	}
	return ack, nil
}

// ackFrame is the canonical success response carrying the original
// ref and channel back to the client.
func ackFrame(ref, channel string) protocol.Frame {
	return protocol.Frame{
		Type:    protocol.FrameAck,
		Ref:     ref,
		Channel: channel,
	}
}

// mapHubErr translates a hub error into a protocol.Error so the
// transport layer can produce the right wire code. Hub errors are a
// closed set, so a default of ErrInternal is safe.
func mapHubErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, hub.ErrTruncated):
		return protocol.NewError(protocol.ErrSlotTruncated, "resume LSN outside retention window")
	case errors.Is(err, hub.ErrUnknownChannel):
		return protocol.NewError(protocol.ErrUnknownChannel, err.Error())
	case errors.Is(err, hub.ErrInvalidArgument):
		return protocol.NewError(protocol.ErrInvalidPayload, err.Error())
	}
	// Compile / permission / validation errors from filter come back as
	// plain errors. Treat them as forbidden_filter so the client knows
	// it cannot retry the subscribe as-is.
	return protocol.NewError(protocol.ErrForbiddenFilter, err.Error())
}

// mapRPCErr maps invoker errors to protocol error codes.
func mapRPCErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, rpc.ErrUnknownFunction):
		return protocol.NewError(protocol.ErrUnknownFunction, err.Error())
	case errors.Is(err, rpc.ErrForbidden):
		return protocol.NewError(protocol.ErrUnauthorized, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewRetryableError(protocol.ErrInternal, "rpc timeout", 0)
	}
	return protocol.NewError(protocol.ErrInternal, err.Error())
}

