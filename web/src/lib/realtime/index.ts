// Public entry point of the realtime SDK.
//
// Usage (minimal):
//
//   import { createRealtimeClient } from '@/lib/realtime'
//
//   const rt = createRealtimeClient({ url: location.origin, apiKey: ANON_KEY })
//   const room = rt.channel('room:42')
//     .onChange({ event: 'INSERT', table: 'messages' }, ev => console.log(ev.new))
//     .onBroadcast('typing', p => console.log(p.payload))
//   await room.subscribe()
//
//   await room.broadcast('typing', { user: 7 })
//   await room.track({ status: 'online' })

import { RealtimeClient, type ClientOptions } from './client'

export { RealtimeClient } from './client'
export type {
  ClientOptions,
  ResumeStorage,
  StateListener,
} from './client'
export { Channel } from './channel'
export type { SubscribeOptions } from './channel'
export { filter, q } from './filter'
export type { FilterBuilder, FilterNode, LeafNode, CompositeNode } from './filter'
export { RealtimeError } from './types'
export type {
  BroadcastListener,
  BroadcastPayload,
  ChangeListener,
  ChangePayload,
  ChannelStatus,
  ConnectionState,
  DBEventType,
  FrameOrigin,
  PresenceDiff,
  PresenceEntry,
  PresenceListener,
  PresenceMembers,
  PresenceSync,
  ResyncReason,
  SystemListener,
} from './types'
export type {
  BroadcastConfig,
  PostgresChangesConfig,
  PresenceConfig,
  SubscribeConfig,
} from './protocol'

/** Convenience factory — equivalent to `new RealtimeClient(opts)`. */
export function createRealtimeClient(opts: ClientOptions): RealtimeClient {
  return new RealtimeClient(opts)
}
