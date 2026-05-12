import { jsonCodec, type Codec } from './codec'
import {
  CLOSE_CODES,
  type Frame,
  type FrameType,
  type OutboundType,
} from './protocol'
import { Backoff, type BackoffOptions } from './reconnect'
import { Channel } from './channel'
import { RealtimeError, type ConnectionState } from './types'

export interface ClientOptions {
  /** Base URL (https://... or http://...); SDK derives the WS URL. */
  url: string
  /** anon_key or service_key. Required. */
  apiKey: string
  /** Optional JWT presented at the handshake. Rotate via setAuth. */
  token?: string
  /** Wire codec. Defaults to JSON. */
  codec?: Codec
  /** Override the reconnect backoff. */
  reconnect?: BackoffOptions | false
  /**
   * Storage backend for resume LSNs per channel. Defaults to
   * sessionStorage in the browser, or an in-memory map in non-browser
   * environments.
   */
  resumeStorage?: ResumeStorage
}

export interface ResumeStorage {
  get(channel: string): string | null
  set(channel: string, lsn: string): void
  delete(channel: string): void
}

// In-memory fallback for non-browser hosts.
class MemoryStorage implements ResumeStorage {
  private map = new Map<string, string>()
  get(c: string) { return this.map.get(c) ?? null }
  set(c: string, v: string) { this.map.set(c, v) }
  delete(c: string) { this.map.delete(c) }
}

class SessionStorageAdapter implements ResumeStorage {
  private prefix = 'rapibase.realtime.lsn:'
  get(c: string) { return sessionStorage.getItem(this.prefix + c) }
  set(c: string, v: string) { sessionStorage.setItem(this.prefix + c, v) }
  delete(c: string) { sessionStorage.removeItem(this.prefix + c) }
}

export type StateListener = (state: ConnectionState) => void

/**
 * RealtimeClient owns the WebSocket and routes frames to the right
 * Channel. There is at most one socket per client; channels share it.
 *
 * Lifecycle:
 *   - First `channel().subscribe()` opens the socket.
 *   - Closing the last channel does NOT close the socket (it stays
 *     open for fast resubscribe). Call `close()` explicitly to tear
 *     down the connection.
 *   - Reconnects are automatic; resume LSNs are persisted per channel.
 */
export class RealtimeClient {
  private ws: WebSocket | null = null
  private state: ConnectionState = 'closed'
  private readonly codec: Codec
  private readonly backoff: Backoff | null
  private readonly stateListeners = new Set<StateListener>()
  private readonly channels = new Map<string, Channel>()
  private readonly pending = new Map<string, (f: Frame) => void>()
  private welcome: Frame | null = null
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private apiKey: string
  private token: string | undefined
  readonly url: string
  readonly resumeStorage: ResumeStorage

  constructor(opts: ClientOptions) {
    if (!opts.url) throw new RealtimeError('transport', 'url required')
    if (!opts.apiKey) throw new RealtimeError('transport', 'apiKey required')
    this.url = opts.url
    this.apiKey = opts.apiKey
    this.token = opts.token
    this.codec = opts.codec ?? jsonCodec
    this.backoff =
      opts.reconnect === false ? null : new Backoff(opts.reconnect ?? {})
    this.resumeStorage =
      opts.resumeStorage ??
      (typeof sessionStorage !== 'undefined'
        ? new SessionStorageAdapter()
        : new MemoryStorage())
  }

  /** Returns the current connection state. */
  getState(): ConnectionState { return this.state }

  /** Subscribe to connection state transitions. */
  onStateChange(fn: StateListener): () => void {
    this.stateListeners.add(fn)
    return () => this.stateListeners.delete(fn)
  }

  /**
   * Get (or create) a channel handle. Idempotent: the same name
   * returns the same Channel instance across the lifetime of the
   * client, so multiple consumers can share it.
   */
  channel(name: string): Channel {
    let c = this.channels.get(name)
    if (!c) {
      c = new Channel(name, this)
      this.channels.set(name, c)
    }
    return c
  }

  /**
   * Rotate the JWT in a live connection. The server reapplies
   * permissions to existing subscriptions; if any subscription
   * exceeds the new role, the server detaches it and the SDK emits
   * onResync('auth_changed') on the affected channels.
   */
  async setAuth(token: string | undefined): Promise<void> {
    this.token = token
    if (this.ws && this.state === 'open') {
      await this.request({ type: 'set_auth', token: token ?? '' })
    }
  }

  /** Internal: ensure the socket is connecting/open. */
  connect(): void {
    if (this.ws) return
    this.openSocket()
  }

  /** Close the socket and tear down everything. Idempotent. */
  close(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
    if (this.ws) {
      try { this.ws.close(1000, 'client closed') } catch { /* ignore */ }
      this.ws = null
    }
    this.setState('closed')
    this.backoff?.reset()
  }

  // -------- internal API for Channel ---------------------------------

