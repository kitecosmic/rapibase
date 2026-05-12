import { useEffect, useMemo, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  BookOpen,
  Activity,
  Pause,
  Play,
  Trash2,
  Loader2,
  Radio,
} from 'lucide-react'
import { authedFetch } from '../lib/api'
import { useAuthStore } from '../store/auth'

type Tab = 'docs' | 'monitor'

export default function Realtime() {
  const [tab, setTab] = useState<Tab>('docs')

  return (
    <div className="space-y-6">
      <header className="flex items-center gap-3">
        <Radio className="w-8 h-8 text-blue-600" />
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Realtime</h1>
          <p className="text-gray-600 text-sm">
            Stream database changes, broadcasts, and presence over a single
            WebSocket. One connection per app, no separate service.
          </p>
        </div>
      </header>

      <nav className="border-b border-gray-200">
        <div className="flex gap-1">
          <TabButton active={tab === 'docs'} onClick={() => setTab('docs')} icon={BookOpen}>
            Docs
          </TabButton>
          <TabButton active={tab === 'monitor'} onClick={() => setTab('monitor')} icon={Activity}>
            Live Monitor
          </TabButton>
        </div>
      </nav>

      {tab === 'docs' ? <DocsPane /> : <LiveMonitor />}
    </div>
  )
}

