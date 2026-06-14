import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ShieldCheck, KeyRound, ScrollText, Loader2, Check, X, Lock, Unlock } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { auth, tables, rls, accessLogs, type RLSStatus } from '../lib/api'

type Tab = 'mfa' | 'rls' | 'logs'

export default function Security() {
  const [tab, setTab] = useState<Tab>('mfa')

  const tabs: { id: Tab; label: string; icon: any }[] = [
    { id: 'mfa', label: 'Two-Factor Auth', icon: KeyRound },
    { id: 'rls', label: 'Row-Level Security', icon: ShieldCheck },
    { id: 'logs', label: 'Access Log', icon: ScrollText },
  ]

  return (
    <div className="max-w-5xl">
      <h1 className="text-2xl font-bold text-gray-900 mb-1">Security</h1>
      <p className="text-gray-600 mb-6">MFA, per-table access control and the request access log.</p>

      <div className="flex gap-1 border-b border-gray-200 mb-6">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`flex items-center gap-2 px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              tab === t.id
                ? 'border-blue-600 text-blue-700'
                : 'border-transparent text-gray-500 hover:text-gray-700'
            }`}
          >
            <t.icon className="w-4 h-4" />
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'mfa' && <MfaSection />}
      {tab === 'rls' && <RlsSection />}
      {tab === 'logs' && <AccessLogSection />}
    </div>
  )
}

// ----------------------------------------------------------------------
// MFA (TOTP)
// ----------------------------------------------------------------------

function chunk4(s: string): string {
  return (s.match(/.{1,4}/g) || []).join(' ')
}

function MfaSection() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['mfa-status'], queryFn: auth.mfaStatus })

  const [setup, setSetup] = useState<{ secret: string; otpauth_url: string } | null>(null)
  const [code, setCode] = useState('')
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')

  const [pw, setPw] = useState('')
  const [disableCode, setDisableCode] = useState('')

  const startSetup = useMutation({
    mutationFn: auth.mfaSetup,
    onSuccess: (d) => { setSetup(d); setErr(''); setMsg('') },
    onError: (e: any) => setErr(e.message || 'Failed to start setup'),
  })
  const verify = useMutation({
    mutationFn: () => auth.mfaVerify(code),
    onSuccess: () => { setSetup(null); setCode(''); setMsg('MFA enabled.'); setErr(''); qc.invalidateQueries({ queryKey: ['mfa-status'] }) },
    onError: (e: any) => setErr(e.message || 'Invalid code'),
  })
  const disable = useMutation({
    mutationFn: () => auth.mfaDisable(pw, disableCode),
    onSuccess: () => { setPw(''); setDisableCode(''); setMsg('MFA disabled.'); setErr(''); qc.invalidateQueries({ queryKey: ['mfa-status'] }) },
    onError: (e: any) => setErr(e.message || 'Failed to disable'),
  })

  if (isLoading) return <Spinner />

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-6 max-w-2xl">
      {msg && <Banner kind="ok">{msg}</Banner>}
      {err && <Banner kind="err">{err}</Banner>}

      {data?.enabled ? (
        <>
          <div className="flex items-center gap-2 text-green-700 mb-4">
            <Check className="w-5 h-5" />
            <span className="font-medium">Two-factor authentication is enabled.</span>
          </div>
          <p className="text-sm text-gray-600 mb-4">
            To turn it off, confirm your password and a current code from your authenticator app.
          </p>
          <div className="space-y-3 max-w-sm">
            <input type="password" placeholder="Password" value={pw} onChange={(e) => setPw(e.target.value)} className={inputCls} />
            <input inputMode="numeric" maxLength={6} placeholder="123456" value={disableCode}
              onChange={(e) => setDisableCode(e.target.value.replace(/\D/g, ''))} className={inputCls} />
            <button onClick={() => disable.mutate()} disabled={disable.isPending}
              className="px-4 py-2 bg-red-600 text-white rounded-lg text-sm font-medium hover:bg-red-700 disabled:opacity-50 flex items-center gap-2">
              {disable.isPending && <Loader2 className="w-4 h-4 animate-spin" />} Disable MFA
            </button>
          </div>
        </>
      ) : setup ? (
        <>
          <h3 className="font-semibold text-gray-900 mb-2">Add to your authenticator app</h3>
          <p className="text-sm text-gray-600 mb-4">
            Scan this QR with Google Authenticator, Authy, 1Password or similar — or choose
            <strong> “Enter a setup key”</strong> and type the key manually. Then enter the 6-digit code to confirm.
            Works fully offline; no Google account needed.
          </p>
          <div className="flex flex-wrap gap-6 items-start mb-4">
            <div className="bg-white border border-gray-200 rounded-lg p-3">
              <QRCodeSVG value={setup.otpauth_url} size={176} />
            </div>
            <div className="bg-gray-50 border border-gray-200 rounded-lg p-4 flex-1 min-w-[220px]">
              <div className="text-xs uppercase text-gray-500 mb-1">Or enter this setup key</div>
              <div className="font-mono text-lg tracking-wide text-gray-900 break-all select-all">{chunk4(setup.secret)}</div>
              <button onClick={() => navigator.clipboard?.writeText(setup.secret)} className="mt-2 text-xs text-blue-600 hover:text-blue-700">
                Copy key
              </button>
            </div>
          </div>
          <div className="flex items-center gap-2 max-w-sm">
            <input inputMode="numeric" maxLength={6} autoFocus placeholder="123456" value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} className={inputCls} />
            <button onClick={() => verify.mutate()} disabled={verify.isPending || code.length !== 6}
              className="px-4 py-2 bg-blue-600 text-white rounded-lg text-sm font-medium hover:bg-blue-700 disabled:opacity-50 whitespace-nowrap flex items-center gap-2">
              {verify.isPending && <Loader2 className="w-4 h-4 animate-spin" />} Verify & enable
            </button>
          </div>
          <button onClick={() => { setSetup(null); setErr('') }} className="mt-3 text-sm text-gray-500 hover:text-gray-700">Cancel</button>
        </>
      ) : (
        <>
          <div className="flex items-center gap-2 text-gray-700 mb-3">
            <KeyRound className="w-5 h-5 text-gray-400" />
            <span className="font-medium">Two-factor authentication is off.</span>
          </div>
          <p className="text-sm text-gray-600 mb-4">
            Require a one-time code from an authenticator app at every dashboard login. Works fully offline — no
            Google account, API keys or external service.
          </p>
          <button onClick={() => startSetup.mutate()} disabled={startSetup.isPending}
            className="px-4 py-2 bg-blue-600 text-white rounded-lg text-sm font-medium hover:bg-blue-700 disabled:opacity-50 flex items-center gap-2">
            {startSetup.isPending && <Loader2 className="w-4 h-4 animate-spin" />} Enable MFA
          </button>
        </>
      )}
    </div>
  )
}

