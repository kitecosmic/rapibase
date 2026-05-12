package protocol

import (
	"time"
)

// FrameType discriminates the union of wire messages exchanged on a
// realtime WebSocket connection. Values are stable strings serialized as the
// "type" field in every frame.
type FrameType string

// Inbound frames (client -> server).
const (
	FrameSubscribe       FrameType = "subscribe"
	FrameUnsubscribe     FrameType = "unsubscribe"
	FrameBroadcastIn     FrameType = "broadcast"
	FramePresenceTrack   FrameType = "presence_track"
	FramePresenceUntrack FrameType = "presence_untrack"
	FrameRPC             FrameType = "rpc"
	FrameResume          FrameType = "resume"
	FrameSetAuth         FrameType = "set_auth"
	FrameHeartbeatIn     FrameType = "heartbeat"
)

// Outbound frames (server -> client).
const (
	FrameWelcome         FrameType = "welcome"
	FrameAck             FrameType = "ack"
	FramePostgresChanges FrameType = "postgres_changes"
	FrameBroadcastOut    FrameType = "broadcast"
	FramePresenceState   FrameType = "presence_state"
	FramePresenceDiff    FrameType = "presence_diff"
	FrameRPCResponse     FrameType = "rpc_response"
	FrameError           FrameType = "error"
	FrameSystem          FrameType = "system"
	FrameHeartbeatOut    FrameType = "heartbeat"
)

// LSN is a Postgres log sequence number serialized as the canonical string
// representation ("X/Y" hex). It is opaque to clients but monotonic per
// channel, and is the dedup/resume key.
type LSN string

// EventType enumerates the kinds of database change events surfaced to
// subscribers.
type EventType string

const (
	EventInsert   EventType = "INSERT"
	EventUpdate   EventType = "UPDATE"
	EventDelete   EventType = "DELETE"
	EventTruncate EventType = "TRUNCATE"
)

// Frame is the single envelope used for every wire message in either
// direction. Fields are optional and meaningful depending on Type. Codecs
// serialize only the fields relevant to the frame type, never the zero
// values.
//
// Keeping a single struct (instead of a polymorphic interface) is a
// deliberate trade-off: codecs stay simple, type switches are explicit at
// call sites, and adding a new frame field is non-breaking.
type Frame struct {
	Type    FrameType `json:"type" msgpack:"type"`
	Ref     string    `json:"ref,omitempty" msgpack:"ref,omitempty"`
	Channel string    `json:"channel,omitempty" msgpack:"channel,omitempty"`

	// Subscribe / Resume
	Config *SubscribeConfig `json:"config,omitempty" msgpack:"config,omitempty"`
	FromLSN LSN              `json:"from_lsn,omitempty" msgpack:"from_lsn,omitempty"`

	// Broadcast
	Event   string       `json:"event,omitempty" msgpack:"event,omitempty"`
	Payload any          `json:"payload,omitempty" msgpack:"payload,omitempty"`
	From    *FrameOrigin `json:"from,omitempty" msgpack:"from,omitempty"`

	// Presence
	Members map[string][]PresenceEntry `json:"members,omitempty" msgpack:"members,omitempty"`
	Joins   map[string][]PresenceEntry `json:"joins,omitempty" msgpack:"joins,omitempty"`
	Leaves  map[string][]PresenceEntry `json:"leaves,omitempty" msgpack:"leaves,omitempty"`
	Updates map[string][]PresenceEntry `json:"updates,omitempty" msgpack:"updates,omitempty"`

	// RPC
	Function string `json:"function,omitempty" msgpack:"function,omitempty"`
	Args     any    `json:"args,omitempty" msgpack:"args,omitempty"`
	OK       bool   `json:"ok,omitempty" msgpack:"ok,omitempty"`
	Result   any    `json:"result,omitempty" msgpack:"result,omitempty"`

	// Postgres changes
	LSN LSN `json:"lsn,omitempty" msgpack:"lsn,omitempty"`
	// CommitTS is serialised as a string (RFC3339Nano) rather than a
	// time.Time because encoding/json does not honour `omitempty` on
	// struct-valued fields — a zero time would leak as
	// "0001-01-01T00:00:00Z" into every frame that does not carry a
	// timestamp (welcome, heartbeat, ack, etc.). String + omitempty
	// gives the wire we actually want.
	CommitTS string    `json:"commit_ts,omitempty" msgpack:"commit_ts,omitempty"`
	DBEvent  EventType `json:"db_event,omitempty" msgpack:"db_event,omitempty"`
	Schema   string    `json:"schema,omitempty" msgpack:"schema,omitempty"`
	Table    string    `json:"table,omitempty" msgpack:"table,omitempty"`
	New      any       `json:"new,omitempty" msgpack:"new,omitempty"`
	Old      any       `json:"old,omitempty" msgpack:"old,omitempty"`
	Columns  []string  `json:"columns,omitempty" msgpack:"columns,omitempty"`

	// Auth
	Token string `json:"token,omitempty" msgpack:"token,omitempty"`

	// Errors / system / ack
	Code      string `json:"code,omitempty" msgpack:"code,omitempty"`
	Message   string `json:"message,omitempty" msgpack:"message,omitempty"`
	Retryable bool   `json:"retryable,omitempty" msgpack:"retryable,omitempty"`
	RetryMs   int    `json:"retry_after_ms,omitempty" msgpack:"retry_after_ms,omitempty"`
	Detail    any    `json:"detail,omitempty" msgpack:"detail,omitempty"`

	// Welcome
	SessionID            string `json:"session_id,omitempty" msgpack:"session_id,omitempty"`
	ServerVersion        string `json:"server_version,omitempty" msgpack:"server_version,omitempty"`
	HeartbeatIntervalMs  int    `json:"heartbeat_interval_ms,omitempty" msgpack:"heartbeat_interval_ms,omitempty"`
	MaxPayloadBytes      int    `json:"max_payload_bytes,omitempty" msgpack:"max_payload_bytes,omitempty"`
	MaxChannelsPerConn   int    `json:"max_channels_per_connection,omitempty" msgpack:"max_channels_per_connection,omitempty"`
}