function TabButton({
  active,
  onClick,
  icon: Icon,
  children,
}: {
  active: boolean
  onClick: () => void
  icon: typeof BookOpen
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-2 px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
        active
          ? 'border-blue-600 text-blue-600'
          : 'border-transparent text-gray-500 hover:text-gray-700'
      }`}
    >
      <Icon className="w-4 h-4" />
      {children}
    </button>
  )
}

// ---- Docs pane ---------------------------------------------------

interface DocCategory {
  title: string
  slugs: string[]
}

function DocsPane() {
  const [categories, setCategories] = useState<DocCategory[] | null>(null)
  const [selected, setSelected] = useState<string>('INDEX')
  const [body, setBody] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Load the slug list once.
  useEffect(() => {
    let cancelled = false
    fetch('/api/realtime/docs')
      .then((r) => r.json())
      .then((data) => {
        if (!cancelled) setCategories(data.categories ?? [])
      })
      .catch((err) => {
        if (!cancelled) setError(String(err))
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Load the selected doc body.
  useEffect(() => {
    if (!selected) return
    let cancelled = false
    setLoading(true)
    setError(null)
    fetch(`/api/realtime/docs/${selected}`)
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return r.text()
      })
      .then((text) => {
        if (!cancelled) {
          setBody(text)
          setLoading(false)
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(String(err))
          setLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [selected])

  if (error && !categories) {
    return (
      <div className="rounded-lg bg-red-50 border border-red-200 p-4 text-sm text-red-800">
        Could not load docs: {error}
      </div>
    )
  }

  return (
    <div className="grid grid-cols-1 lg:grid-cols-[240px_1fr] gap-6">
      <aside className="lg:sticky lg:top-4 self-start">
        <nav className="space-y-4">
          {categories === null ? (
            <div className="flex items-center gap-2 text-gray-400 text-sm">
              <Loader2 className="w-4 h-4 animate-spin" />
              Loading
            </div>
          ) : (
            categories.map((cat) => (
              <div key={cat.title}>
                <h3 className="text-xs font-semibold uppercase tracking-wider text-gray-400 px-3 mb-1">
                  {cat.title}
                </h3>
                <div className="space-y-0.5">
                  {cat.slugs.map((slug) => (
                    <button
                      key={slug}
                      onClick={() => setSelected(slug)}
                      className={`block w-full text-left px-3 py-1.5 rounded-md text-sm transition-colors ${
                        slug === selected
                          ? 'bg-blue-50 text-blue-700 font-medium'
                          : 'text-gray-700 hover:bg-gray-50'
                      }`}
                    >
                      {prettifySlug(slug)}
                    </button>
                  ))}
                </div>
              </div>
            ))
          )}
        </nav>
      </aside>

      <article className="bg-white border border-gray-200 rounded-lg p-6 min-h-[60vh]">
        {loading ? (
          <div className="flex items-center gap-2 text-gray-400 text-sm">
            <Loader2 className="w-4 h-4 animate-spin" />
            Loading {selected}…
          </div>
        ) : error ? (
          <div className="text-red-700 text-sm">{error}</div>
        ) : (
          <div className="prose prose-slate max-w-none prose-headings:font-semibold prose-h1:text-2xl prose-h1:mt-0 prose-h1:mb-4 prose-h2:text-xl prose-h2:mt-8 prose-h2:mb-3 prose-h3:text-lg prose-h3:mt-6 prose-h3:mb-2 prose-p:leading-relaxed prose-li:my-1 prose-code:bg-gray-100 prose-code:px-1.5 prose-code:py-0.5 prose-code:rounded prose-code:text-rose-700 prose-code:font-medium prose-code:before:content-none prose-code:after:content-none prose-pre:bg-gray-900 prose-pre:text-gray-100 prose-pre:my-4 prose-a:text-blue-600 prose-a:no-underline hover:prose-a:underline prose-table:text-sm prose-th:bg-gray-50 prose-thead:border-b prose-thead:border-gray-200">
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              components={{
                a: ({ href, children, ...props }) => {
                  // Internal doc links (relative .md) → switch the tab
                  // in-place instead of navigating away.
                  if (href && href.endsWith('.md') && !href.startsWith('http')) {
                    const target = href.replace(/^\.\//, '').replace(/\.md$/, '')
                    return (
                      <button
                        type="button"
                        onClick={(e) => {
                          e.preventDefault()
                          setSelected(target)
                        }}
                        className="text-blue-600 underline"
                      >
                        {children}
                      </button>
                    )
                  }
                  return (
                    <a href={href} target="_blank" rel="noreferrer" {...props}>
                      {children}
                    </a>
                  )
                },
              }}
            >
              {body}
            </ReactMarkdown>
          </div>
        )}
      </article>
    </div>
  )
}

function prettifySlug(slug: string): string {
  if (slug.toUpperCase() === 'INDEX') return 'Overview'
  return slug
    .split('-')
    .map((s) => s.charAt(0).toUpperCase() + s.slice(1))
    .join(' ')
}

// ---- Live Monitor pane -------------------------------------------

interface LiveEvent {
  id: number
  ts: Date
  raw: string
  preview: string
  kind: string
}

function LiveMonitor() {
  const [connected, setConnected] = useState(false)
  const [paused, setPaused] = useState(false)
  const [events, setEvents] = useState<LiveEvent[]>([])
  const [error, setError] = useState<string | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const heartbeatRef = useRef<number | null>(null)
  const idRef = useRef(1)
  const pausedRef = useRef(false)
  useEffect(() => {
    pausedRef.current = paused
  }, [paused])

  const { token } = useAuthStore()

  // Fetch the project keys so we can authenticate the monitor.
  const [anonKey, setAnonKey] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    authedFetch('/project')
      .then((r) => r.json())
      .then((p) => {
        if (!cancelled) setAnonKey(p.anon_key)
      })
      .catch(() => {
        if (!cancelled) setError('could not load project keys')
      })
    return () => {
      cancelled = true
    }
  }, [])

  const wsURL = useMemo(() => {
    if (!anonKey) return null
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const params = new URLSearchParams({ apikey: anonKey })
    if (token) params.set('token', token)
    return `${proto}//${window.location.host}/api/realtime/v1?${params.toString()}`
  }, [anonKey, token])

  useEffect(() => {
    if (!wsURL) return

    setError(null)
    const ws = new WebSocket(wsURL, ['rapibase-realtime.v1+json'])
    wsRef.current = ws

    ws.onopen = () => setConnected(true)
    ws.onclose = (ev) => {
      setConnected(false)
      if (ev.code !== 1000) setError(`closed: ${ev.code} ${ev.reason}`)
    }
    ws.onerror = () => setError('connection error')
    ws.onmessage = (msg) => {
      if (pausedRef.current) return
      try {
        const frame = JSON.parse(msg.data)
        // Reply heartbeats so the server doesn't kick us.
        if (frame.type === 'heartbeat') {
          ws.send(JSON.stringify({ type: 'heartbeat' }))
        }
        setEvents((prev) => {
          const next: LiveEvent = {
            id: idRef.current++,
            ts: new Date(),
            raw: typeof msg.data === 'string' ? msg.data : '',
            preview: previewFor(frame),
            kind: frame.type ?? 'unknown',
          }
          // Keep the last 200 — enough for inspection, bounded for memory.
          const out = [next, ...prev]
          return out.length > 200 ? out.slice(0, 200) : out
        })
      } catch {
        // Non-JSON message; ignore (msgpack would land here if negotiated).
      }
    }

    // Client-side heartbeat keeps the connection alive even when no
    // server-initiated ping arrives (defensive — the server already
    // sends them, but a stale state could miss one).
    heartbeatRef.current = window.setInterval(() => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'heartbeat' }))
      }
    }, 20_000)

    return () => {
      if (heartbeatRef.current) {
        clearInterval(heartbeatRef.current)
        heartbeatRef.current = null
      }
      ws.close()
      wsRef.current = null
    }
  }, [wsURL])

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between bg-white border border-gray-200 rounded-lg p-4">
        <div className="flex items-center gap-3">
          <span
            className={`inline-flex items-center gap-2 text-sm font-medium ${
              connected ? 'text-green-600' : 'text-gray-500'
            }`}
          >
            <span
              className={`w-2 h-2 rounded-full ${
                connected ? 'bg-green-500 animate-pulse' : 'bg-gray-400'
              }`}
            />
            {connected ? 'connected' : 'disconnected'}
          </span>
          <span className="text-sm text-gray-500">
            {events.length} event{events.length === 1 ? '' : 's'}
          </span>
          {error && <span className="text-sm text-red-600">{error}</span>}
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setPaused((p) => !p)}
            className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-md border border-gray-300 text-gray-700 hover:bg-gray-50"
            disabled={!connected}
          >
            {paused ? (
              <>
                <Play className="w-4 h-4" /> Resume
              </>
            ) : (
              <>
                <Pause className="w-4 h-4" /> Pause
              </>
            )}
          </button>
          <button
            onClick={() => setEvents([])}
            className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-md border border-gray-300 text-gray-700 hover:bg-gray-50"
          >
            <Trash2 className="w-4 h-4" /> Clear
          </button>
        </div>
      </div>

      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        {events.length === 0 ? (
          <div className="px-6 py-10 text-center text-sm text-gray-500">
            {connected
              ? 'Waiting for events…  insert / update / delete a row in any table to see it here.'
              : 'Connecting…'}
          </div>
        ) : (
          <ul className="divide-y divide-gray-100 max-h-[60vh] overflow-y-auto">
            {events.map((ev) => (
              <li key={ev.id} className="px-4 py-3 hover:bg-gray-50">
                <div className="flex items-baseline justify-between gap-4">
                  <div className="flex items-center gap-2">
                    <KindBadge kind={ev.kind} />
                    <span className="font-mono text-xs text-gray-500">
                      {ev.ts.toLocaleTimeString()}
                    </span>
                  </div>
                  <span className="text-xs text-gray-400">#{ev.id}</span>
                </div>
                <details className="mt-1">
                  <summary className="text-sm text-gray-700 cursor-pointer">
                    {ev.preview}
                  </summary>
                  <pre className="mt-2 text-xs bg-gray-900 text-gray-100 rounded p-3 overflow-x-auto">
                    {pretty(ev.raw)}
                  </pre>
                </details>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function KindBadge({ kind }: { kind: string }) {
  const style =
    kind === 'postgres_changes'
      ? 'bg-blue-100 text-blue-700'
      : kind === 'broadcast'
      ? 'bg-purple-100 text-purple-700'
      : kind === 'presence_state' || kind === 'presence_diff'
      ? 'bg-amber-100 text-amber-700'
      : kind === 'rpc_response'
      ? 'bg-green-100 text-green-700'
      : kind === 'error'
      ? 'bg-red-100 text-red-700'
      : kind === 'welcome' || kind === 'ack' || kind === 'system'
      ? 'bg-gray-100 text-gray-700'
      : kind === 'heartbeat'
      ? 'bg-gray-100 text-gray-400'
      : 'bg-gray-100 text-gray-600'
  return (
    <span
      className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${style}`}
    >
      {kind}
    </span>
  )
}

function previewFor(frame: any): string {
  switch (frame.type) {
    case 'postgres_changes': {
      const op = frame.db_event ?? '?'
      const tbl = `${frame.schema ?? '?'}.${frame.table ?? '?'}`
      return `${op} on ${tbl}`
    }
    case 'broadcast':
      return `${frame.channel ?? '?'} · ${frame.event ?? '?'}`
    case 'presence_state':
      return `${frame.channel ?? '?'} · members=${Object.keys(frame.members ?? {}).length}`
    case 'presence_diff':
      return `${frame.channel ?? '?'} · joins=${Object.keys(frame.joins ?? {}).length} leaves=${Object.keys(frame.leaves ?? {}).length}`
    case 'rpc_response':
      return `${frame.function ?? '?'} · ok=${frame.ok}`
    case 'error':
      return `${frame.code ?? '?'}: ${frame.message ?? ''}`
    case 'welcome':
      return `session ${frame.session_id?.slice(0, 8)}…`
    case 'ack':
      return frame.channel ? `ack on ${frame.channel}` : 'ack'
    case 'heartbeat':
      return 'heartbeat'
    case 'system':
      return frame.code ?? 'system'
    default:
      return frame.type
  }
}

function pretty(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}
