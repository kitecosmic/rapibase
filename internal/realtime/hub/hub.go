// Package hub is the fan-out core of the realtime service.
//
// It receives decoded database events from the bus, filters them per
// subscriber, and pushes wire frames to each subscriber's outbound
// queue. It also coordinates broadcast and presence at the channel
// level, neither of which travels through the bus.
//
// The hub is sharded by channel name so subscriptions to unrelated
// channels do not contend on the same mutex. Sharding is the single
// biggest factor in fan-out throughput on multi-core machines.
package hub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/bus"
	"github.com/rapibase/rapibase/internal/realtime/filter"
	"github.com/rapibase/rapibase/internal/realtime/metrics"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// Config configures a Hub.
type Config struct {
	// Shards is the number of shard buckets. Should be a power of two
	// and at least the number of CPU cores. Defaults to 64 when zero.
	Shards int

	// SubscriberQueueSize is the bounded outbound queue per
	// subscriber. When full the subscriber is closed with
	// CloseSlowConsumer. Defaults to 1024 when zero.
	SubscriberQueueSize int

	// ResumeBufferSize is the number of recent events retained per
	// channel for resume(). Defaults to 4096 when zero.
	ResumeBufferSize int

	// Permissions is the policy used to filter events by role.
	// Required.
	Permissions filter.PermissionChecker

	// Metrics is the observability sink. Defaults to NoOp.
	Metrics metrics.Recorder

	// GaugeInterval is how often Run emits gauge metrics (active
	// channels / subscriptions). Defaults to 5s.
	GaugeInterval time.Duration
}

// Hub is the central fan-out component. One Hub instance per rapibase
// process.
type Hub struct {
	cfg    Config
	shards []*shard
	bus    bus.Bus

	// attachedChannels tracks which channels every subscriber holds,
	// so DetachAll can clean up in O(channels-of-this-sub) instead of
	// scanning every shard.
	attachMu         sync.Mutex
	attachedChannels map[*Subscriber]map[string]struct{}

	// tableIndex maps (schema, table) → channels that listen to that
	// relation. Fan-out reads from this instead of iterating every
	// channel in every shard, which is the difference between O(N)
	// and O(matches) for the hot path.
	tableIndex *channelIndex
}

// New constructs a Hub bound to the supplied bus. The hub does not
// start consuming until Run is called.
func New(cfg Config, b bus.Bus) *Hub {
	if cfg.Shards == 0 {
		cfg.Shards = 64
	}
	if cfg.SubscriberQueueSize == 0 {
		cfg.SubscriberQueueSize = 1024
	}
	if cfg.ResumeBufferSize == 0 {
		cfg.ResumeBufferSize = 4096
	}
	if cfg.Permissions == nil {
		cfg.Permissions = openPermissions{}
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NoOp{}
	}
	if cfg.GaugeInterval == 0 {
		cfg.GaugeInterval = 5 * time.Second
	}
	h := &Hub{
		cfg:              cfg,
		bus:              b,
		attachedChannels: make(map[*Subscriber]map[string]struct{}),
		tableIndex:       newChannelIndex(),
	}
	h.shards = make([]*shard, cfg.Shards)
	for i := range h.shards {
		h.shards[i] = newShard(cfg)
	}
	return h
}

// Run subscribes to the bus and fans events out to every channel until
// ctx is canceled or the bus is closed. Returns ctx.Err on cancel, or
// bus.ErrClosed when the bus shuts down.
//
// A side ticker emits gauge metrics (active channels / subscriptions)
// at GaugeInterval so operators can chart them without instrumenting
// every Attach / Detach individually.
func (h *Hub) Run(ctx context.Context) error {
	in, cancel, err := h.bus.Subscribe(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	gaugeTicker := time.NewTicker(h.cfg.GaugeInterval)
	defer gaugeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-gaugeTicker.C:
			h.emitGauges()
		case ev, ok := <-in:
			if !ok {
				return bus.ErrClosed
			}
			h.fanout(ev)
		}
	}
}