// ----------------------------------------------------------------------
// Row-Level Security
// ----------------------------------------------------------------------

function RlsSection() {
  const { data, isLoading } = useQuery({ queryKey: ['tables'], queryFn: tables.list })
  if (isLoading) return <Spinner />

  const list = data?.tables || []
  return (
    <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
      <div className="p-4 border-b border-gray-200 bg-amber-50 text-sm text-amber-800">
        Tables without a policy are <strong>not</strong> readable through the public API (anon key + user token) —
        only the service key and this admin panel see them. Enable a mode per table to grant scoped access.
      </div>
      {list.length === 0 ? (
        <div className="p-6 text-gray-500 text-sm">No user tables yet.</div>
      ) : (
        <div className="divide-y divide-gray-100">
          {list.map((t: any) => <RlsRow key={t.name} table={t.name} />)}
        </div>
      )}
    </div>
  )
}

function RlsRow({ table }: { table: string }) {
  const qc = useQueryClient()
  const { data: status } = useQuery<RLSStatus>({ queryKey: ['rls', table], queryFn: () => rls.status(table) })
  const [editing, setEditing] = useState(false)
  const [mode, setMode] = useState('owner')
  const [ownerColumn, setOwnerColumn] = useState('')
  const [err, setErr] = useState('')

  // Columns for the owner-column picker (only fetched while editing).
  const { data: schema } = useQuery({
    queryKey: ['table-schema', table],
    queryFn: () => tables.get(table),
    enabled: editing,
  })

  const enable = useMutation({
    mutationFn: () => rls.enable(table, mode, mode === 'owner' ? ownerColumn : undefined),
    onSuccess: () => { setEditing(false); setErr(''); qc.invalidateQueries({ queryKey: ['rls', table] }) },
    onError: (e: any) => setErr(e.message || 'Failed'),
  })
  const disable = useMutation({
    mutationFn: () => rls.disable(table),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rls', table] }),
  })

  const current = status?.mode || (status?.enabled ? 'custom' : '')

  return (
    <div className="p-4">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0">
          {current ? <Lock className="w-4 h-4 text-green-600 flex-shrink-0" /> : <Unlock className="w-4 h-4 text-gray-300 flex-shrink-0" />}
          <span className="font-medium text-gray-900 truncate">{table}</span>
          {current
            ? <span className="text-xs px-2 py-0.5 rounded-full bg-green-50 text-green-700">{current}</span>
            : <span className="text-xs px-2 py-0.5 rounded-full bg-gray-100 text-gray-500">locked (no public access)</span>}
        </div>
        <div className="flex items-center gap-2">
          {current && (
            <button onClick={() => disable.mutate()} disabled={disable.isPending}
              className="text-sm text-gray-500 hover:text-red-600">Disable</button>
          )}
          <button onClick={() => { setEditing(!editing); setErr(''); setOwnerColumn(status?.owner_column || '') }}
            className="text-sm text-blue-600 hover:text-blue-700">{editing ? 'Close' : 'Configure'}</button>
        </div>
      </div>

      {editing && (
        <div className="mt-3 pl-7 space-y-3">
          {err && <Banner kind="err">{err}</Banner>}
          <div className="flex flex-wrap items-end gap-3">
            <label className="text-sm">
              <span className="block text-gray-600 mb-1">Mode</span>
              <select value={mode} onChange={(e) => setMode(e.target.value)} className={inputCls}>
                <option value="owner">owner — each user only their rows</option>
                <option value="authenticated">authenticated — any logged-in user</option>
                <option value="public">public — anyone can read</option>
              </select>
            </label>
            {mode === 'owner' && (
              <label className="text-sm">
                <span className="block text-gray-600 mb-1">Owner column (uuid)</span>
                <select value={ownerColumn} onChange={(e) => setOwnerColumn(e.target.value)} className={inputCls}>
                  <option value="">— select —</option>
                  {(schema?.columns || []).map((c: any) => (
                    <option key={c.name} value={c.name}>{c.name} {c.type ? `(${c.type})` : ''}</option>
                  ))}
                </select>
              </label>
            )}
            <button onClick={() => enable.mutate()} disabled={enable.isPending || (mode === 'owner' && !ownerColumn)}
              className="px-4 py-2 bg-blue-600 text-white rounded-lg text-sm font-medium hover:bg-blue-700 disabled:opacity-50 flex items-center gap-2">
              {enable.isPending && <Loader2 className="w-4 h-4 animate-spin" />} Apply
            </button>
          </div>
          {mode === 'owner' && (
            <p className="text-xs text-gray-500">
              The owner column is defaulted to the caller’s id on insert, so apps don’t have to send it.
            </p>
          )}
        </div>
      )}
    </div>
  )
}

