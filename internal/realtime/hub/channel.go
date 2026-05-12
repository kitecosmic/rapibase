package hub

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/filter"
	"github.com/rapibase/rapibase/internal/realtime/metrics"
	"github.com/rapibase/rapibase/internal/realtime/presence"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// Channel is a logical fan-out group. Subscribers on the same channel
// see each other's broadcast frames, share presence state, and each
// declares (independently) which postgres_changes streams to receive.
type Channel struct {
	name string

	mu          sync.RWMutex
	subscribers map[*Subscriber]*subscriberStreams
	resumeBuf   *Buffer
	presence    *presence.State

	cfg Config
}

// subscriberStreams holds everything a subscriber asked for on this
// channel: zero or more pg_changes streams (each with a compiled
// predicate and projection), plus optional broadcast / presence opts.
type subscriberStreams struct {
	pg        []pgStream
	broadcast *protocol.BroadcastConfig
	presence  *protocol.PresenceConfig
}

type pgStream struct {
	event   string // "INSERT" | "UPDATE" | "DELETE" | "*" | ""
	schema  string
	table   string
	columns []string // requested projection; empty = all readable columns
	pred    filter.Predicate

	// referenced is the sorted set of column names referenced by the
	// filter tree, cached at compile time. set_auth re-validation
	// reads this without re-walking the filter.
	referenced []string
}

func newChannel(name string, cfg Config) *Channel {
	return &Channel{
		name:        name,
		subscribers: make(map[*Subscriber]*subscriberStreams),
		resumeBuf:   NewBuffer(cfg.ResumeBufferSize),
		presence:    presence.NewState(),
		cfg:         cfg,
	}
}

// Name returns the channel name.
func (c *Channel) Name() string { return c.name }

// SubscriberCount returns the number of subscribers currently attached.
func (c *Channel) SubscriberCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.subscribers)
}

// Attach registers a subscriber on this channel with the supplied
// configuration. Compiles filters and validates that the role can read
// the columns referenced by every filter and every requested
// projection. Returns the underlying error from filter.Compile or
// filter.ValidateColumns; the caller maps it to a protocol error.
func (c *Channel) Attach(sub *Subscriber, cfg *protocol.SubscribeConfig) error {
	streams := &subscriberStreams{}
	role := sub.Role()

	if cfg != nil {
		for _, pc := range cfg.PostgresChanges {
			pred, err := filter.Compile(pc.Filter)
			if err != nil {
				return err
			}
			refCols := filter.ReferencedColumns(pc.Filter)
			if err := filter.ValidateColumns(c.cfg.Permissions, role, pc.Schema, pc.Table, refCols); err != nil {
				return err
			}
			if err := filter.ValidateColumns(c.cfg.Permissions, role, pc.Schema, pc.Table, pc.Columns); err != nil {
				return err
			}
			streams.pg = append(streams.pg, pgStream{
				event:      strings.ToUpper(pc.Event),
				schema:     pc.Schema,
				table:      pc.Table,
				columns:    append([]string(nil), pc.Columns...),
				pred:       pred,
				referenced: refCols,
			})
		}
		streams.broadcast = cfg.Broadcast
		streams.presence = cfg.Presence
	}

	c.mu.Lock()
	c.subscribers[sub] = streams
	c.mu.Unlock()

	// Push the presence snapshot to the new subscriber so it starts
	// with the current state. Done outside the lock so a slow enqueue
	// does not block other attach/detach operations.
	if streams.presence != nil {
		snap := c.presence.Snapshot()
		if len(snap) > 0 {
			sub.enqueue(protocol.Frame{
				Type:    protocol.FramePresenceState,
				Channel: c.name,
				Members: snap,
			})
		}
	}
	return nil
}

// Detach removes a subscriber from this channel and cleans up its
// presence entries. Returns true when the channel becomes empty so the
// shard can garbage-collect it.
func (c *Channel) Detach(sub *Subscriber) (empty bool) {
	c.mu.Lock()
	_, was := c.subscribers[sub]
	delete(c.subscribers, sub)
	empty = len(c.subscribers) == 0
	c.mu.Unlock()
	if !was {
		return
	}

	if diff := c.presence.DropRef(sub.ID()); !diff.Empty() {
		c.cfg.Metrics.Add(metrics.MetricPresenceLeaves, float64(diffMembers(diff.Leaves)), nil)
		c.fanoutPresenceDiff(diff)
	}
	return
}

// PublishEvent fans out a WAL event to subscribers whose streams match
// and whose filter predicate accepts the row. The event is also added
// to the resume buffer regardless of who consumes it.
func (c *Channel) PublishEvent(ev wal.Event) {
	c.resumeBuf.Push(ev)
	c.cfg.Metrics.Inc(metrics.MetricEventsPublished, metrics.Labels{
		"schema": ev.Schema,
		"table":  ev.Table,
	})
	c.deliverEvent(ev, nil)
}

