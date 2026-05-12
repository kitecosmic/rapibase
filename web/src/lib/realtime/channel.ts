import type { RealtimeClient } from './client'
import type {
  BroadcastConfig,
  DBEventType,
  Frame,
  PostgresChangesConfig,
  PresenceConfig,
  PresenceMembers,
  SubscribeConfig,
} from './protocol'
import type {
  BroadcastListener,
  BroadcastPayload,
  ChangeListener,
  ChangePayload,
  ChannelStatus,
  PresenceDiff,
  PresenceListener,
  PresenceSync,
  ResyncReason,
  SystemListener,
} from './types'
import { RealtimeError } from './types'
import { filter as buildFilter, type FilterBuilder, type FilterNode } from './filter'

interface PgListener {
  cfg: Omit<PostgresChangesConfig, 'filter'> & { filter?: FilterNode }
  fn: ChangeListener<any>
}

interface BroadcastListenerEntry {
  event: string | '*'
  fn: BroadcastListener<any>
}

interface PresenceListenerEntry {
  type: 'sync' | 'join' | 'leave' | 'update' | 'diff'
  fn: PresenceListener<any>
}

export interface SubscribeOptions {
  /** Auto-resume after reconnect using the last seen LSN. Default true. */
  resume?: boolean
  /** Override broadcast config. */
  broadcast?: BroadcastConfig
  /** Override presence config. */
  presence?: PresenceConfig
}

type StatusListener = (status: ChannelStatus, err?: RealtimeError) => void
type ResyncListener = (reason: ResyncReason) => void

/**
 * Channel exposes the per-channel API: postgres_changes subscriptions,
 * broadcast, presence and RPC. Channels are created via
 * `client.channel(name)` and reused for the lifetime of the client.
 */
export class Channel {
  readonly name: string
  private readonly client: RealtimeClient
  private status: ChannelStatus = 'closed'
  private pgListeners: PgListener[] = []
  private broadcastListeners: BroadcastListenerEntry[] = []
  private presenceListeners: PresenceListenerEntry[] = []
  private systemListeners = new Set<SystemListener>()
  private statusListeners = new Set<StatusListener>()
  private resyncListeners = new Set<ResyncListener>()
  private subOpts: SubscribeOptions = {}
  private lastLSN: string | null = null
  private trackedState: unknown | undefined
  private autoResume = true

  constructor(name: string, client: RealtimeClient) {
    this.name = name
    this.client = client
  }

  // -------- subscribe / unsubscribe ---------------------------------

  /**
   * Subscribe (or update) the channel. Idempotent — calling it again
   * with new listeners replaces the previous subscribe config on the
   * server and re-applies. The promise resolves once the server has
   * acked.
   */
  async subscribe(opts: SubscribeOptions = {}): Promise<void> {
    this.subOpts = opts
    this.autoResume = opts.resume ?? true
    this.setStatus('subscribing')
    try {
      this.client.connect()
      await this.sendSubscribe()
      this.setStatus('subscribed')
    } catch (e) {
      const err = e instanceof RealtimeError ? e : new RealtimeError('transport', String(e))
      this.setStatus('error', err)
      throw err
    }
  }

  /** Detach from the channel. After unsubscribe, listeners are kept;
   *  call subscribe() again to resume them. */
  async unsubscribe(): Promise<void> {
    try {
      await this.client.request({ type: 'unsubscribe', channel: this.name })
    } finally {
      this.setStatus('closed')
    }
  }

  // -------- listener registration -----------------------------------

  onChange<Row = Record<string, unknown>>(
    cfg: {
      event: DBEventType | '*'
      schema?: string
      table: string
      columns?: string[]
      filter?: (q: FilterBuilder) => FilterNode
    },
    fn: ChangeListener<Row>,
  ): this {
    this.pgListeners.push({
      cfg: {
        event: cfg.event,
        schema: cfg.schema,
        table: cfg.table,
        columns: cfg.columns,
        filter: cfg.filter ? buildFilter(cfg.filter) : undefined,
      },
      fn: fn as ChangeListener<any>,
    })
    return this
  }

  onBroadcast<P = unknown>(event: string | '*', fn: BroadcastListener<P>): this {
    this.broadcastListeners.push({ event, fn: fn as BroadcastListener<any> })
    return this
  }

