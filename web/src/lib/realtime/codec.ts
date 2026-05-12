import {
  Frame,
  SUBPROTOCOL_JSON,
  SUBPROTOCOL_MSGPACK,
} from './protocol'

export type SubprotocolToken =
  | typeof SUBPROTOCOL_JSON
  | typeof SUBPROTOCOL_MSGPACK

export interface Codec {
  /** Returns the WebSocket subprotocol token this codec negotiates. */
  subprotocol(): SubprotocolToken
  /** Encodes a Frame for WebSocket.send. */
  encode(f: Frame): string | ArrayBuffer
  /** Decodes wire bytes (Blob/ArrayBuffer/string) into a Frame. */
  decode(data: string | ArrayBuffer): Frame
}

// JSON codec — the default for the browser SDK. msgpack is supported
// by the server but we ship JSON only here to avoid a runtime dep on
// @msgpack/msgpack. A separate `@rapibase/client-msgpack` adapter can
// add it later for clients that care about wire size (mobile).
export const jsonCodec: Codec = {
  subprotocol: () => SUBPROTOCOL_JSON,
  encode(f) {
    // omit-empty: strip undefined fields so the wire matches what the
    // Go server emits.
    return JSON.stringify(f, (_key, value) =>
      value === undefined ? undefined : value,
    )
  },
  decode(data) {
    const text = typeof data === 'string' ? data : new TextDecoder().decode(data)
    const obj = JSON.parse(text)
    if (!obj || typeof obj !== 'object' || typeof obj.type !== 'string') {
      throw new Error('invalid frame: missing or non-string type')
    }
    return obj as Frame
  },
}