// SubscribeConfig is the config block of a subscribe/resume frame. Each
// section is optional; absent sections mean the subscriber does not want
// that kind of event.
type SubscribeConfig struct {
	PostgresChanges []PostgresChangesConfig `json:"postgres_changes,omitempty" msgpack:"postgres_changes,omitempty"`
	Broadcast       *BroadcastConfig        `json:"broadcast,omitempty" msgpack:"broadcast,omitempty"`
	Presence        *PresenceConfig         `json:"presence,omitempty" msgpack:"presence,omitempty"`
}

// PostgresChangesConfig declares one stream of database events for the
// subscriber. Filter is left as a generic any so the protocol package
// does not pull in the filter package; the hub compiles it via
// filter.Compile, which accepts the same any tree.
type PostgresChangesConfig struct {
	Event   string   `json:"event" msgpack:"event"` // INSERT | UPDATE | DELETE | *
	Schema  string   `json:"schema,omitempty" msgpack:"schema,omitempty"`
	Table   string   `json:"table" msgpack:"table"`
	Filter  any      `json:"filter,omitempty" msgpack:"filter,omitempty"`
	Columns []string `json:"columns,omitempty" msgpack:"columns,omitempty"`
}

// BroadcastConfig opts the subscriber into broadcast on a channel.
type BroadcastConfig struct {
	Self bool `json:"self,omitempty" msgpack:"self,omitempty"`
	Ack  bool `json:"ack,omitempty" msgpack:"ack,omitempty"`
}

// PresenceConfig opts the subscriber into presence on a channel.
type PresenceConfig struct {
	Key string `json:"key,omitempty" msgpack:"key,omitempty"`
}

// PresenceEntry is one presence instance for a given key on a channel. A
// single member may have multiple entries (e.g. multiple tabs).
type PresenceEntry struct {
	Ref      string    `json:"ref" msgpack:"ref"`
	JoinedAt time.Time `json:"joined_at" msgpack:"joined_at"`
	State    any       `json:"state,omitempty" msgpack:"state,omitempty"`
}

// FrameOrigin identifies the originator of a broadcast frame as seen by
// other subscribers.
type FrameOrigin struct {
	SessionID string `json:"session_id" msgpack:"session_id"`
	UserID    string `json:"user_id,omitempty" msgpack:"user_id,omitempty"`
}