// deliverEvent does the per-subscriber match/filter/project/enqueue.
// When `only` is non-nil, only that subscriber is considered (used for
// resume replay).
func (c *Channel) deliverEvent(ev wal.Event, only *Subscriber) {
	row := rowForEval(ev)

	c.mu.RLock()
	defer c.mu.RUnlock()
	for sub, streams := range c.subscribers {
		if only != nil && sub != only {
			continue
		}
		if sub.IsClosed() {
			continue
		}
		for _, s := range streams.pg {
			if !s.matches(ev) {
				continue
			}
			if !s.pred(row) {
				continue
			}
			frame, ok := c.buildEventFrame(sub, ev, s)
			if !ok {
				continue
			}
			if sub.enqueue(frame) {
				c.cfg.Metrics.Inc(metrics.MetricEventsDelivered, metrics.Labels{
					"schema": ev.Schema,
					"table":  ev.Table,
				})
			} else {
				c.cfg.Metrics.Inc(metrics.MetricEventsDropped, nil)
			}
		}
	}
}

func (c *Channel) buildEventFrame(sub *Subscriber, ev wal.Event, s pgStream) (protocol.Frame, bool) {
	requested := s.columns
	if len(requested) == 0 {
		requested = defaultColumns(ev)
	}
	readable := c.cfg.Permissions.ReadableColumns(sub.Role(), ev.Schema, ev.Table, requested)
	if len(readable) == 0 {
		return protocol.Frame{}, false
	}

	frame := protocol.Frame{
		Type:     protocol.FramePostgresChanges,
		Channel:  c.name,
		LSN:      ev.LSN,
		CommitTS: formatCommitTS(ev.CommitTS),
		DBEvent:  ev.Type,
		Schema:   ev.Schema,
		Table:    ev.Table,
		Columns:  readable,
	}
	if ev.New != nil {
		frame.New = projectRow(ev.New, readable)
	}
	if ev.Old != nil {
		frame.Old = projectRow(ev.Old, readable)
	}
	return frame, true
}

// Broadcast publishes an ephemeral frame to every subscriber that
// opted into broadcast. By default the originating subscriber receives
// the frame only if its own BroadcastConfig.Self is true; the force
// flag overrides that per-subscriber preference and is reserved for
// server-emitted broadcasts (admin tools, system notifications) where
// fan-out semantics must ignore client opt-outs.
//
// Returns the count of subscribers reached (useful for ack semantics).
func (c *Channel) Broadcast(from *Subscriber, event string, payload any, force bool) int {
	frame := protocol.Frame{
		Type:    protocol.FrameBroadcastOut,
		Channel: c.name,
		Event:   event,
		Payload: payload,
	}
	if from != nil {
		origin := from.Origin()
		frame.From = &origin
	}

	delivered := 0
	c.mu.RLock()
	defer c.mu.RUnlock()
	for sub, streams := range c.subscribers {
		if streams.broadcast == nil {
			continue
		}
		if from != nil && sub == from && !force && !streams.broadcast.Self {
			continue
		}
		if sub.enqueue(frame) {
			delivered++
		}
	}
	c.cfg.Metrics.Inc(metrics.MetricBroadcasts, metrics.Labels{
		"channel_kind": channelKind(c.name),
	})
	return delivered
}

// channelKind reduces a channel name to its prefix before the first
// colon so high-cardinality channel names ("room:42", "room:43" …) do
// not explode label cardinality on the broadcast metric. Channels
// without a colon are reported as "default".
func channelKind(name string) string {
	if i := strings.IndexByte(name, ':'); i > 0 {
		return name[:i]
	}
	return "default"
}

// diffMembers counts every entry across every key in a presence
// joins/leaves/updates map. A single Track may produce more than one
// entry (multi-tab); the metric reflects the literal number of
// presences entering or leaving.
func diffMembers(m map[string][]protocol.PresenceEntry) int {
	total := 0
	for _, entries := range m {
		total += len(entries)
	}
	return total
}

// TrackPresence records a presence state for a subscriber and broadcasts
// the resulting diff to every subscriber that opted into presence.
func (c *Channel) TrackPresence(sub *Subscriber, state any) {
	key := c.presenceKey(sub)
	diff := c.presence.Track(key, sub.ID(), state)
	if diff.Empty() {
		return
	}
	c.cfg.Metrics.Add(metrics.MetricPresenceJoins, float64(diffMembers(diff.Joins)), nil)
	c.fanoutPresenceDiff(diff)
}

// UntrackPresence removes a subscriber's presence entry early (before
// disconnect) and broadcasts the diff.
func (c *Channel) UntrackPresence(sub *Subscriber) {
	key := c.presenceKey(sub)
	diff := c.presence.Untrack(key, sub.ID())
	if diff.Empty() {
		return
	}
	c.cfg.Metrics.Add(metrics.MetricPresenceLeaves, float64(diffMembers(diff.Leaves)), nil)
	c.fanoutPresenceDiff(diff)
}

