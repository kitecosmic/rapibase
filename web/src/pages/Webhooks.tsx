import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { 
  Webhook, 
  Plus, 
  Trash2, 
  Edit2, 
  ToggleLeft, 
  ToggleRight,
  CheckCircle,
  XCircle,
  Clock,
  Loader2,
  X,
  ChevronDown,
  ChevronUp
} from 'lucide-react'
import { webhooks } from '../lib/api'

interface FilterCond {
  column: string
  op: string
  value?: string
}

interface WebhookData {
  id: number
  name: string
  url: string
  secret: string
  events: string[]
  headers: Record<string, string>
  filter?: FilterCond[]
  enabled: boolean
  created_at: string
  updated_at: string
}

interface WebhookLog {
  id: number
  webhook_id: number
  webhook_name: string
  event: string
  payload: string
  response_status: number
  response_body: string
  attempts: number
  success: boolean
  error: string
  created_at: string
}

export default function Webhooks() {
  const queryClient = useQueryClient()
  const [showModal, setShowModal] = useState(false)
  const [editingWebhook, setEditingWebhook] = useState<WebhookData | null>(null)
  const [showLogs, setShowLogs] = useState(false)
  const [expandedLog, setExpandedLog] = useState<number | null>(null)

  const { data: webhooksData, isLoading } = useQuery({
    queryKey: ['webhooks'],
    queryFn: webhooks.list,
  })

  const { data: eventsData } = useQuery({
    queryKey: ['webhook-events'],
    queryFn: webhooks.getEvents,
  })

  const { data: logsData, isLoading: logsLoading } = useQuery({
    queryKey: ['webhook-logs'],
    queryFn: () => webhooks.getLogs(),
    enabled: showLogs,
  })

  const createMutation = useMutation({
    mutationFn: webhooks.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['webhooks'] })
      setShowModal(false)
      setEditingWebhook(null)
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: any }) => webhooks.update(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['webhooks'] })
      setShowModal(false)
      setEditingWebhook(null)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: webhooks.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['webhooks'] })
    },
  })

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => webhooks.toggle(id, enabled),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['webhooks'] })
    },
  })

  const webhookList: WebhookData[] = webhooksData?.webhooks || []
  const availableEvents: string[] = eventsData?.events || []
  const logs: WebhookLog[] = logsData?.logs || []

  return (
    <div>
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Webhooks</h1>
          <p className="text-gray-600 mt-1">Automate workflows with HTTP callbacks</p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => setShowLogs(!showLogs)}
            className={`px-4 py-2 rounded-lg border transition-colors ${
              showLogs 
                ? 'bg-gray-100 border-gray-300 text-gray-700' 
                : 'border-gray-200 text-gray-600 hover:bg-gray-50'
            }`}
          >
            <Clock className="w-4 h-4 inline mr-2" />
            Logs
          </button>
          <button
            onClick={() => {
              setEditingWebhook(null)
              setShowModal(true)
            }}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
          >
            <Plus className="w-4 h-4" />
            New Webhook
          </button>
        </div>
      </div>

      {/* Webhooks List */}
      {!showLogs && (
        <div className="bg-white rounded-xl border border-gray-200">
          {isLoading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
            </div>
          ) : webhookList.length === 0 ? (
            <div className="text-center py-12">
              <Webhook className="w-12 h-12 text-gray-400 mx-auto mb-4" />
              <h3 className="text-lg font-medium text-gray-900 mb-2">No webhooks yet</h3>
              <p className="text-gray-600 mb-4">Create your first webhook to start automating</p>
              <button
                onClick={() => setShowModal(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
              >
                <Plus className="w-4 h-4" />
                Create Webhook
              </button>
            </div>
          ) : (
            <div className="divide-y divide-gray-200">
              {webhookList.map((webhook) => (
                <div key={webhook.id} className="p-4 hover:bg-gray-50">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-4">
                      <button
                        onClick={() => toggleMutation.mutate({ id: webhook.id, enabled: !webhook.enabled })}
                        className={`transition-colors ${webhook.enabled ? 'text-green-600' : 'text-gray-400'}`}
                      >
                        {webhook.enabled ? (
                          <ToggleRight className="w-8 h-8" />
                        ) : (
                          <ToggleLeft className="w-8 h-8" />
                        )}
                      </button>
                      <div>
                        <h3 className="font-medium text-gray-900">{webhook.name}</h3>
                        <p className="text-sm text-gray-500 truncate max-w-md">{webhook.url}</p>
                      </div>
                    </div>
                    <div className="flex items-center gap-2">
                      <div className="flex flex-wrap gap-1 max-w-xs">
                        {webhook.events.slice(0, 3).map((event) => (
                          <span
                            key={event}
                            className="px-2 py-0.5 text-xs bg-blue-100 text-blue-700 rounded"
                          >
                            {event}
                          </span>
                        ))}
                        {webhook.events.length > 3 && (
                          <span className="px-2 py-0.5 text-xs bg-gray-100 text-gray-600 rounded">
                            +{webhook.events.length - 3}
                          </span>
                        )}
                      </div>
                      <button
                        onClick={() => {
                          setEditingWebhook(webhook)
                          setShowModal(true)
                        }}
                        className="p-2 text-gray-500 hover:text-blue-600 transition-colors"
                      >
                        <Edit2 className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => {
                          if (confirm('Are you sure you want to delete this webhook?')) {
                            deleteMutation.mutate(webhook.id)
                          }
                        }}
                        className="p-2 text-gray-500 hover:text-red-600 transition-colors"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Logs View */}
      {showLogs && (
        <div className="bg-white rounded-xl border border-gray-200">
          <div className="px-6 py-4 border-b border-gray-200">
            <h2 className="text-lg font-semibold text-gray-900">Delivery Logs</h2>
          </div>
          {logsLoading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
            </div>
          ) : logs.length === 0 ? (
            <div className="text-center py-12 text-gray-500">
              No delivery logs yet
            </div>
          ) : (
            <div className="divide-y divide-gray-200">
              {logs.map((log) => (
                <div key={log.id} className="p-4">
                  <div 
                    className="flex items-center justify-between cursor-pointer"
                    onClick={() => setExpandedLog(expandedLog === log.id ? null : log.id)}
                  >
                    <div className="flex items-center gap-3">
                      {log.success ? (
                        <CheckCircle className="w-5 h-5 text-green-600" />
                      ) : (
                        <XCircle className="w-5 h-5 text-red-600" />
                      )}
                      <div>
                        <span className="font-medium text-gray-900">{log.webhook_name}</span>
                        <span className="mx-2 text-gray-400">→</span>
                        <span className="text-sm text-blue-600">{log.event}</span>
                      </div>
                    </div>
                    <div className="flex items-center gap-4">
                      <span className={`text-sm ${log.success ? 'text-green-600' : 'text-red-600'}`}>
                        {log.response_status || 'Error'}
                      </span>
                      <span className="text-sm text-gray-500">
                        {new Date(log.created_at).toLocaleString()}
                      </span>
                      {expandedLog === log.id ? (
                        <ChevronUp className="w-4 h-4 text-gray-400" />
                      ) : (
                        <ChevronDown className="w-4 h-4 text-gray-400" />
                      )}
                    </div>
                  </div>
                  {expandedLog === log.id && (
                    <div className="mt-4 space-y-3">
                      <div>
                        <label className="text-xs font-medium text-gray-500 uppercase">Payload</label>
                        <pre className="mt-1 p-3 bg-gray-50 rounded text-xs overflow-x-auto">
                          {JSON.stringify(JSON.parse(log.payload || '{}'), null, 2)}
                        </pre>
                      </div>
                      {log.response_body && (
                        <div>
                          <label className="text-xs font-medium text-gray-500 uppercase">Response</label>
                          <pre className="mt-1 p-3 bg-gray-50 rounded text-xs overflow-x-auto">
                            {log.response_body}
                          </pre>
                        </div>
                      )}
                      {log.error && (
                        <div>
                          <label className="text-xs font-medium text-gray-500 uppercase">Error</label>
                          <p className="mt-1 text-sm text-red-600">{log.error}</p>
                        </div>
                      )}
                      <div className="text-xs text-gray-500">
                        Attempts: {log.attempts}
                      </div>
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Create/Edit Modal */}
      {showModal && (
        <WebhookModal
          webhook={editingWebhook}
          events={availableEvents}
          onClose={() => {
            setShowModal(false)
            setEditingWebhook(null)
          }}
          onSave={(data) => {
            if (editingWebhook) {
              updateMutation.mutate({ id: editingWebhook.id, data })
            } else {
              createMutation.mutate(data)
            }
          }}
          isLoading={createMutation.isPending || updateMutation.isPending}
        />
      )}
    </div>
  )
}

interface WebhookModalProps {
  webhook: WebhookData | null
  events: string[]
  onClose: () => void
  onSave: (data: any) => void
  isLoading: boolean
}

const FILTER_OPS: [string, string][] = [
  ['eq', '='],
  ['neq', '≠'],
  ['gt', '>'],
  ['gte', '≥'],
  ['lt', '<'],
  ['lte', '≤'],
  ['contains', 'contains'],
  ['is_null', 'is null'],
  ['not_null', 'is not null'],
]

function WebhookModal({ webhook, events, onClose, onSave, isLoading }: WebhookModalProps) {
  const [name, setName] = useState(webhook?.name || '')
  const [url, setUrl] = useState(webhook?.url || '')
  const [secret, setSecret] = useState('')
  const [selectedEvents, setSelectedEvents] = useState<string[]>(webhook?.events || [])
  const [enabled, setEnabled] = useState(webhook?.enabled ?? true)
  const [filter, setFilter] = useState<FilterCond[]>(webhook?.filter || [])

  // builder de eventos: una tabla + los tipos marcados → chips
  const tables = [...new Set(events.map((e) => e.split(':')[1]).filter((t) => t && t !== '*'))].sort()
  const [pickTable, setPickTable] = useState('*')
  const [pickTypes, setPickTypes] = useState<string[]>(['INSERT', 'UPDATE', 'DELETE'])

  const addEvents = () => {
    if (pickTypes.length === 0) return
    const added = pickTypes.map((t) => `${t}:${pickTable}`)
    setSelectedEvents((prev) => [...new Set([...prev, ...added])])
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    onSave({
      name,
      url,
      secret: secret || undefined,
      events: selectedEvents,
      filter: filter.filter((c) => c.column && c.op),
      enabled,
    })
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-xl w-full max-w-lg max-h-[90vh] overflow-y-auto">
        <div className="flex items-center justify-between p-4 border-b border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900">
            {webhook ? 'Edit Webhook' : 'New Webhook'}
          </h2>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-700">
            <X className="w-5 h-5" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="p-4 space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="My Webhook"
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">URL</label>
            <input
              type="url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://example.com/webhook"
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Secret (optional)
              <span className="text-gray-500 font-normal ml-1">- for signature verification</span>
            </label>
            <input
              type="password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              placeholder={webhook ? '••••••••' : 'Enter a secret key'}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-2">Events</label>
            <div className="flex gap-2 items-center flex-wrap">
              <select
                value={pickTable}
                onChange={(e) => setPickTable(e.target.value)}
                className="px-3 py-2 border border-gray-300 rounded-lg bg-white text-sm"
              >
                <option value="*">All tables (*)</option>
                {tables.map((t) => (
                  <option key={t} value={t}>{t}</option>
                ))}
              </select>
              {['INSERT', 'UPDATE', 'DELETE'].map((type) => (
                <label key={type} className="flex items-center gap-1.5 text-xs text-gray-700 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={pickTypes.includes(type)}
                    onChange={() =>
                      setPickTypes((prev) =>
                        prev.includes(type) ? prev.filter((t) => t !== type) : [...prev, type]
                      )
                    }
                    className="w-3.5 h-3.5"
                  />
                  {type}
                </label>
              ))}
              <button
                type="button"
                onClick={addEvents}
                disabled={pickTypes.length === 0}
                className="px-3 py-1.5 text-xs font-medium bg-gray-100 hover:bg-gray-200 border border-gray-300 rounded-lg disabled:opacity-50 transition-colors"
              >
                + Add
              </button>
            </div>
            {selectedEvents.length > 0 ? (
              <div className="flex flex-wrap gap-1.5 mt-3">
                {[...selectedEvents].sort().map((event) => (
                  <span
                    key={event}
                    className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded border bg-blue-100 border-blue-300 text-blue-700"
                  >
                    {event}
                    <button
                      type="button"
                      onClick={() => setSelectedEvents((prev) => prev.filter((e) => e !== event))}
                      className="hover:text-blue-900"
                      title="Remove"
                    >
                      <X className="w-3 h-3" />
                    </button>
                  </span>
                ))}
              </div>
            ) : (
              <p className="text-xs text-gray-500 mt-2">
                Pick a table and the events you want, then press Add. <code>*</code> means every table.
              </p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Conditions
              <span className="text-gray-500 font-normal ml-1">
                - optional; deliver only when the row matches ALL of them
              </span>
            </label>
            <div className="space-y-2">
              {filter.map((cond, i) => (
                <div key={i} className="flex gap-2 items-center">
                  <input
                    type="text"
                    value={cond.column}
                    onChange={(e) => setFilter((prev) => prev.map((c, j) => (j === i ? { ...c, column: e.target.value } : c)))}
                    placeholder="column"
                    className="flex-1 min-w-0 px-3 py-1.5 border border-gray-300 rounded-lg text-sm font-mono"
                  />
                  <select
                    value={cond.op}
                    onChange={(e) => setFilter((prev) => prev.map((c, j) => (j === i ? { ...c, op: e.target.value } : c)))}
                    className="px-2 py-1.5 border border-gray-300 rounded-lg bg-white text-sm"
                  >
                    {FILTER_OPS.map(([op, label]) => (
                      <option key={op} value={op}>{label}</option>
                    ))}
                  </select>
                  {cond.op !== 'is_null' && cond.op !== 'not_null' && (
                    <input
                      type="text"
                      value={cond.value ?? ''}
                      onChange={(e) => setFilter((prev) => prev.map((c, j) => (j === i ? { ...c, value: e.target.value } : c)))}
                      placeholder="value"
                      className="flex-1 min-w-0 px-3 py-1.5 border border-gray-300 rounded-lg text-sm"
                    />
                  )}
                  <button
                    type="button"
                    onClick={() => setFilter((prev) => prev.filter((_, j) => j !== i))}
                    className="text-gray-400 hover:text-red-600 transition-colors"
                    title="Remove condition"
                  >
                    <X className="w-4 h-4" />
                  </button>
                </div>
              ))}
              <button
                type="button"
                onClick={() => setFilter((prev) => [...prev, { column: '', op: 'eq', value: '' }])}
                className="text-xs font-medium text-blue-600 hover:text-blue-700"
              >
                + Add condition
              </button>
              {filter.length > 0 && (
                <p className="text-xs text-gray-500">
                  Conditions are checked against the row data (for DELETE, the deleted row) — e.g.{' '}
                  <code>status = paid</code> or <code>total &gt; 100</code>. Numbers compare numerically.
                </p>
              )}
            </div>
          </div>

          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="enabled"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              className="w-4 h-4 text-blue-600 rounded"
            />
            <label htmlFor="enabled" className="text-sm text-gray-700">
              Enable webhook
            </label>
          </div>

          <div className="flex justify-end gap-2 pt-4">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isLoading || !name || !url || selectedEvents.length === 0}
              className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {isLoading ? (
                <Loader2 className="w-4 h-4 animate-spin" />
              ) : webhook ? (
                'Update'
              ) : (
                'Create'
              )}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