// emitGauges publishes the current channel and subscriber counts.
func (h *Hub) emitGauges() {
	channels := 0
	subs := 0
	for _, sh := range h.shards {
		sh.forEach(func(c *Channel) {
			channels++
			subs += c.SubscriberCount()
		})
	}
	h.cfg.Metrics.Gauge(metrics.MetricChannels, float64(channels), nil)
	h.cfg.Metrics.Gauge(metrics.MetricSubscribers, float64(subs), nil)
}

// fanout delivers an event only to channels that registered interest
// in its (schema, table) — channels with no matching pgStream are
// skipped entirely. Wildcard subscribers (empty schema or table)
// always participate; channels that have no pgStreams at all do not
// appear in the index and never see the event.
func (h *Hub) fanout(ev wal.Event) {
	candidates := h.tableIndex.lookup(ev.Schema, ev.Table)
	for _, c := range candidates {
		c.PublishEvent(ev)
	}
}

// Attach registers a subscriber on a channel. Returns an error mapped
// from filter compilation, permission validation, or the channel-level
// attach itself.
func (h *Hub) Attach(ctx context.Context, sub *Subscriber, channel string, cfg *protocol.SubscribeConfig) error {
	_ = ctx
	if sub == nil || channel == "" {
		return ErrInvalidArgument
	}
	sh := h.shardFor(channel)
	if sh == nil {
		return ErrInvalidArgument
	}
	c := sh.getOrCreate(channel)
	if err := c.Attach(sub, cfg); err != nil {
		// Roll back the channel creation if this was the first attach
		// attempt and the channel is now empty.
		if sh.removeIfEmpty(channel) {
			h.tableIndex.remove(c)
		}
		return err
	}

	h.attachMu.Lock()
	chans, ok := h.attachedChannels[sub]
	if !ok {
		chans = make(map[string]struct{})
		h.attachedChannels[sub] = chans
	}
	chans[channel] = struct{}{}
	h.attachMu.Unlock()

	// Reconcile the inverted index with the channel's new active set.
	tables, wild := c.ActiveTables()
	h.tableIndex.set(c, tables, wild)
	return nil
}

// Detach removes a subscriber from a channel. Idempotent.
func (h *Hub) Detach(sub *Subscriber, channel string) {
	if sub == nil || channel == "" {
		return
	}
	sh := h.shardFor(channel)
	if sh == nil {
		return
	}
	c := sh.get(channel)
	if c == nil {
		return
	}
	if empty := c.Detach(sub); empty {
		if sh.removeIfEmpty(channel) {
			h.tableIndex.remove(c)
		}
	} else {
		tables, wild := c.ActiveTables()
		h.tableIndex.set(c, tables, wild)
	}

	h.attachMu.Lock()
	if chans := h.attachedChannels[sub]; chans != nil {
		delete(chans, channel)
		if len(chans) == 0 {
			delete(h.attachedChannels, sub)
		}
	}
	h.attachMu.Unlock()
}

// DetachAll removes a subscriber from every channel it was attached to.
// Called by the transport when the underlying connection closes.
func (h *Hub) DetachAll(sub *Subscriber) {
	if sub == nil {
		return
	}
	h.attachMu.Lock()
	chans := h.attachedChannels[sub]
	delete(h.attachedChannels, sub)
	h.attachMu.Unlock()

	for channel := range chans {
		if sh := h.shardFor(channel); sh != nil {
			if c := sh.get(channel); c != nil {
				if empty := c.Detach(sub); empty {
					if sh.removeIfEmpty(channel) {
						h.tableIndex.remove(c)
					}
				} else {
					tables, wild := c.ActiveTables()
					h.tableIndex.set(c, tables, wild)
				}
			}
		}
	}
}

// Broadcast publishes an ephemeral frame to every subscriber of a
// channel. The sender receives its own broadcast only if its own
// BroadcastConfig.Self is true (or force is set, for server-emitted
// broadcasts). Returns the number of subscribers reached.
func (h *Hub) Broadcast(channel string, from *Subscriber, event string, payload any, force bool) int {
	if channel == "" {
		return 0
	}
	sh := h.shardFor(channel)
	if sh == nil {
		return 0
	}
	c := sh.get(channel)
	if c == nil {
		return 0
	}
	return c.Broadcast(from, event, payload, force)
}