  onPresence<S = unknown>(
    type: 'sync' | 'join' | 'leave' | 'update' | 'diff',
    fn: PresenceListener<S>,
  ): this {
    this.presenceListeners.push({ type, fn: fn as PresenceListener<any> })
    return this
  }

  onSystem(fn: SystemListener): this {
    this.systemListeners.add(fn)
    return this
  }

  onStatusChange(fn: StatusListener): this {
    this.statusListeners.add(fn)
    return this
  }

  onResync(fn: ResyncListener): this {
    this.resyncListeners.add(fn)
    return this
  }

  // -------- actions -------------------------------------------------

  /** Send a broadcast frame to other subscribers on this channel. */
  async broadcast<P = unknown>(
    event: string,
    payload: P,
    opts: { ack?: boolean } = {},
  ): Promise<void> {
    const frame: Frame = {
      type: 'broadcast',
      channel: this.name,
      event,
      payload,
    }
    if (opts.ack) {
      await this.client.request(frame)
      return
    }
    await this.client.send(frame)
  }

  /** Track presence with an opaque state value. */
  async track<S = unknown>(state: S): Promise<void> {
    this.trackedState = state
    await this.client.request({
      type: 'presence_track',
      channel: this.name,
      payload: state,
    })
  }

  /** Remove presence early (before disconnect). */
  async untrack(): Promise<void> {
    this.trackedState = undefined
    await this.client.request({
      type: 'presence_untrack',
      channel: this.name,
    })
  }

  /** Invoke a server-side RPC scoped to this channel. The optional
   *  timeoutMs is honoured by the server's invoker. */
  async invoke<Args = unknown, Result = unknown>(
    fn: string,
    args: Args,
    _opts: { timeoutMs?: number } = {},
  ): Promise<Result> {
    const resp = await this.client.request({
      type: 'rpc',
      channel: this.name,
      function: fn,
      args,
    })
    if (!resp.ok) {
      throw new RealtimeError('internal', 'rpc failed')
    }
    return resp.result as Result
  }

  /** Force a resume from the last known LSN. Useful after long pauses. */
  async resync(): Promise<void> {
    await this.sendResumeOrSubscribe('forced')
  }

  // -------- internal: called by client ------------------------------

  handleFrame(frame: Frame): void {
    switch (frame.type) {
      case 'postgres_changes':
        this.handlePostgresChanges(frame)
        return
      case 'broadcast':
        this.handleBroadcastFrame(frame)
        return
      case 'presence_state':
        this.emitPresence('sync', { members: frame.members ?? {} })
        return
      case 'presence_diff':
        this.emitPresenceDiff(frame)
        return
      case 'system':
        for (const fn of this.systemListeners) {
          fn({
            code: (frame.code ?? 'behind') as any,
            channel: frame.channel,
            detail: frame.detail,
          })
        }
        if (frame.code === 'auth_expired') {
          // Ack from set_auth may have signalled lost_channels in detail.
          // Trigger a resync so the application invalidates caches.
          for (const fn of this.resyncListeners) fn('auth_changed')
        }
        return
      case 'ack':
        // Set_auth carries `detail.lost_channels`; surface it as resync.
        if (
          frame.detail &&
          typeof frame.detail === 'object' &&
          Array.isArray((frame.detail as { lost_channels?: unknown[] }).lost_channels)
        ) {
          const lost = (frame.detail as { lost_channels: string[] }).lost_channels
          if (lost.includes(this.name)) {
            for (const fn of this.resyncListeners) fn('auth_changed')
          }
        }
        return
      default:
        return
    }
  }

  /** Called by the client after a reconnect lands on `welcome`. */
  async resubscribeAfterReconnect(): Promise<void> {
    if (this.status === 'closed') return
    this.setStatus('reconnecting')
    try {
      await this.sendResumeOrSubscribe('forced')
      this.setStatus('subscribed')
    } catch (e) {
      const err = e instanceof RealtimeError ? e : new RealtimeError('transport', String(e))
      if (err.code === 'slot_truncated') {
        for (const fn of this.resyncListeners) fn('slot_truncated')
        // Resubscribe fresh, ignoring LSN.
        this.lastLSN = null
        try {
          await this.sendSubscribe()
          this.setStatus('subscribed')
          return
        } catch (e2) {
          const err2 = e2 instanceof RealtimeError ? e2 : new RealtimeError('transport', String(e2))
          this.setStatus('error', err2)
          throw err2
        }
      }
      this.setStatus('error', err)
    }
  }

