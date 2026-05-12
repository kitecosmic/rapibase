package metrics

import (
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scrape returns the Prometheus text output produced by p.Handler.
func scrape(t *testing.T, p *Prom) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	p.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("scrape status %d, body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// containsLine returns true if any line of the scrape output starts
// with the supplied prefix. Matching prefixes (rather than full
// substring) avoids false positives from HELP / TYPE comments.
func containsLine(text, prefix string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func TestProm_Counter_Inc(t *testing.T) {
	p := NewProm(nil)
	p.Inc(MetricEventsDropped, nil)
	p.Inc(MetricEventsDropped, nil)
	p.Inc(MetricEventsDropped, nil)

	body := scrape(t, p)
	if !containsLine(body, MetricEventsDropped+" 3") {
		t.Fatalf("expected counter at 3, got:\n%s", body)
	}
}

func TestProm_Counter_Add(t *testing.T) {
	p := NewProm(nil)
	p.Add(MetricEventsDropped, 5, nil)
	p.Add(MetricEventsDropped, 2.5, nil)
	body := scrape(t, p)
	if !containsLine(body, MetricEventsDropped+" 7.5") {
		t.Fatalf("expected counter at 7.5, got:\n%s", body)
	}
}

func TestProm_Counter_AddIgnoresNonPositive(t *testing.T) {
	p := NewProm(nil)
	p.Add(MetricEventsDropped, -1, nil) // ignored
	p.Add(MetricEventsDropped, 0, nil)  // ignored
	p.Add(MetricEventsDropped, math.NaN(), nil) // ignored
	p.Add(MetricEventsDropped, math.Inf(1), nil) // ignored
	p.Add(MetricEventsDropped, 1, nil)
	body := scrape(t, p)
	if !containsLine(body, MetricEventsDropped+" 1") {
		t.Fatalf("expected counter at exactly 1, got:\n%s", body)
	}
}

func TestProm_Counter_WithLabels(t *testing.T) {
	p := NewProm(nil)
	p.Inc(MetricEventsPublished, Labels{"schema": "public", "table": "messages"})
	p.Inc(MetricEventsPublished, Labels{"schema": "public", "table": "messages"})
	p.Inc(MetricEventsPublished, Labels{"schema": "public", "table": "users"})

	body := scrape(t, p)
	if !containsLine(body, MetricEventsPublished+`{schema="public",table="messages"} 2`) {
		t.Fatalf("expected 2 for messages, got:\n%s", body)
	}
	if !containsLine(body, MetricEventsPublished+`{schema="public",table="users"} 1`) {
		t.Fatalf("expected 1 for users, got:\n%s", body)
	}
}

func TestProm_Counter_UnknownLabelIgnored(t *testing.T) {
	p := NewProm(nil)
	// "kind" is not a declared label for events_published — it must
	// not produce a new time series.
	p.Inc(MetricEventsPublished, Labels{"schema": "public", "table": "x", "kind": "bogus"})
	body := scrape(t, p)
	if strings.Contains(body, "bogus") {
		t.Fatalf("undeclared label leaked into output:\n%s", body)
	}
	if !containsLine(body, MetricEventsPublished+`{schema="public",table="x"} 1`) {
		t.Fatalf("expected canonical labels, got:\n%s", body)
	}
}

func TestProm_Gauge_Set(t *testing.T) {
	p := NewProm(nil)
	p.Gauge(MetricConnections, 5, nil)
	p.Gauge(MetricConnections, 12, nil)
	body := scrape(t, p)
	if !containsLine(body, MetricConnections+" 12") {
		t.Fatalf("gauge should be 12, got:\n%s", body)
	}
}

func TestProm_Gauge_RejectsNaN(t *testing.T) {
	p := NewProm(nil)
	p.Gauge(MetricConnections, 3, nil)
	p.Gauge(MetricConnections, math.NaN(), nil) // ignored
	body := scrape(t, p)
	if !containsLine(body, MetricConnections+" 3") {
		t.Fatalf("NaN should have been ignored, got:\n%s", body)
	}
}

func TestProm_Histogram_Observe(t *testing.T) {
	p := NewProm(nil)
	p.Observe(MetricRPCDuration, 50*time.Millisecond, Labels{"function": "ping"})
	p.Observe(MetricRPCDuration, 150*time.Millisecond, Labels{"function": "ping"})
	body := scrape(t, p)
	if !containsLine(body, MetricRPCDuration+`_count{function="ping"} 2`) {
		t.Fatalf("expected 2 observations, got:\n%s", body)
	}
	// Sum is 0.2 seconds total — Prometheus rendering uses scientific
	// notation depending on value; just verify the _sum line exists.
	if !strings.Contains(body, MetricRPCDuration+`_sum{function="ping"}`) {
		t.Fatalf("histogram sum missing:\n%s", body)
	}
}

func TestProm_Observe_IgnoresNegative(t *testing.T) {
	p := NewProm(nil)
	p.Observe(MetricRPCDuration, -1*time.Second, Labels{"function": "x"})
	body := scrape(t, p)
	if containsLine(body, MetricRPCDuration+`_count{function="x"} 1`) {
		t.Fatalf("negative observation should not register, got:\n%s", body)
	}
}

func TestProm_UnknownMetric_NoOp(t *testing.T) {
	p := NewProm(nil)
	// Should not panic. Should not appear in output.
	p.Inc("realtime_does_not_exist", nil)
	p.Gauge("realtime_does_not_exist", 1, nil)
	p.Observe("realtime_does_not_exist", time.Second, nil)
	body := scrape(t, p)
	if strings.Contains(body, "does_not_exist") {
		t.Fatalf("unknown metric leaked: %s", body)
	}
}

func TestProm_AllMetricsAreRegistered(t *testing.T) {
	// Touch every known metric once so the registry has samples to
	// emit. prometheus.Registry.Gather only returns metric families
	// that have at least one observation — describing without sampling
	// is not exposed by the public API.
	p := NewProm(nil)
	p.Inc(MetricConnectionsTotal, nil)
	p.Inc(MetricEventsPublished, Labels{"schema": "public", "table": "t"})
	p.Inc(MetricEventsDelivered, Labels{"schema": "public", "table": "t"})
	p.Inc(MetricEventsDropped, nil)
	p.Inc(MetricSlowConsumerEvictions, nil)
	p.Inc(MetricBroadcasts, Labels{"channel_kind": "room"})
	p.Inc(MetricPresenceJoins, nil)
	p.Inc(MetricPresenceLeaves, nil)
	p.Inc(MetricRPCCalls, Labels{"function": "ping", "status": "ok"})
	p.Gauge(MetricConnections, 1, nil)
	p.Gauge(MetricChannels, 1, nil)
	p.Gauge(MetricSubscribers, 1, nil)
	p.Gauge(MetricWALLagBytes, 1, nil)
	p.Observe(MetricRPCDuration, time.Millisecond, Labels{"function": "ping"})

	families, err := p.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(families))
	for _, f := range families {
		got[f.GetName()] = true
	}

	want := []string{
		MetricConnectionsTotal,
		MetricEventsPublished,
		MetricEventsDelivered,
		MetricEventsDropped,
		MetricSlowConsumerEvictions,
		MetricBroadcasts,
		MetricPresenceJoins,
		MetricPresenceLeaves,
		MetricRPCCalls,
		MetricConnections,
		MetricChannels,
		MetricSubscribers,
		MetricWALLagBytes,
		MetricRPCDuration,
	}
	for _, m := range want {
		if !got[m] {
			t.Fatalf("metric %s not registered (got: %v)", m, got)
		}
	}
}

func TestNoOp_DoesNotPanic(t *testing.T) {
	var r Recorder = NoOp{}
	r.Inc("anything", Labels{"x": "y"})
	r.Add("anything", 1, nil)
	r.Gauge("anything", 1.5, nil)
	r.Observe("anything", time.Millisecond, nil)
}