  /** Send a frame and wait for the matching ack/error. */
  async request(frame: Frame): Promise<Frame> {
    const ref = frame.ref ?? randomRef()
    frame.ref = ref
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(ref)
        reject(new RealtimeError('timeout', `frame ${frame.type} timed out`))
      }, 10_000)
      this.pending.set(ref, (resp) => {
        clearTimeout(timer)
        this.pending.delete(ref)
        if (resp.type === 'error') {
          reject(
            new RealtimeError(
              (resp.code as RealtimeError['code']) ?? 'internal',
              resp.message ?? 'unknown error',
              {
                retryable: resp.retryable,
                retryAfterMs: resp.retry_after_ms,
              },
            ),
          )
          return
        }
        resolve(resp)
      })
      this.send(frame).catch(reject)
    })
  }

  /** Fire-and-forget send. Throws RealtimeError if the socket is not open. */
  async send(frame: Frame): Promise<void> {
    if (this.state !== 'open' || !this.ws) {
      // Wait briefly for the socket to open before giving up.
      await this.waitForOpen(2000)
    }
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new RealtimeError('transport', 'socket not open')
    }
    this.ws.send(this.codec.encode(frame))
  }

  // -------- private --------------------------------------------------

  private openSocket(): void {
    const wsURL = toWebSocketURL(this.url) + '/api/realtime/v1' + this.qs()
    this.setState(this.welcome ? 'reconnecting' : 'connecting')

    let ws: WebSocket
    try {
      ws = new WebSocket(wsURL, [this.codec.subprotocol()])
    } catch (err) {
      this.scheduleReconnect()
      return
    }
    ws.binaryType = 'arraybuffer'
    this.ws = ws

    ws.onopen = () => {
      // Don't transition to 'open' yet — wait for welcome frame.
    }
    ws.onmessage = (ev) => this.handleMessage(ev.data)
    ws.onerror = () => { /* surfaced via onclose */ }
    ws.onclose = (ev) => this.handleClose(ev)
  }

  private handleMessage(data: string | ArrayBuffer): void {
    let frame: Frame
    try {
      frame = this.codec.decode(data)
    } catch {
      return // malformed frame — server will reciprocate with an error
    }
    if (frame.type === 'welcome') {
      this.welcome = frame
      this.setState('open')
      this.backoff?.reset()
      this.startHeartbeat(frame.heartbeat_interval_ms ?? 25_000)
      this.resubscribeAll()
      return
    }
    if (frame.type === 'heartbeat') {
      this.send({ type: 'heartbeat' }).catch(() => undefined)
      return
    }
    // Ack/error/rpc_response — dispatch to pending request first.
    if (frame.ref) {
      const cb = this.pending.get(frame.ref)
      if (cb) {
        cb(frame)
        return
      }
    }
    // Otherwise route by channel.
    if (frame.channel) {
      const ch = this.channels.get(frame.channel)
      ch?.handleFrame(frame)
      return
    }
    // System events without a channel target — broadcast to every channel.
    if (frame.type === 'system') {
      for (const ch of this.channels.values()) {
        ch.handleFrame(frame)
      }
    }
  }

  private handleClose(ev: CloseEvent): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
    this.ws = null
    // Reject any in-flight requests.
    for (const cb of this.pending.values()) {
      cb({
        type: 'error',
        code: 'internal',
        message: `connection closed (${ev.code})`,
      })
    }
    this.pending.clear()

    // Permanent close codes are not retried.
    const permanent: number[] = [
      CLOSE_CODES.UNSUPPORTED_VERSION,
      CLOSE_CODES.UNAUTHORIZED,
    ]
    if (permanent.includes(ev.code) || !this.backoff) {
      this.setState('closed')
      return
    }
    this.scheduleReconnect()
  }

  private scheduleReconnect(): void {
    if (!this.backoff) {
      this.setState('closed')
      return
    }
    const delay = this.backoff.next()
    if (delay === null) {
      this.setState('closed')
      return
    }
    this.setState('reconnecting')
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.openSocket()
    }, delay)
  }

  private startHeartbeat(intervalMs: number): void {
    if (this.heartbeatTimer) clearInterval(this.heartbeatTimer)
    this.heartbeatTimer = setInterval(() => {
      this.send({ type: 'heartbeat' }).catch(() => undefined)
    }, intervalMs)
  }

  private resubscribeAll(): void {
    for (const ch of this.channels.values()) {
      void ch.resubscribeAfterReconnect()
    }
  }

  private async waitForOpen(timeoutMs: number): Promise<void> {
    if (this.state === 'open') return
    if (this.state === 'closed') this.connect()
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => {
        unsub()
        reject(new RealtimeError('timeout', 'socket open timeout'))
      }, timeoutMs)
      const unsub = this.onStateChange((s) => {
        if (s === 'open') {
          clearTimeout(timer)
          unsub()
          resolve()
        }
      })
    })
  }

  private setState(s: ConnectionState): void {
    if (this.state === s) return
    this.state = s
    for (const fn of this.stateListeners) fn(s)
  }

  private qs(): string {
    const p = new URLSearchParams({ apikey: this.apiKey })
    if (this.token) p.set('token', this.token)
    return '?' + p.toString()
  }
}

function toWebSocketURL(httpURL: string): string {
  if (httpURL.startsWith('https://')) return 'wss://' + httpURL.slice(8)
  if (httpURL.startsWith('http://')) return 'ws://' + httpURL.slice(7)
  return httpURL
}

function randomRef(): string {
  // Short ref ids are enough; uniqueness is per-conn, not global.
  return Math.random().toString(36).slice(2, 12)
}

/** Verifies a frame type at runtime (used by tests and dispatchers). */
export function isOutbound(t: FrameType): t is OutboundType {
  switch (t) {
    case 'welcome':
    case 'ack':
    case 'postgres_changes':
    case 'broadcast':
    case 'presence_state':
    case 'presence_diff':
    case 'rpc_response':
    case 'error':
    case 'system':
    case 'heartbeat':
      return true
    default:
      return false
  }
}
