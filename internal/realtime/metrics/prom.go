package metrics

import (
	"math"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prom is a Recorder backed by github.com/prometheus/client_golang. It
// pre-declares every metric name from this package with its correct
// Prometheus type (Counter / Gauge / Histogram) and the closed set of
// label names that name may carry.
//
// Pre-declaration matters: it prevents the most common observability
// failure in event systems — accidental high-cardinality labels.
// Calling Inc with a label not declared for that metric is a no-op;
// the operator cannot accidentally explode the time-series space.
type Prom struct {
	reg        *prometheus.Registry
	counters   map[string]*prometheus.CounterVec
	gauges     map[string]*prometheus.GaugeVec
	histograms map[string]*prometheus.HistogramVec
	labelOrder map[string][]string
}

// NewProm builds a Prom recorder using the supplied registry, or a
// fresh one if reg is nil. Using your own registry lets you keep the
// realtime metrics isolated from other subsystems on the same process.
func NewProm(reg *prometheus.Registry) *Prom {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	p := &Prom{
		reg:        reg,
		counters:   make(map[string]*prometheus.CounterVec),
		gauges:     make(map[string]*prometheus.GaugeVec),
		histograms: make(map[string]*prometheus.HistogramVec),
		labelOrder: make(map[string][]string),
	}
	p.registerKnown()
	return p
}

// registerKnown declares the full set of metrics and their label
// vocabulary. To add a metric: add the const in metrics.go and an
// entry here. There is no auto-creation.
func (p *Prom) registerKnown() {
	type spec struct {
		name, help string
		labels     []string
	}

	counters := []spec{
		{MetricConnectionsTotal, "Total realtime connections opened since process start.", nil},
		{MetricEventsPublished, "Database change events published into the hub.", []string{"schema", "table"}},
		{MetricEventsDelivered, "Database change events delivered to subscribers.", []string{"schema", "table"}},
		{MetricEventsDropped, "Events dropped because the subscriber queue was full.", nil},
		{MetricSlowConsumerEvictions, "Subscribers evicted because their queue stayed full.", nil},
		{MetricBroadcasts, "Broadcast frames emitted across all channels.", []string{"channel_kind"}},
		{MetricPresenceJoins, "Presence join events emitted.", nil},
		{MetricPresenceLeaves, "Presence leave events emitted.", nil},
		{MetricRPCCalls, "RPC invocations completed.", []string{"function", "status"}},
	}
	for _, s := range counters {
		cv := prometheus.NewCounterVec(prometheus.CounterOpts{Name: s.name, Help: s.help}, s.labels)
		p.reg.MustRegister(cv)
		p.counters[s.name] = cv
		p.labelOrder[s.name] = s.labels
	}

	gauges := []spec{
		{MetricConnections, "Currently open realtime connections.", nil},
		{MetricChannels, "Currently active channels.", nil},
		{MetricSubscribers, "Currently active subscribers (sum across channels).", nil},
		{MetricWALLagBytes, "Bytes the replicator is behind the Postgres primary.", nil},
	}
	for _, s := range gauges {
		gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: s.name, Help: s.help}, s.labels)
		p.reg.MustRegister(gv)
		p.gauges[s.name] = gv
		p.labelOrder[s.name] = s.labels
	}

	histograms := []struct {
		spec
		buckets []float64
	}{
		{spec{MetricRPCDuration, "Wall-clock duration of RPC handler invocations.", []string{"function"}}, prometheus.DefBuckets},
	}
	for _, h := range histograms {
		hv := prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: h.name, Help: h.help, Buckets: h.buckets},
			h.labels,
		)
		p.reg.MustRegister(hv)
		p.histograms[h.name] = hv
		p.labelOrder[h.name] = h.labels
	}
}

// labelValues returns the values for `name` in the declared order.
// Missing keys produce empty strings (Prometheus accepts empty label
// values). Extra keys are silently dropped — this is the contract that
// keeps high-cardinality at bay.
func (p *Prom) labelValues(name string, l Labels) []string {
	order := p.labelOrder[name]
	if len(order) == 0 {
		return nil
	}
	out := make([]string, len(order))
	for i, k := range order {
		out[i] = l[k]
	}
	return out
}

// Inc implements Recorder.
func (p *Prom) Inc(name string, l Labels) {
	if c, ok := p.counters[name]; ok {
		c.WithLabelValues(p.labelValues(name, l)...).Inc()
	}
}

// Add implements Recorder.
func (p *Prom) Add(name string, value float64, l Labels) {
	if value <= 0 || !isFinite(value) {
		return
	}
	if c, ok := p.counters[name]; ok {
		c.WithLabelValues(p.labelValues(name, l)...).Add(value)
	}
}

// Gauge implements Recorder.
func (p *Prom) Gauge(name string, value float64, l Labels) {
	if !isFinite(value) {
		return
	}
	if g, ok := p.gauges[name]; ok {
		g.WithLabelValues(p.labelValues(name, l)...).Set(value)
	}
}

// Observe implements Recorder.
func (p *Prom) Observe(name string, duration time.Duration, l Labels) {
	if duration < 0 {
		return
	}
	if h, ok := p.histograms[name]; ok {
		h.WithLabelValues(p.labelValues(name, l)...).Observe(duration.Seconds())
	}
}

// Handler returns an http.Handler exposing the underlying registry in
// the Prometheus text format. Mount it on /metrics.
func (p *Prom) Handler() http.Handler {
	return promhttp.HandlerFor(p.reg, promhttp.HandlerOpts{})
}

// Registry returns the underlying registry. Useful when the operator
// wants to add custom metrics that share the realtime namespace.
func (p *Prom) Registry() *prometheus.Registry { return p.reg }

// isFinite guards against NaN / Inf which would corrupt the metric.
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