// TrackPresence wires through to the channel's presence state.
func (h *Hub) TrackPresence(channel string, sub *Subscriber, state any) {
	if c := h.channel(channel); c != nil {
		c.TrackPresence(sub, state)
	}
}

// UntrackPresence wires through to the channel's presence state.
func (h *Hub) UntrackPresence(channel string, sub *Subscriber) {
	if c := h.channel(channel); c != nil {
		c.UntrackPresence(sub)
	}
}

// Resume replays buffered events to the subscriber from the given LSN.
// Returns ErrTruncated when the LSN is outside the resume window.
func (h *Hub) Resume(sub *Subscriber, channel string, from protocol.LSN) error {
	c := h.channel(channel)
	if c == nil {
		return ErrUnknownChannel
	}
	if !c.Has(sub) {
		return ErrUnknownChannel
	}
	return c.Resume(sub, from)
}

// PublishLocal injects an event directly into the hub bypassing the
// bus. Used by tests and by server-side admin tools that want to emit
// synthetic events.
func (h *Hub) PublishLocal(ev wal.Event) {
	h.fanout(ev)
}

// ReValidateSubscriber walks every channel the subscriber holds and
// detaches it from any channel where its current pgStreams would
// reference columns the current role can no longer read. Called by
// the realtime router after a successful set_auth so a subscriber
// that downgrades its JWT loses access to formerly-permitted streams
// immediately.
//
// Returns the sorted list of channels the subscriber was detached
// from so the router can include them in the ack frame.
func (h *Hub) ReValidateSubscriber(sub *Subscriber) []string {
	if sub == nil {
		return nil
	}
	h.attachMu.Lock()
	channels := make([]string, 0, len(h.attachedChannels[sub]))
	for ch := range h.attachedChannels[sub] {
		channels = append(channels, ch)
	}
	h.attachMu.Unlock()

	var revoked []string
	for _, channel := range channels {
		sh := h.shardFor(channel)
		if sh == nil {
			continue
		}
		c := sh.get(channel)
		if c == nil {
			continue
		}
		if !c.streamsExceedPermissions(sub, h.cfg.Permissions) {
			continue
		}
		h.Detach(sub, channel)
		revoked = append(revoked, channel)
	}
	// Sorted output makes the ack deterministic for tests and logs.
	sortStrings(revoked)
	return revoked
}

// sortStrings is a tiny inline insertion sort for short slices. Avoids
// pulling in "sort" for a single use in the hot-cold path.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j-1] > s[j] {
			s[j-1], s[j] = s[j], s[j-1]
			j--
		}
	}
}

// ChannelCount returns the total number of live channels across every
// shard. Diagnostic only — counting iterates all shards under their
// read locks.
func (h *Hub) ChannelCount() int {
	total := 0
	for _, sh := range h.shards {
		total += sh.channelCount()
	}
	return total
}

// channel is the internal lookup helper used by methods that need an
// already-created channel.
func (h *Hub) channel(name string) *Channel {
	sh := h.shardFor(name)
	if sh == nil {
		return nil
	}
	return sh.get(name)
}

// Errors returned by hub operations. Callers map these to protocol-level
// error codes (e.g. ErrUnknownChannel -> protocol.ErrUnknownChannel).
var (
	ErrInvalidArgument = errors.New("hub: invalid argument")
	ErrUnknownChannel  = errors.New("hub: unknown channel")
)

// openPermissions is the permissive default used when no checker was
// provided. Mostly relevant for tests and for the single-tenant default
// of small deployments. Production deployments must pass a real
// PermissionChecker through Config.
type openPermissions struct{}

func (openPermissions) CanRead(string, string, string, string) bool { return true }
func (openPermissions) ReadableColumns(_, _, _ string, cols []string) []string {
	return append([]string(nil), cols...)
}