func (c *Channel) fanoutPresenceDiff(d presence.Diff) {
	frame := d.ToFrame(c.name)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for sub, streams := range c.subscribers {
		if streams.presence == nil {
			continue
		}
		sub.enqueue(frame)
	}
}

// presenceKey returns the stable identifier for a subscriber on this
// channel. If the subscriber's subscribe config specified a key, it
// wins; otherwise the JWT user id is used, falling back to the session
// id for anonymous connections.
func (c *Channel) presenceKey(sub *Subscriber) string {
	c.mu.RLock()
	streams := c.subscribers[sub]
	c.mu.RUnlock()
	if streams != nil && streams.presence != nil && streams.presence.Key != "" {
		return streams.presence.Key
	}
	if uid := sub.UserID(); uid != "" {
		return uid
	}
	return sub.ID()
}

// Resume replays buffered events with LSN strictly greater than from
// for the supplied subscriber, applying its filters and permissions
// just like a live event. Returns ErrTruncated when the requested LSN
// is older than the resume window.
func (c *Channel) Resume(sub *Subscriber, from protocol.LSN) error {
	events, err := c.resumeBuf.Replay(from)
	if err != nil {
		return err
	}
	for _, ev := range events {
		c.deliverEvent(ev, sub)
	}
	return nil
}

// Has reports whether the subscriber is attached to this channel.
func (c *Channel) Has(sub *Subscriber) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.subscribers[sub]
	return ok
}

// ActiveTables returns the union, across every subscriber, of the
// concrete (schema, table) relations the channel currently listens
// for, plus a flag indicating whether any subscriber holds a wildcard
// stream (empty schema or empty table). The Hub consults this after
// every Attach/Detach to keep its inverted index in sync.
//
// "Concrete" means both schema and table are non-empty in the
// pgStream config — partial wildcards collapse into the wildcard
// flag.
func (c *Channel) ActiveTables() ([]tableKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.subscribers) == 0 {
		return nil, false
	}
	seen := make(map[tableKey]struct{})
	wildcard := false
	for _, streams := range c.subscribers {
		for _, s := range streams.pg {
			if s.schema == "" || s.table == "" {
				wildcard = true
				continue
			}
			seen[tableKey{schema: s.schema, table: s.table}] = struct{}{}
		}
	}
	out := make([]tableKey, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out, wildcard
}

// streamsExceedPermissions returns true when at least one pgStream the
// subscriber holds on this channel references columns the supplied
// PermissionChecker (typically reflecting a freshly-rotated role)
// would refuse. Called by Hub.ReValidateSubscriber after set_auth.
func (c *Channel) streamsExceedPermissions(sub *Subscriber, perms filter.PermissionChecker) bool {
	if perms == nil {
		return false
	}
	c.mu.RLock()
	streams := c.subscribers[sub]
	c.mu.RUnlock()
	if streams == nil {
		return false
	}

	role := sub.Role()
	for _, s := range streams.pg {
		if filter.ValidateColumns(perms, role, s.schema, s.table, s.referenced) != nil {
			return true
		}
		if filter.ValidateColumns(perms, role, s.schema, s.table, s.columns) != nil {
			return true
		}
	}
	return false
}

// matches checks whether a stream's event/schema/table targets the
// supplied WAL event. Empty fields are wildcards.
func (s pgStream) matches(ev wal.Event) bool {
	if s.schema != "" && s.schema != ev.Schema {
		return false
	}
	if s.table != "" && s.table != ev.Table {
		return false
	}
	if s.event != "" && s.event != "*" && s.event != string(ev.Type) {
		return false
	}
	return true
}

// rowForEval picks the row the filter should evaluate against. For
// INSERT/UPDATE that is New; for DELETE it is Old. Truncate events have
// neither, so the filter sees an empty row (and typically still
// matches via wildcards).
func rowForEval(ev wal.Event) filter.Row {
	switch ev.Type {
	case protocol.EventDelete:
		if ev.Old != nil {
			return ev.Old
		}
	default:
		if ev.New != nil {
			return ev.New
		}
		if ev.Old != nil {
			return ev.Old
		}
	}
	return wal.Row{}
}

// defaultColumns returns the sorted union of every column present in
// ev.New and ev.Old. Used when a subscriber did not specify a
// projection.
func defaultColumns(ev wal.Event) []string {
	seen := make(map[string]struct{})
	for k := range ev.New {
		seen[k] = struct{}{}
	}
	for k := range ev.Old {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// formatCommitTS serialises the WAL event's commit time for the wire.
// Returns "" for the zero value so the field is omitted via omitempty
// — otherwise every event without a real timestamp would emit
// "0001-01-01T00:00:00Z".
func formatCommitTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// projectRow returns a map containing only the requested columns from
// the source row. Returns nil if src is nil; returns an empty map if
// none of the requested columns are present.
func projectRow(src wal.Row, cols []string) any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(cols))
	for _, c := range cols {
		if v, ok := src[c]; ok {
			out[c] = v
		}
	}
	return out
}
