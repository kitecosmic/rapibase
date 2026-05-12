// Exponential backoff with jitter for WebSocket reconnection.
//
// Algorithm: delay = min(maxDelayMs, baseMs * 2^attempt) ± jitter.
// Jitter is ±25% to spread reconnect storms when many clients lose
// their connection simultaneously (e.g. server restart).

export interface BackoffOptions {
  baseMs?: number
  maxDelayMs?: number
  jitter?: boolean
  maxAttempts?: number
}

export class Backoff {
  private attempt = 0
  private readonly baseMs: number
  private readonly maxDelayMs: number
  private readonly jitter: boolean
  private readonly maxAttempts: number

  constructor(opts: BackoffOptions = {}) {
    this.baseMs = opts.baseMs ?? 250
    this.maxDelayMs = opts.maxDelayMs ?? 30_000
    this.jitter = opts.jitter ?? true
    this.maxAttempts = opts.maxAttempts ?? Number.POSITIVE_INFINITY
  }

  /** Returns the delay for the next attempt, or null when exhausted. */
  next(): number | null {
    if (this.attempt >= this.maxAttempts) return null
    const exp = Math.min(this.maxDelayMs, this.baseMs * 2 ** this.attempt)
    this.attempt++
    if (!this.jitter) return exp
    const variance = exp * 0.25
    return Math.round(exp - variance + Math.random() * variance * 2)
  }

  /** Resets the attempt counter after a successful connection. */
  reset(): void {
    this.attempt = 0
  }

  get attempts(): number {
    return this.attempt
  }
}