// ----------------------------------------------------------------------
// Access log
// ----------------------------------------------------------------------

function AccessLogSection() {
  const [ip, setIp] = useState('')
  const [applied, setApplied] = useState('')
  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['access-logs', applied],
    queryFn: () => accessLogs.list(200, 0, applied || undefined),
  })

  const logs = data?.logs || []
  return (
    <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
      <div className="p-4 border-b border-gray-200 flex items-center gap-2">
        <input placeholder="Filter by IP (exact)" value={ip} onChange={(e) => setIp(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && setApplied(ip)} className={inputCls} />
        <button onClick={() => setApplied(ip)} className="px-3 py-2 bg-gray-900 text-white rounded-lg text-sm">Filter</button>
        {applied && <button onClick={() => { setIp(''); setApplied('') }} className="text-sm text-gray-500 hover:text-gray-700">Clear</button>}
        <button onClick={() => refetch()} className="ml-auto text-sm text-blue-600 hover:text-blue-700">
          {isFetching ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      {isLoading ? <Spinner /> : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-gray-500 text-left">
              <tr>
                <Th>Time</Th><Th>IP</Th><Th>Method</Th><Th>Path</Th><Th>Status</Th><Th>Key</Th><Th>User</Th><Th>ms</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {logs.length === 0 ? (
                <tr><td colSpan={8} className="p-6 text-center text-gray-400">No entries.</td></tr>
              ) : logs.map((l: any) => (
                <tr key={l.id} className="hover:bg-gray-50">
                  <Td>{l.ts ? new Date(l.ts).toLocaleString() : '—'}</Td>
                  <Td className="font-mono">{l.ip || '—'}</Td>
                  <Td>{l.method}</Td>
                  <Td className="font-mono max-w-xs truncate" title={l.path}>{l.path}</Td>
                  <Td><StatusBadge code={l.status} /></Td>
                  <Td>{l.key_type || '—'}</Td>
                  <Td className="font-mono text-xs">{l.user_id ? `${l.user_id.slice(0, 8)}… (${l.user_role || '?'})` : '—'}</Td>
                  <Td>{l.latency_ms}</Td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// ----------------------------------------------------------------------
// Small shared bits
// ----------------------------------------------------------------------

const inputCls = 'px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none'

function Spinner() {
  return <div className="p-8 flex justify-center text-gray-400"><Loader2 className="w-6 h-6 animate-spin" /></div>
}

function Banner({ kind, children }: { kind: 'ok' | 'err'; children: React.ReactNode }) {
  const cls = kind === 'ok'
    ? 'bg-green-50 border-green-200 text-green-700'
    : 'bg-red-50 border-red-200 text-red-700'
  return (
    <div className={`flex items-center gap-2 p-3 mb-4 rounded-lg border text-sm ${cls}`}>
      {kind === 'ok' ? <Check className="w-4 h-4" /> : <X className="w-4 h-4" />}
      {children}
    </div>
  )
}

function Th({ children }: { children: React.ReactNode }) {
  return <th className="px-3 py-2 font-medium whitespace-nowrap">{children}</th>
}
function Td({ children, className = '', title }: { children: React.ReactNode; className?: string; title?: string }) {
  return <td className={`px-3 py-2 text-gray-700 ${className}`} title={title}>{children}</td>
}
function StatusBadge({ code }: { code: number }) {
  const color = code >= 500 ? 'bg-red-100 text-red-700'
    : code >= 400 ? 'bg-amber-100 text-amber-700'
    : 'bg-green-100 text-green-700'
  return <span className={`px-2 py-0.5 rounded-full text-xs font-medium ${color}`}>{code}</span>
}