  // -------- private -------------------------------------------------

  private buildSubscribeConfig(): SubscribeConfig {
    const cfg: SubscribeConfig = {}
    if (this.pgListeners.length > 0) {
      cfg.postgres_changes = this.pgListeners.map((l) => ({
        event: l.cfg.event,
        schema: l.cfg.schema,
        table: l.cfg.table,
        columns: l.cfg.columns,
        filter: l.cfg.filter,
      }))
    }
    if (this.broadcastListeners.length > 0 || this.subOpts.broadcast) {
      cfg.broadcast = this.subOpts.broadcast ?? {}
    }
    if (this.presenceListeners.length > 0 || this.subOpts.presence) {
      cfg.presence = this.subOpts.presence ?? {}
    }
    return cfg
  }

  private async sendSubscribe(): Promise<void> {
    await this.client.request({
      type: 'subscribe',
      channel: this.name,
      config: this.buildSubscribeConfig(),
    })
    // Restore presence state if one was tracked before disconnect.
    if (this.trackedState !== undefined) {
      await this.client
        .request({
          type: 'presence_track',
          channel: this.name,
          payload: this.trackedState,
        })
        .catch(() => undefined)
    }
  }

  private async sendResumeOrSubscribe(_reason: ResyncReason): Promise<void> {
    const stored = this.client.resumeStorage.get(this.name)
    const lsn = this.lastLSN ?? stored
    if (!this.autoResume || !lsn) {
      await this.sendSubscribe()
      return
    }
    // Bubble up so resubscribeAfterReconnect can map slot_truncated.
    await this.client.request({
      type: 'resume',
      channel: this.name,
      from_lsn: lsn,
      config: this.buildSubscribeConfig(),
    })
  }

  private handlePostgresChanges(frame: Frame): void {
    if (frame.lsn) {
      this.lastLSN = frame.lsn
      this.client.resumeStorage.set(this.name, frame.lsn)
    }
    const payload: ChangePayload = {
      event: (frame.db_event ?? 'INSERT') as DBEventType,
      schema: frame.schema ?? '',
      table: frame.table ?? '',
      lsn: frame.lsn ?? '',
      commitTs: frame.commit_ts ? new Date(frame.commit_ts) : new Date(0),
      columns: frame.columns ?? [],
      new: (frame.new ?? null) as Record<string, unknown> | null,
      old: (frame.old ?? null) as Record<string, unknown> | null,
    }
    for (const l of this.pgListeners) {
      if (l.cfg.event !== '*' && l.cfg.event !== payload.event) continue
      if (l.cfg.schema && l.cfg.schema !== payload.schema) continue
      if (l.cfg.table && l.cfg.table !== payload.table) continue
      l.fn(payload)
    }
  }

  private handleBroadcastFrame(frame: Frame): void {
    const payload: BroadcastPayload = {
      event: frame.event ?? '',
      payload: frame.payload,
      from: frame.from,
    }
    for (const l of this.broadcastListeners) {
      if (l.event !== '*' && l.event !== payload.event) continue
      l.fn(payload)
    }
  }

  private emitPresence(
    _type: 'sync',
    sync: PresenceSync,
  ): void {
    for (const l of this.presenceListeners) {
      if (l.type === 'sync' || l.type === 'diff') l.fn(sync)
    }
  }

  private emitPresenceDiff(frame: Frame): void {
    const diff: PresenceDiff = {
      joins: (frame.joins ?? {}) as PresenceMembers,
      leaves: (frame.leaves ?? {}) as PresenceMembers,
      updates: (frame.updates ?? {}) as PresenceMembers,
    }
    for (const l of this.presenceListeners) {
      if (l.type === 'diff') { l.fn(diff); continue }
      if (l.type === 'join' && Object.keys(diff.joins).length > 0) l.fn(diff)
      if (l.type === 'leave' && Object.keys(diff.leaves).length > 0) l.fn(diff)
      if (l.type === 'update' && Object.keys(diff.updates).length > 0) l.fn(diff)
    }
  }

  private setStatus(status: ChannelStatus, err?: RealtimeError): void {
    this.status = status
    for (const fn of this.statusListeners) fn(status, err)
  }

  getStatus(): ChannelStatus { return this.status }
}
