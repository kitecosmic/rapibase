// Wire types — TypeScript mirror of internal/realtime/protocol (Go).
//
// Kept intentionally simple: a single Frame interface with optional
// fields. Discriminating by `type` is the consumer's job (the
// dispatcher in client.ts uses narrow guards). Adding fields here is
// non-breaking; renaming or removing them is breaking and requires a
// wire version bump.

export const VERSION = 'rapibase-realtime.v1' as const
export const SUBPROTOCOL_JSON = 'rapibase-realtime.v1+json' as const
export const SUBPROTOCOL_MSGPACK = 'rapibase-realtime.v1+msgpack' as const

// ---- enums ---------------------------------------------------------

// Inbound (client -> server)
export type InboundType =
  | 'subscribe'
  | 'unsubscribe'
  | 'broadcast'
  | 'presence_track'
  | 'presence_untrack'
  | 'rpc'
  | 'resume'
  | 'set_auth'
  | 'heartbeat'

// Outbound (server -> client)
export type OutboundType =
  | 'welcome'
  | 'ack'
  | 'postgres_changes'
  | 'broadcast'
  | 'presence_state'
  | 'presence_diff'
  | 'rpc_response'
  | 'error'
  | 'system'
  | 'heartbeat'

export type FrameType = InboundType | OutboundType

export type DBEventType = 'INSERT' | 'UPDATE' | 'DELETE' | 'TRUNCATE'

// Errors that arrive in `code` of error frames. Closed enum; SDK
// consumers can switch on these without parsing free text.
export type ErrorCode =
  | 'unauthorized'
  | 'forbidden_filter'
  | 'unknown_channel'
  | 'unknown_function'
  | 'invalid_filter'
  | 'invalid_payload'
  | 'slot_truncated'
  | 'rate_limited'
  | 'quota_exceeded'
  | 'internal'

// Server-emitted system codes (informational).
export type SystemCode =
  | 'behind'
  | 'lsn_advance'
  | 'quota'
  | 'auth_expired'
  | 'server_shutdown'

// ---- subscribe config ---------------------------------------------

export interface PostgresChangesConfig {
  event: DBEventType | '*'
  schema?: string
  table: string
  filter?: unknown // structured object; built by FilterBuilder
  columns?: string[]
}

export interface BroadcastConfig {
  self?: boolean
  ack?: boolean
}

export interface PresenceConfig {
  key?: string
}

export interface SubscribeConfig {
  postgres_changes?: PostgresChangesConfig[]
  broadcast?: BroadcastConfig
  presence?: PresenceConfig
}

// ---- presence -----------------------------------------------------

export interface PresenceEntry<S = unknown> {
  ref: string
  joined_at: string // ISO-8601 from the wire
  state?: S
}

export type PresenceMembers<S = unknown> = Record<string, PresenceEntry<S>[]>

// ---- frame --------------------------------------------------------

export interface FrameOrigin {
  session_id: string
  user_id?: string
}

/**
 * Frame is the single wire envelope. Fields are optional and only some
 * are meaningful per `type` — the dispatcher in client.ts narrows by
 * type before reading specific fields.
 */
export interface Frame {
  type: FrameType
  ref?: string
  channel?: string

  // subscribe / resume
  config?: SubscribeConfig
  from_lsn?: string

  // broadcast
  event?: string
  payload?: unknown
  from?: FrameOrigin

  // presence
  members?: PresenceMembers
  joins?: PresenceMembers
  leaves?: PresenceMembers
  updates?: PresenceMembers

  // rpc
  function?: string
  args?: unknown
  ok?: boolean
  result?: unknown

  // postgres changes
  lsn?: string
  commit_ts?: string
  db_event?: DBEventType
  schema?: string
  table?: string
  new?: Record<string, unknown> | null
  old?: Record<string, unknown> | null
  columns?: string[]

  // auth
  token?: string

  // welcome
  session_id?: string
  server_version?: string
  heartbeat_interval_ms?: number
  max_payload_bytes?: number
  max_channels_per_connection?: number

  // errors / system / ack
  code?: string
  message?: string
  retryable?: boolean
  retry_after_ms?: number
  detail?: unknown
}

// ---- close codes (WebSocket) --------------------------------------

export const CLOSE_CODES = {
  UNSUPPORTED_VERSION: 4400,
  UNAUTHORIZED: 4401,
  HEARTBEAT_TIMEOUT: 4408,
  PAYLOAD_TOO_LARGE: 4413,
  SLOW_CONSUMER: 4429,
  INTERNAL_ERROR: 4500,
  SERVER_SHUTDOWN: 4503,
} as const

export type CloseCode = (typeof CLOSE_CODES)[keyof typeof CLOSE_CODES]
