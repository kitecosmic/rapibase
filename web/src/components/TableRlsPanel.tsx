import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import CodeMirror from '@uiw/react-codemirror'
import { sql as sqlLang } from '@codemirror/lang-sql'
import { Lock, Unlock, Loader2 } from 'lucide-react'
import { rls, query, type RLSStatus } from '../lib/api'

const MODES = [
  {
    id: 'owner',
    title: 'Owner-based — each user only their own rows',
    desc: 'A uuid column holds the owning user id; users see and modify only rows where it equals their id. Best for per-customer data (orders, profiles, documents).',
  },
  {
    id: 'authenticated',
    title: 'Any signed-in user — all rows',
    desc: 'Every logged-in user can read and write every row. For shared internal tables. At least requires a valid login.',
  },
  {
    id: 'public',
    title: 'Public — anyone can read',
    desc: 'Anyone can read even without logging in; signed-in users can write. For public catalogs / reference data.',
  },
  {
    id: 'custom',
    title: 'Custom — write your own policies (advanced)',
    desc: 'Grants API access and turns RLS on with no built-in policy. Write your own SQL policy below. Until you add one, only the service key can read.',
  },
]

// TableRlsPanel is the per-table row-level-security configurator, shown
// in the table's "RLS" tab. Mirrors how Supabase exposes RLS: a clear
// on/off state plus a policy choice in plain language, with an advanced
// SQL escape hatch.
export default function TableRlsPanel({ table, columns }: { table: string; columns: any[] }) {
  const qc = useQueryClient()
  const { data: status, isLoading } = useQuery<RLSStatus>({
    queryKey: ['rls', table],
    queryFn: () => rls.status(table),
  })

  const [mode, setMode] = useState('owner')
  const [ownerColumn, setOwnerColumn] = useState('')
  const [err, setErr] = useState('')
  const [policySQL, setPolicySQL] = useState('')
  const [policyMsg, setPolicyMsg] = useState('')

  useEffect(() => {
    if (status?.mode) setMode(status.mode)
    if (status?.owner_column) setOwnerColumn(status.owner_column)
  }, [status])

  // Seed a starter policy when the Custom radio is selected.
  useEffect(() => {
    if (mode === 'custom' && !policySQL) {
      setPolicySQL(
        `-- Read access for any signed-in user (edit to taste):\n` +
          `CREATE POLICY ${table}_read ON ${table}\n` +
          `  FOR SELECT TO rapibase_authenticated\n` +
          `  USING (true);`
      )
    }
  }, [mode, table, policySQL])

  const enable = useMutation({
    mutationFn: () => rls.enable(table, mode, mode === 'owner' ? ownerColumn : undefined),
    onSuccess: () => { setErr(''); setPolicyMsg(''); qc.invalidateQueries({ queryKey: ['rls', table] }) },
    onError: (e: any) => setErr(e.message || 'Failed'),
  })

  const disable = useMutation({
    mutationFn: () => rls.disable(table),
    onSuccess: () => { setPolicyMsg(''); qc.invalidateQueries({ queryKey: ['rls', table] }) },
  })

  const deletePolicy = useMutation({
    mutationFn: (name: string) => query.execute(`DROP POLICY IF EXISTS "${name}" ON "${table}"`),
    onSuccess: () => { setErr(''); qc.invalidateQueries({ queryKey: ['rls', table] }) },
    onError: (e: any) => setErr(e.message || 'Failed to delete policy'),
  })

  // One button for custom: set up the base (grant + RLS on) the first
  // time, then run the policy SQL — a single clear action.
  const applyCustom = useMutation({
    mutationFn: async () => {
      if (status?.mode !== 'custom') {
        await rls.enable(table, 'custom')
      }
      if (policySQL.trim()) {
        await query.execute(policySQL)
      }
    },
    onSuccess: () => {
      setErr('')
      setPolicyMsg('Applied.')
      setPolicySQL('') // reset the add-box (re-seeds the template); the new policy now shows in the list above
      qc.invalidateQueries({ queryKey: ['rls', table] })
    },
    onError: (e: any) => { setPolicyMsg(''); setErr(e.message || 'Failed to apply') },
  })

  if (isLoading) {
    return <div className="p-8 flex justify-center text-gray-400"><Loader2 className="w-6 h-6 animate-spin" /></div>
  }

  const active = status?.mode || (status?.enabled ? 'custom' : '')
  const hasUuid = columns.some((c) => String(c.type).toLowerCase().includes('uuid'))
  const customReady = status?.mode === 'custom'

  return (
    <div className="max-w-3xl space-y-5">
      <div className={`flex items-start gap-3 p-4 rounded-lg border ${active ? 'bg-green-50 border-green-200' : 'bg-amber-50 border-amber-200'}`}>
        {active ? <Lock className="w-5 h-5 text-green-600 mt-0.5 flex-shrink-0" /> : <Unlock className="w-5 h-5 text-amber-600 mt-0.5 flex-shrink-0" />}
        <div className="text-sm text-gray-800">
          {active ? (
            <>
              <strong>Row-Level Security is ON</strong>{active !== 'custom' && <> (<code>{active}</code> mode)</>}.
              The public API enforces this policy on <code>{table}</code>.
            </>
          ) : (
            <>
              <strong>Row-Level Security is OFF.</strong> The public API (anon key + user token) <strong>cannot</strong> read
              or write <code>{table}</code> — only the service key and this dashboard can. Choose a policy below to let your
              app users in.
            </>
          )}
        </div>
      </div>

      {status?.policy_details && status.policy_details.length > 0 && (
        <div className="space-y-2">
          <div className="text-xs font-medium text-gray-700">Active policies on this table</div>
          {status.policy_details.map((p) => (
            <div key={p.name} className="flex items-start justify-between gap-3 text-xs bg-gray-50 border border-gray-100 rounded-lg p-3">
              <div className="min-w-0">
                <div className="font-mono font-medium text-gray-800 break-all">
                  {p.name} <span className="text-gray-400">· {p.command} · {p.roles}</span>
                </div>
                {p.using && <div className="text-gray-600 mt-0.5 break-all">USING <span className="font-mono">{p.using}</span></div>}
                {p.with_check && <div className="text-gray-600 break-all">WITH CHECK <span className="font-mono">{p.with_check}</span></div>}
              </div>
              <button
                onClick={() => { if (window.confirm(`Delete policy "${p.name}"?`)) deletePolicy.mutate(p.name) }}
                className="text-red-500 hover:text-red-700 flex-shrink-0"
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="space-y-2">
        {MODES.map((m) => (
          <label
            key={m.id}
            className={`flex gap-3 p-3 rounded-lg border cursor-pointer transition-colors ${
              mode === m.id ? 'border-blue-500 bg-blue-50' : 'border-gray-200 hover:bg-gray-50'
            }`}
          >
            <input type="radio" name="rls-mode" checked={mode === m.id} onChange={() => setMode(m.id)} className="mt-1" />
            <div className="min-w-0 w-full">
              <div className="font-medium text-gray-900 text-sm">{m.title}</div>
              <div className="text-xs text-gray-600 mt-0.5">{m.desc}</div>
              {m.id === 'owner' && mode === 'owner' && (
                <div className="mt-2 flex items-center gap-2 flex-wrap">
                  <span className="text-xs text-gray-600">Owner column:</span>
                  <select
                    value={ownerColumn}
                    onChange={(e) => setOwnerColumn(e.target.value)}
                    className="px-2 py-1 border border-gray-300 rounded text-sm"
                  >
                    <option value="">— select a uuid column —</option>
                    {columns.map((c) => <option key={c.name} value={c.name}>{c.name} ({c.type})</option>)}
                  </select>
                  {!hasUuid && (
                    <span className="text-xs text-amber-700">
                      No uuid column found — add one (e.g. <code>owner uuid</code>) to use owner mode.
                    </span>
                  )}
                </div>
              )}
            </div>
          </label>
        ))}
      </div>

      {/* Custom SQL editor — outside the radio labels so clicks/selection
          in the editor never toggle the radio. Shown as soon as Custom is
          selected. */}
      {mode === 'custom' && (
        <div className="border border-gray-200 rounded-lg p-4 space-y-3 bg-gray-50">
          <div className="text-sm font-medium text-gray-900">Add a policy (SQL)</div>
          <p className="text-xs text-gray-600">
            This box <strong>creates</strong> a new policy — your saved ones appear in the list above. Target the public
            role <code>rapibase_authenticated</code> (or omit <code>TO</code> to apply to all roles).
            Helpers: <code>auth.uid()</code>, <code>auth.role()</code>, <code>auth.email()</code>, <code>auth.jwt()</code>.
            Policies are additive (OR); remove one with <code>DROP POLICY name ON {table}</code>.
          </p>
          <div className="border border-gray-300 rounded-lg overflow-hidden bg-white">
            <CodeMirror
              value={policySQL}
              height="150px"
              extensions={[sqlLang()]}
              onChange={(v) => setPolicySQL(v)}
              className="text-sm"
              basicSetup={{ lineNumbers: true, bracketMatching: true, autocompletion: true }}
            />
          </div>
          <div className="flex items-center gap-3 flex-wrap">
            <button
              onClick={() => applyCustom.mutate()}
              disabled={applyCustom.isPending || (customReady && !policySQL.trim())}
              className="px-4 py-2 bg-blue-600 text-white rounded-lg text-sm font-medium hover:bg-blue-700 disabled:opacity-50 flex items-center gap-2"
            >
              {applyCustom.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
              {customReady ? 'Run policy SQL' : 'Enable custom RLS & run'}
            </button>
            {active && (
              <button
                onClick={() => disable.mutate()}
                disabled={disable.isPending}
                className="px-4 py-2 border border-gray-300 text-gray-700 rounded-lg text-sm hover:bg-gray-50"
              >
                Turn off (lock table)
              </button>
            )}
            {policyMsg && <span className="text-xs text-green-600">{policyMsg}</span>}
          </div>
          {!customReady && (
            <p className="text-xs text-gray-500">
              One click: grants API access, turns RLS on, and runs your policy.
            </p>
          )}
        </div>
      )}

      {err && <div className="text-sm text-red-600">{err}</div>}

      {/* Built-in modes use a single Apply/Turn-off action; Custom has
          its own buttons in the editor above. */}
      {mode !== 'custom' && (
        <div className="flex items-center gap-3">
          <button
            onClick={() => enable.mutate()}
            disabled={enable.isPending || (mode === 'owner' && !ownerColumn)}
            className="px-4 py-2 bg-blue-600 text-white rounded-lg text-sm font-medium hover:bg-blue-700 disabled:opacity-50 flex items-center gap-2"
          >
            {enable.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
            {active ? 'Update policy' : 'Enable & apply'}
          </button>
          {active && (
            <button
              onClick={() => disable.mutate()}
              disabled={disable.isPending}
              className="px-4 py-2 border border-gray-300 text-gray-700 rounded-lg text-sm hover:bg-gray-50 flex items-center gap-2"
            >
              {disable.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
              Turn off (lock table)
            </button>
          )}
        </div>
      )}

      <p className="text-xs text-gray-500 border-t border-gray-100 pt-3">
        The <strong>service key</strong> and this dashboard always have full access (RLS does not apply to them).
        Realtime change events for this table are scoped the same way. In owner mode, the owner column is auto-filled
        with the caller’s id on insert, so your app doesn’t have to send it.
      </p>
    </div>
  )
}
