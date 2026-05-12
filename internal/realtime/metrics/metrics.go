// Package metrics defines the observability surface of the realtime
// subsystem. It does not import any specific metrics library: every
// counter and gauge is fronted by a Recorder interface that the root
// realtime package wires to the chosen backend (prometheus by default).
//
// Keeping the interface narrow lets tests use a no-op recorder and lets
// operators swap backends without touching the hot path.
package metrics

import "time"

// Recorder is the minimal API every realtime subpackage uses for
// observability. Implementations must be safe for concurrent use and
// must never block the caller.
type Recorder interface {
	// Inc increments a named counter by 1, scoped by optional labels.
	Inc(name string, labels Labels)

	// Add increments a named counter by an arbitrary non-negative value.
	Add(name string, value float64, labels Labels)

	// Gauge sets a named gauge to value.
	Gauge(name string, value float64, labels Labels)

	// Observe records a duration into a named histogram.
	Observe(name string, duration time.Duration, labels Labels)
}

// Labels are the dimension keys attached to a metric sample. Allocating
// a Labels map on the hot path is acceptable as long as the recorder is
// not blocked; backends that buffer should not block on overflow.
type Labels map[string]string

// Names of the metrics emitted across the realtime subsystem. Centralised
// so backends can enumerate them at startup if they need to.
const (
	MetricConnections           = "realtime_connections_active"
	MetricConnectionsTotal      = "realtime_connections_total"
	MetricChannels              = "realtime_channels_active"
	MetricSubscribers           = "realtime_subscribers_active"
	MetricEventsPublished       = "realtime_events_published_total"
	MetricEventsDelivered       = "realtime_events_delivered_total"
	MetricEventsDropped         = "realtime_events_dropped_total"
	MetricSlowConsumerEvictions = "realtime_slow_consumer_evictions_total"
	MetricWALLagBytes           = "realtime_wal_lag_bytes"
	MetricBroadcasts            = "realtime_broadcasts_total"
	MetricPresenceJoins         = "realtime_presence_joins_total"
	MetricPresenceLeaves        = "realtime_presence_leaves_total"
	MetricRPCCalls              = "realtime_rpc_calls_total"
	MetricRPCDuration           = "realtime_rpc_duration_seconds"
)

// NoOp is a Recorder that discards every sample. Useful in tests and as
// the default when no backend is wired.
type NoOp struct{}

func (NoOp) Inc(string, Labels)                       {}
func (NoOp) Add(string, float64, Labels)              {}
func (NoOp) Gauge(string, float64, Labels)            {}
func (NoOp) Observe(string, time.Duration, Labels)    {}
