package hub

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/bus"
	"github.com/rapibase/rapibase/internal/realtime/metrics"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// spyRecorder counts how many times each metric was touched and the
// last labels seen. Safe for concurrent use.
type spyRecorder struct {
	mu     sync.Mutex
	counts map[string]int
	gauges map[string]float64
}

func newSpy() *spyRecorder {
	return &spyRecorder{
		counts: make(map[string]int),
		gauges: make(map[string]float64),
	}
}

func (s *spyRecorder) Inc(name string, _ metrics.Labels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[name]++
}

func (s *spyRecorder) Add(name string, value float64, _ metrics.Labels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[name] += int(value)
}

func (s *spyRecorder) Gauge(name string, value float64, _ metrics.Labels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gauges[name] = value
}

func (s *spyRecorder) Observe(string, time.Duration, metrics.Labels) {}

func (s *spyRecorder) count(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[name]
}

func (s *spyRecorder) gauge(name string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gauges[name]
}

func TestMetrics_PublishedAndDelivered(t *testing.T) {
	spy := newSpy()
	b := bus.NewLocal(8)
	defer b.Close()
	h := New(Config{
		Shards:              2,
		SubscriberQueueSize: 8,
		ResumeBufferSize:    8,
		Metrics:             spy,
	}, b)

	sub := NewSubscriber("s1", "anon", "", 8)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*", Table: "messages"}},
	}
	if err := h.Attach(context.Background(), sub, "room", cfg); err != nil {
		t.Fatal(err)
	}

	h.PublishLocal(wal.Event{
		LSN:    "0/1",
		Type:   protocol.EventInsert,
		Schema: "public",
		Table:  "messages",
		New:    wal.Row{"id": 1},
	})

	if got := spy.count(metrics.MetricEventsPublished); got != 1 {
		t.Fatalf("events_published = %d, want 1", got)
	}
	if got := spy.count(metrics.MetricEventsDelivered); got != 1 {
		t.Fatalf("events_delivered = %d, want 1", got)
	}
	if got := spy.count(metrics.MetricEventsDropped); got != 0 {
		t.Fatalf("events_dropped = %d, want 0", got)
	}
}

func TestMetrics_DroppedOnSlowConsumer(t *testing.T) {
	spy := newSpy()
	b := bus.NewLocal(8)
	defer b.Close()
	h := New(Config{
		Shards:              2,
		SubscriberQueueSize: 1, // tiny queue forces drops fast
		ResumeBufferSize:    16,
		Metrics:             spy,
	}, b)

	// Build a subscriber whose outbound queue is size 1 and never drain.
	sub := NewSubscriber("s1", "anon", "", 1)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*", Table: "t"}},
	}
	if err := h.Attach(context.Background(), sub, "c", cfg); err != nil {
		t.Fatal(err)
	}

	// First event fills the queue; second is dropped.
	for i := 0; i < 5; i++ {
		h.PublishLocal(wal.Event{
			LSN:    protocol.LSN("0/" + string(rune('1'+i))),
			Type:   protocol.EventInsert,
			Schema: "public",
			Table:  "t",
			New:    wal.Row{"id": i},
		})
	}
	if spy.count(metrics.MetricEventsDropped) == 0 {
		t.Fatalf("expected drops to be counted, got 0 (delivered=%d)",
			spy.count(metrics.MetricEventsDelivered))
	}
}

func TestMetrics_Broadcasts(t *testing.T) {
	spy := newSpy()
	b := bus.NewLocal(8)
	defer b.Close()
	h := New(Config{Shards: 2, SubscriberQueueSize: 8, Metrics: spy}, b)

	sub := NewSubscriber("s1", "anon", "", 8)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}}
	if err := h.Attach(context.Background(), sub, "room:42", cfg); err != nil {
		t.Fatal(err)
	}

	h.Broadcast("room:42", sub, "typing", nil, false)
	h.Broadcast("room:42", sub, "typing", nil, false)

	if got := spy.count(metrics.MetricBroadcasts); got != 2 {
		t.Fatalf("broadcasts = %d, want 2", got)
	}
}

func TestMetrics_GaugesEmittedByRun(t *testing.T) {
	spy := newSpy()
	b := bus.NewLocal(8)
	defer b.Close()
	h := New(Config{
		Shards:              2,
		SubscriberQueueSize: 8,
		Metrics:             spy,
		GaugeInterval:       30 * time.Millisecond,
	}, b)

	// Set up one channel with one subscriber so the gauges are
	// non-zero when Run ticks.
	sub := NewSubscriber("s1", "anon", "", 8)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}}
	if err := h.Attach(context.Background(), sub, "room", cfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	// Wait until the ticker has fired at least once.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if spy.gauge(metrics.MetricChannels) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := spy.gauge(metrics.MetricChannels); got != 1 {
		t.Fatalf("channels gauge = %v, want 1", got)
	}
	if got := spy.gauge(metrics.MetricSubscribers); got != 1 {
		t.Fatalf("subscribers gauge = %v, want 1", got)
	}
}

func TestMetrics_PresenceJoinsAndLeaves(t *testing.T) {
	spy := newSpy()
	b := bus.NewLocal(8)
	defer b.Close()
	h := New(Config{Shards: 2, SubscriberQueueSize: 8, Metrics: spy}, b)

	sub := NewSubscriber("s1", "anon", "user_7", 8)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{Presence: &protocol.PresenceConfig{Key: "user_7"}}
	if err := h.Attach(context.Background(), sub, "room", cfg); err != nil {
		t.Fatal(err)
	}

	h.TrackPresence("room", sub, "online")
	if got := spy.count(metrics.MetricPresenceJoins); got != 1 {
		t.Fatalf("joins = %d, want 1", got)
	}
	h.UntrackPresence("room", sub)
	if got := spy.count(metrics.MetricPresenceLeaves); got != 1 {
		t.Fatalf("leaves = %d, want 1", got)
	}
}
