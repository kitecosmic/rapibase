import type {
  DBEventType,
  ErrorCode,
  FrameOrigin,
  PresenceEntry,
  PresenceMembers,
  SystemCode,
} from './protocol'

/** Connection state surfaced through client.onStateChange and channel.subscribe. */
export type ConnectionState =
  | 'connecting'
  | 'open'
  | 'reconnecting'
  | 'closed'

/** Status reported by Channel.subscribe callback. */
export type ChannelStatus =
  | 'subscribing'
  | 'subscribed'
  | 'reconnecting'
  | 'closed'
  | 'error'

/** Structured error raised by the SDK. */
export class RealtimeError extends Error {
  code: ErrorCode | 'transport' | 'timeout'
  retryable: boolean
  retryAfterMs?: number

  constructor(
    code: RealtimeError['code'],
    message: string,
    opts: { retryable?: boolean; retryAfterMs?: number } = {},
  ) {
    super(message)
    this.name = 'RealtimeError'
    this.code = code
    this.retryable = opts.retryable ?? false
    this.retryAfterMs = opts.retryAfterMs
  }
}

/**
 * Payload delivered to onChange handlers. New is the post-image for
 * INSERT/UPDATE; Old is the pre-image for UPDATE (only REPLICA
 * IDENTITY columns) and DELETE.
 */
export interface ChangePayload<Row = Record<string, unknown>> {
  event: DBEventType
  schema: string
  table: string
  lsn: string
  commitTs: Date
  columns: string[]
  new: Row | null
  old: Row | null
}

/** Broadcast payload + sender metadata. */
export interface BroadcastPayload<P = unknown> {
  event: string
  payload: P
  from?: FrameOrigin
}

/** Presence diff handlers receive this shape. */
export interface PresenceDiff<S = unknown> {
  joins: PresenceMembers<S>
  leaves: PresenceMembers<S>
  updates: PresenceMembers<S>
}

/** Snapshot delivered after subscribe when presence is enabled. */
export interface PresenceSync<S = unknown> {
  members: PresenceMembers<S>
}

/** Reasons the SDK forces a resync. */
export type ResyncReason = 'slot_truncated' | 'auth_changed' | 'forced'

/** Listener registered with onChange. */
export type ChangeListener<Row = Record<string, unknown>> = (
  payload: ChangePayload<Row>,
) => void

/** Listener registered with onBroadcast. */
export type BroadcastListener<P = unknown> = (
  payload: BroadcastPayload<P>,
) => void

/** Listener for presence events. */
export type PresenceListener<S = unknown> = (
  diff: PresenceDiff<S> | PresenceSync<S>,
) => void

/** System frame listener (informational events from the server). */
export type SystemListener = (event: {
  code: SystemCode
  channel?: string
  detail?: unknown
}) => void

/** Re-export the protocol types consumers commonly need. */
export type { PresenceEntry, PresenceMembers, FrameOrigin, DBEventType }
