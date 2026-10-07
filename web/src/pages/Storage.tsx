import { useState, useCallback, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { storage, type StorageBucket, type StorageEntry, type BucketSettings, type AccessRule, type RuleOperation } from '../lib/api'
import {
  HardDrive,
  Plus,
  Trash2,
  Loader2,
  X,
  Upload,
  File,
  Folder,
  FolderPlus,
  Image,
  FileText,
  Film,
  Music,
  Archive,
  ChevronRight,
  ChevronLeft,
  Eye,
  Download,
  Link,
  Lock,
  Globe,
  Settings,
  AlertTriangle,
  Check,
  Shield,
} from 'lucide-react'

const formatBytes = (bytes: number): string => {
  if (!bytes) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

const formatDate = (dateString: string): string =>
  new Date(dateString).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })

const getFileIcon = (contentType: string, isDir: boolean) => {
  if (isDir) return Folder
  if (contentType.startsWith('image/')) return Image
  if (contentType.startsWith('video/')) return Film
  if (contentType.startsWith('audio/')) return Music
  if (contentType.includes('zip') || contentType.includes('tar') || contentType.includes('gzip')) return Archive
  if (contentType.includes('text') || contentType.includes('json') || contentType.includes('xml')) return FileText
  return File
}

// SVG is excluded on purpose: it is served sandboxed and isn't worth a thumbnail.
const isPreviewableImage = (ct: string) => ct.startsWith('image/') && ct !== 'image/svg+xml'

const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e))

const LINK_EXPIRIES: [number, string][] = [
  [60, '1 hour'],
  [60 * 24, '1 day'],
  [60 * 24 * 7, '7 days'],
]

// ---- bucket settings form (create + edit) --------------------------------

interface SettingsDraft {
  public: boolean
  ownerAccess: boolean
  maxSizeMB: string
  mimeTypes: string
}

const draftFrom = (b?: StorageBucket): SettingsDraft => ({
  public: b?.public ?? false,
  ownerAccess: b?.owner_access ?? true,
  maxSizeMB: b?.file_size_limit ? String(+(b.file_size_limit / 1024 / 1024).toFixed(2)) : '',
  mimeTypes: (b?.allowed_mime_types ?? []).join(', '),
})

const settingsFrom = (d: SettingsDraft): BucketSettings => ({
  public: d.public,
  owner_access: d.ownerAccess,
  file_size_limit: d.maxSizeMB.trim() ? Math.round(parseFloat(d.maxSizeMB) * 1024 * 1024) : 0,
  allowed_mime_types: d.mimeTypes
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean),
})

function BucketSettingsFields({ draft, onChange }: { draft: SettingsDraft; onChange: (d: SettingsDraft) => void }) {
  return (
    <div className="space-y-4">
      <div>
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={draft.public}
            onChange={(e) => onChange({ ...draft, public: e.target.checked })}
            className="rounded"
          />
          <span className="text-sm font-medium text-gray-700">Public bucket</span>
        </label>
        <p className="mt-1 text-xs text-gray-500 ml-6">
          Anyone with a file's public URL can read it, without keys. Uploads and deletes still need authorization.
        </p>
        {draft.public && (
          <p className="mt-2 ml-6 flex items-start gap-1.5 text-xs text-amber-700">
            <AlertTriangle className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
            Don't store private documents here. Use a private bucket and signed URLs for those.
          </p>
        )}
      </div>
      <div>
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={draft.ownerAccess}
            onChange={(e) => onChange({ ...draft, ownerAccess: e.target.checked })}
            className="rounded"
          />
          <span className="text-sm font-medium text-gray-700">Files belong to their uploader</span>
        </label>
        <p className="mt-1 text-xs text-gray-500 ml-6">
          A logged-in user can upload files that are theirs, and read, replace and delete their own. Turn it off to rely only on
          this bucket's access rules.
        </p>
      </div>
      <div>
        <label className="block text-sm font-medium text-gray-700 mb-1">Max file size (MB)</label>
        <input
          type="number"
          min="0"
          step="any"
          value={draft.maxSizeMB}
          onChange={(e) => onChange({ ...draft, maxSizeMB: e.target.value })}
          className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
          placeholder="No limit"
        />
      </div>
      <div>
        <label className="block text-sm font-medium text-gray-700 mb-1">Allowed file types</label>
        <input
          type="text"
          value={draft.mimeTypes}
          onChange={(e) => onChange({ ...draft, mimeTypes: e.target.value })}
          className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none font-mono text-sm"
          placeholder="image/*, application/pdf"
        />
        <p className="mt-1 text-xs text-gray-500">Comma separated MIME types; wildcards like image/* work. Empty allows any type.</p>
      </div>
    </div>
  )
}

// ---- access rules ----------------------------------------------------------

const OPERATIONS: [RuleOperation, string][] = [
  ['read', 'Read'],
  ['upload', 'Upload'],
  ['update', 'Replace / edit'],
  ['delete', 'Delete'],
  ['all', 'Everything'],
]

const RULE_EXAMPLES: { label: string; operation: RuleOperation; condition: string }[] = [
  {
    label: 'Team members, folder per team',
    operation: 'read',
    condition: 'EXISTS (SELECT 1 FROM team_members m WHERE m.user_id = auth.uid() AND m.team_id::text = (storage.foldername(name))[1])',
  },
  { label: 'Each user only in their own folder', operation: 'upload', condition: '(storage.foldername(name))[1] = auth.uid()::text' },
  { label: 'Any logged-in user', operation: 'read', condition: 'auth.uid() IS NOT NULL' },
  { label: 'Only some extensions', operation: 'upload', condition: "storage.extension(name) IN ('png', 'jpg', 'pdf')" },
]

function BucketRules({ bucket }: { bucket: string }) {
  const queryClient = useQueryClient()
  const [adding, setAdding] = useState(false)
  const [rule, setRule] = useState<AccessRule>({ name: '', operation: 'read', condition: '', restrictive: false })
  const [error, setError] = useState('')
  const { data, isLoading } = useQuery({ queryKey: ['storage-rules', bucket], queryFn: () => storage.listRules(bucket) })
  const rules = (data?.rules ?? []).filter((r) => !r.builtin)
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['storage-rules', bucket] })

  const save = useMutation({
    mutationFn: () => storage.setRule({ ...rule, bucket }),
    onSuccess: () => {
      refresh()
      setAdding(false)
      setError('')
      setRule({ name: '', operation: 'read', condition: '', restrictive: false })
    },
    onError: (e) => setError(errorText(e)),
  })
  const remove = useMutation({ mutationFn: (name: string) => storage.deleteRule(name), onSuccess: refresh, onError: (e) => setError(errorText(e)) })

  return (
    <div className="mt-6 border-t border-gray-200 pt-5">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold text-gray-900 flex items-center gap-1.5">
          <Shield className="w-4 h-4" />
          Access rules
        </h3>
        {!adding && (
          <button onClick={() => setAdding(true)} className="flex items-center gap-1 text-sm text-blue-600 hover:text-blue-700">
            <Plus className="w-4 h-4" />
            Add rule
          </button>
        )}
      </div>
      <p className="text-xs text-gray-500 mb-3">
        SQL conditions checked for each file, as the app user (anon key + JWT). Use <code>auth.uid()</code>,{' '}
        <code>storage.foldername(name)</code>, the file's <code>owner</code>, <code>size</code>, <code>mime_type</code>,{' '}
        <code>metadata</code>, and any table the user can read. The service key always has full access.
      </p>

      {isLoading ? (
        <Loader2 className="w-4 h-4 animate-spin text-blue-600" />
      ) : rules.length === 0 && !adding ? (
        <p className="text-xs text-gray-500 italic">No custom rules: only public access and the uploader's own files apply.</p>
      ) : (
        <ul className="space-y-2">
          {rules.map((r) => (
            <li key={r.name} className="rounded-lg border border-gray-200 p-3">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 min-w-0">
                  <span className="text-sm font-medium text-gray-900 truncate">{r.name}</span>
                  <span className="text-xs px-1.5 py-0.5 rounded bg-gray-100 text-gray-700">{OPERATIONS.find(([o]) => o === r.operation)?.[1]}</span>
                  {r.restrictive && <span className="text-xs px-1.5 py-0.5 rounded bg-amber-50 text-amber-700">restrictive</span>}
                  {!r.bucket && <span className="text-xs px-1.5 py-0.5 rounded bg-gray-100 text-gray-500">all buckets</span>}
                </div>
                <button
                  onClick={() => confirm(`Delete rule "${r.name}"?`) && remove.mutate(r.name)}
                  className="p-1 text-gray-400 hover:text-red-600"
                  title="Delete rule"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
              <code className="block mt-1.5 text-xs text-gray-600 break-all">{r.condition}</code>
            </li>
          ))}
        </ul>
      )}

      {adding && (
        <div className="mt-3 rounded-lg border border-gray-200 p-3 space-y-3">
          <div className="flex gap-2">
            <input
              value={rule.name}
              onChange={(e) => setRule({ ...rule, name: e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, '') })}
              placeholder="rule_name"
              className="flex-1 min-w-0 px-3 py-2 text-sm border border-gray-300 rounded-lg outline-none focus:ring-2 focus:ring-blue-500"
            />
            <select
              value={rule.operation}
              onChange={(e) => setRule({ ...rule, operation: e.target.value as RuleOperation })}
              className="px-3 py-2 text-sm border border-gray-300 rounded-lg outline-none"
            >
              {OPERATIONS.map(([o, label]) => (
                <option key={o} value={o}>
                  {label}
                </option>
              ))}
            </select>
          </div>
          <textarea
            value={rule.condition}
            onChange={(e) => setRule({ ...rule, condition: e.target.value })}
            rows={3}
            placeholder="SQL condition, e.g. owner = auth.uid()"
            className="w-full px-3 py-2 text-xs font-mono border border-gray-300 rounded-lg outline-none focus:ring-2 focus:ring-blue-500"
          />
          <div className="flex flex-wrap gap-1.5">
            {RULE_EXAMPLES.map((ex) => (
              <button
                key={ex.label}
                onClick={() => setRule({ ...rule, operation: ex.operation, condition: ex.condition })}
                className="text-xs px-2 py-1 rounded border border-gray-200 text-gray-600 hover:bg-gray-50"
              >
                {ex.label}
              </button>
            ))}
          </div>
          <label className="flex items-start gap-2">
            <input
              type="checkbox"
              checked={rule.restrictive}
              onChange={(e) => setRule({ ...rule, restrictive: e.target.checked })}
              className="rounded mt-0.5"
            />
            <span className="text-xs text-gray-600">
              <strong>Restrictive</strong>: an extra requirement every access must also meet (narrows access). Off: the rule grants access.
            </span>
          </label>
          <div className="flex justify-end gap-2">
            <button onClick={() => setAdding(false)} className="px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-100 rounded-lg">
              Cancel
            </button>
            <button
              onClick={() => save.mutate()}
              disabled={!rule.name || !rule.condition.trim() || save.isPending}
              className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50"
            >
              {save.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
              Save rule
            </button>
          </div>
        </div>
      )}
      {error && <p className="mt-2 text-xs text-red-600">{error}</p>}
    </div>
  )
}

function Modal({
  title,
  onClose,
  children,
  footer,
  wide = false,
}: {
  title: string
  onClose: () => void
  children: React.ReactNode
  footer: React.ReactNode
  wide?: boolean
}) {
  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4" onClick={onClose}>
      <div
        className={`bg-white rounded-xl shadow-xl w-full ${wide ? 'max-w-2xl' : 'max-w-md'} max-h-[92vh] flex flex-col`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-700">
            <X className="w-5 h-5" />
          </button>
        </div>
        <div className="p-6 overflow-y-auto">{children}</div>
        <div className="flex justify-end gap-3 px-6 py-4 border-t border-gray-200">{footer}</div>
      </div>
    </div>
  )
}

// ---- page ------------------------------------------------------------------

export default function Storage() {
  const queryClient = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)

  const [selectedBucket, setSelectedBucket] = useState<string | null>(null)
  const [currentPath, setCurrentPath] = useState<string>('')
  const [showCreateBucket, setShowCreateBucket] = useState(false)
  const [showSettings, setShowSettings] = useState(false)
  const [showCreateFolder, setShowCreateFolder] = useState(false)
  const [newBucketName, setNewBucketName] = useState('')
  const [draft, setDraft] = useState<SettingsDraft>(draftFrom())
  const [newFolderName, setNewFolderName] = useState('')
  const [preview, setPreview] = useState<StorageEntry | null>(null)
  const [linkFor, setLinkFor] = useState<StorageEntry | null>(null)
  const [copied, setCopied] = useState<string | null>(null)
  const [manualLink, setManualLink] = useState<string | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [uploading, setUploading] = useState<{ done: number; total: number } | null>(null)
  const [errors, setErrors] = useState<string[]>([])

  const { data: bucketsData, isLoading: bucketsLoading } = useQuery({
    queryKey: ['storage-buckets'],
    queryFn: storage.listBuckets,
  })

  const { data: objectsData, isLoading: objectsLoading } = useQuery({
    queryKey: ['storage-objects', selectedBucket, currentPath],
    queryFn: () => storage.listObjects(selectedBucket!, currentPath),
    enabled: !!selectedBucket,
  })

  const buckets = bucketsData?.buckets || []
  const bucket = buckets.find((b) => b.name === selectedBucket)
  const objects = objectsData?.objects || []
  const pathParts = currentPath.split('/').filter(Boolean)

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['storage-objects', selectedBucket] })
    queryClient.invalidateQueries({ queryKey: ['storage-buckets'] })
  }
  const fail = (e: unknown) => setErrors((prev) => [...prev, errorText(e)])

  const createBucketMutation = useMutation({
    mutationFn: () => storage.createBucket(newBucketName, settingsFrom(draft)),
    onSuccess: (b) => {
      queryClient.invalidateQueries({ queryKey: ['storage-buckets'] })
      setShowCreateBucket(false)
      setNewBucketName('')
      setSelectedBucket(b.name)
      setCurrentPath('')
    },
    onError: fail,
  })

  const updateBucketMutation = useMutation({
    mutationFn: () => storage.updateBucket(selectedBucket!, settingsFrom(draft)),
    onSuccess: () => {
      refresh()
      setShowSettings(false)
    },
    onError: fail,
  })

  const deleteBucketMutation = useMutation({
    mutationFn: (name: string) => storage.deleteBucket(name, true),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['storage-buckets'] })
      setSelectedBucket(null)
      setCurrentPath('')
      setShowSettings(false)
    },
    onError: fail,
  })

  const deleteObjectMutation = useMutation({
    mutationFn: (key: string) => storage.deleteObject(selectedBucket!, key),
    onSuccess: refresh,
    onError: fail,
  })

  const createFolderMutation = useMutation({
    mutationFn: () => storage.createFolder(selectedBucket!, currentPath + newFolderName.replace(/^\/+|\/+$/g, '')),
    onSuccess: () => {
      refresh()
      setShowCreateFolder(false)
      setNewFolderName('')
    },
    onError: fail,
  })

  const handleFileUpload = useCallback(
    async (files: FileList | null) => {
      if (!files || !selectedBucket || files.length === 0) return
      const list = Array.from(files)
      setUploading({ done: 0, total: list.length })
      const failures: string[] = []
      for (const [i, file] of list.entries()) {
        try {
          await storage.uploadObject(selectedBucket, file, currentPath)
        } catch (e) {
          failures.push(`${file.name}: ${errorText(e)}`)
        }
        setUploading({ done: i + 1, total: list.length })
      }
      setUploading(null)
      if (failures.length) setErrors((prev) => [...prev, ...failures])
      refresh()
      if (fileInputRef.current) fileInputRef.current.value = ''
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selectedBucket, currentPath]
  )

  // The clipboard API can be blocked (e.g. the dashboard embedded in an
  // iframe without clipboard permission): fall back to execCommand, and if
  // that fails too, show the link so it can be copied by hand.
  const copy = async (text: string, id: string) => {
    let ok = false
    try {
      await navigator.clipboard.writeText(text)
      ok = true
    } catch {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      try {
        ok = document.execCommand('copy')
      } catch {
        ok = false
      }
      document.body.removeChild(ta)
    }
    if (!ok) {
      setManualLink(text)
      return
    }
    setManualLink(null)
    setCopied(id)
    setTimeout(() => setCopied((c) => (c === id ? null : c)), 1500)
  }

  const copySignedLink = async (obj: StorageEntry, minutes: number) => {
    try {
      const { url } = await storage.getSignedUrl(selectedBucket!, obj.key, minutes)
      await copy(url, `${obj.key}:${minutes}`)
    } catch (e) {
      fail(e)
    }
  }

  const download = async (obj: StorageEntry) => {
    try {
      const { url } = await storage.getSignedUrl(selectedBucket!, obj.key, 10, true)
      window.location.assign(url)
    } catch (e) {
      fail(e)
    }
  }

  const openSettings = () => {
    setDraft(draftFrom(bucket))
    setShowSettings(true)
  }

  return (
    <div className="h-full">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Storage</h1>
          <p className="text-gray-600 mt-1">Buckets and files. Public buckets serve files at a permanent URL; private ones need keys or a signed link.</p>
        </div>
        <button
          onClick={() => {
            setDraft(draftFrom())
            setNewBucketName('')
            setShowCreateBucket(true)
          }}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
        >
          <Plus className="w-4 h-4" />
          New Bucket
        </button>
      </div>

      {errors.length > 0 && (
        <div className="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 flex items-start gap-3">
          <AlertTriangle className="w-4 h-4 mt-0.5 flex-shrink-0" />
          <div className="flex-1 space-y-1">
            {errors.map((e, i) => (
              <p key={i}>{e}</p>
            ))}
          </div>
          <button onClick={() => setErrors([])} className="text-red-500 hover:text-red-700">
            <X className="w-4 h-4" />
          </button>
        </div>
      )}

      <div className="flex gap-6 h-[calc(100vh-220px)]">
        {/* Buckets sidebar */}
        <div className="w-64 flex-shrink-0 bg-white rounded-xl border border-gray-200 overflow-hidden flex flex-col">
          <div className="px-4 py-3 border-b border-gray-200 bg-gray-50">
            <h2 className="font-medium text-gray-900 flex items-center gap-2">
              <HardDrive className="w-4 h-4" />
              Buckets
            </h2>
          </div>
          <div className="flex-1 overflow-y-auto">
            {bucketsLoading ? (
              <div className="flex items-center justify-center py-8">
                <Loader2 className="w-6 h-6 text-blue-600 animate-spin" />
              </div>
            ) : buckets.length === 0 ? (
              <div className="text-center py-8 px-4">
                <HardDrive className="w-10 h-10 text-gray-300 mx-auto mb-2" />
                <p className="text-sm text-gray-500">No buckets yet</p>
              </div>
            ) : (
              <div className="p-2">
                {buckets.map((b) => (
                  <button
                    key={b.name}
                    className={`w-full text-left px-3 py-2 rounded-lg transition-colors ${
                      selectedBucket === b.name ? 'bg-blue-50 text-blue-700' : 'hover:bg-gray-50 text-gray-700'
                    }`}
                    onClick={() => {
                      setSelectedBucket(b.name)
                      setCurrentPath('')
                    }}
                  >
                    <div className="flex items-center gap-2 min-w-0">
                      {b.public ? (
                        <Globe className="w-4 h-4 flex-shrink-0 text-green-600" />
                      ) : (
                        <Lock className="w-4 h-4 flex-shrink-0 text-gray-400" />
                      )}
                      <span className="text-sm font-medium truncate">{b.name}</span>
                    </div>
                    <p className="ml-6 text-xs text-gray-500">
                      {b.object_count} {b.object_count === 1 ? 'file' : 'files'} · {formatBytes(b.total_size)}
                    </p>
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>

        {/* Objects panel */}
        <div className="flex-1 min-w-0 bg-white rounded-xl border border-gray-200 overflow-hidden flex flex-col">
          {!selectedBucket ? (
            <div className="flex-1 flex items-center justify-center">
              <div className="text-center">
                <Folder className="w-16 h-16 text-gray-300 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-gray-900 mb-2">Select a bucket</h3>
                <p className="text-gray-500">Choose a bucket from the sidebar to view its contents</p>
              </div>
            </div>
          ) : (
            <>
              <div className="px-4 py-3 border-b border-gray-200 bg-gray-50 flex items-center justify-between gap-4">
                <div className="flex items-center gap-2 min-w-0 overflow-x-auto">
                  <button
                    onClick={() => setCurrentPath('')}
                    className={`text-sm font-medium ${currentPath ? 'text-blue-600 hover:text-blue-700' : 'text-gray-900'}`}
                  >
                    {selectedBucket}
                  </button>
                  {pathParts.map((part, index) => (
                    <div key={index} className="flex items-center gap-2">
                      <ChevronRight className="w-4 h-4 text-gray-400" />
                      <button
                        onClick={() => setCurrentPath(pathParts.slice(0, index + 1).join('/') + '/')}
                        className={`text-sm font-medium whitespace-nowrap ${
                          index === pathParts.length - 1 ? 'text-gray-900' : 'text-blue-600 hover:text-blue-700'
                        }`}
                      >
                        {part}
                      </button>
                    </div>
                  ))}
                </div>
                <div className="flex items-center gap-2 flex-shrink-0">
                  <span
                    className={`flex items-center gap-1.5 px-2.5 py-1 text-xs rounded-full border ${
                      bucket?.public ? 'bg-green-50 border-green-200 text-green-700' : 'bg-gray-50 border-gray-200 text-gray-600'
                    }`}
                  >
                    {bucket?.public ? <Globe className="w-3.5 h-3.5" /> : <Lock className="w-3.5 h-3.5" />}
                    {bucket?.public ? 'Public' : 'Private'}
                  </span>
                  <button
                    onClick={openSettings}
                    className="p-1.5 text-gray-500 hover:text-gray-800 hover:bg-gray-100 rounded-lg"
                    title="Bucket settings"
                  >
                    <Settings className="w-4 h-4" />
                  </button>
                  {currentPath && (
                    <button
                      onClick={() => {
                        const parts = [...pathParts]
                        parts.pop()
                        setCurrentPath(parts.length ? parts.join('/') + '/' : '')
                      }}
                      className="flex items-center gap-1 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
                    >
                      <ChevronLeft className="w-4 h-4" />
                      Back
                    </button>
                  )}
                  <button
                    onClick={() => setShowCreateFolder(true)}
                    className="flex items-center gap-1 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-100 rounded-lg border border-gray-200 transition-colors"
                  >
                    <FolderPlus className="w-4 h-4" />
                    New Folder
                  </button>
                  <button
                    onClick={() => fileInputRef.current?.click()}
                    className="flex items-center gap-1 px-3 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
                  >
                    <Upload className="w-4 h-4" />
                    Upload
                  </button>
                  <input
                    ref={fileInputRef}
                    type="file"
                    multiple
                    className="hidden"
                    onChange={(e) => handleFileUpload(e.target.files)}
                  />
                </div>
              </div>

              {bucket && (bucket.file_size_limit || bucket.allowed_mime_types.length > 0) && (
                <div className="px-4 py-2 border-b border-gray-200 text-xs text-gray-500">
                  {bucket.file_size_limit ? `Max ${formatBytes(bucket.file_size_limit)} per file` : ''}
                  {bucket.file_size_limit && bucket.allowed_mime_types.length > 0 ? ' · ' : ''}
                  {bucket.allowed_mime_types.length > 0 ? `Only ${bucket.allowed_mime_types.join(', ')}` : ''}
                </div>
              )}

              <div
                className={`flex-1 overflow-y-auto ${dragOver ? 'bg-blue-50' : ''}`}
                onDrop={(e) => {
                  e.preventDefault()
                  setDragOver(false)
                  handleFileUpload(e.dataTransfer.files)
                }}
                onDragOver={(e) => {
                  e.preventDefault()
                  setDragOver(true)
                }}
                onDragLeave={(e) => {
                  e.preventDefault()
                  setDragOver(false)
                }}
              >
                {objectsLoading || uploading ? (
                  <div className="flex items-center justify-center py-12">
                    <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
                    {uploading && (
                      <span className="ml-2 text-gray-600">
                        Uploading {uploading.done}/{uploading.total}…
                      </span>
                    )}
                  </div>
                ) : objects.length === 0 ? (
                  <div className="flex-1 flex items-center justify-center py-12">
                    <div className="text-center">
                      <Upload className="w-12 h-12 text-gray-300 mx-auto mb-4" />
                      <h3 className="text-lg font-medium text-gray-900 mb-2">No files yet</h3>
                      <p className="text-gray-500 mb-4">Drag and drop files here or click Upload</p>
                    </div>
                  </div>
                ) : (
                  <table className="w-full">
                    <thead className="bg-gray-50 sticky top-0">
                      <tr>
                        <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Name</th>
                        <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Size</th>
                        <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Modified</th>
                        <th className="px-4 py-3 text-right text-xs font-medium text-gray-500 uppercase tracking-wider">Actions</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-200">
                      {objects.map((obj) => {
                        const FileIcon = getFileIcon(obj.content_type, obj.is_dir)
                        const thumb = !obj.is_dir && isPreviewableImage(obj.content_type) && obj.url
                        return (
                          <tr
                            key={obj.key}
                            className="hover:bg-gray-50 cursor-pointer"
                            onClick={() => (obj.is_dir ? setCurrentPath(obj.key) : setPreview(obj))}
                          >
                            <td className="px-4 py-3">
                              <div className="flex items-center gap-3 min-w-0">
                                {thumb ? (
                                  <div className="w-10 h-10 rounded bg-gray-100 overflow-hidden flex-shrink-0">
                                    <img src={obj.url} alt={obj.name} loading="lazy" className="w-full h-full object-cover" />
                                  </div>
                                ) : (
                                  <FileIcon className={`w-5 h-5 flex-shrink-0 ${obj.is_dir ? 'text-yellow-500' : 'text-gray-400'}`} />
                                )}
                                <span className="text-sm font-medium text-gray-900 truncate">{obj.name}</span>
                              </div>
                            </td>
                            <td className="px-4 py-3 text-sm text-gray-500 whitespace-nowrap">{obj.is_dir ? '-' : formatBytes(obj.size)}</td>
                            <td className="px-4 py-3 text-sm text-gray-500 whitespace-nowrap">
                              {obj.is_dir || !obj.last_modified ? '-' : formatDate(obj.last_modified)}
                            </td>
                            <td className="px-4 py-3 text-right">
                              <div className="flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                                {!obj.is_dir && (
                                  <>
                                    <button
                                      onClick={() => setPreview(obj)}
                                      className="p-1.5 text-gray-400 hover:text-blue-600 transition-colors"
                                      title="Preview"
                                    >
                                      <Eye className="w-4 h-4" />
                                    </button>
                                    <button
                                      onClick={() => download(obj)}
                                      className="p-1.5 text-gray-400 hover:text-blue-600 transition-colors"
                                      title="Download"
                                    >
                                      <Download className="w-4 h-4" />
                                    </button>
                                    <button
                                      onClick={() => { setManualLink(null); setLinkFor(obj) }}
                                      className="p-1.5 text-gray-400 hover:text-blue-600 transition-colors"
                                      title="Get link"
                                    >
                                      <Link className="w-4 h-4" />
                                    </button>
                                  </>
                                )}
                                <button
                                  onClick={() => {
                                    const msg = obj.is_dir
                                      ? `Delete the folder "${obj.name}" and everything in it?`
                                      : `Delete "${obj.name}"?`
                                    if (confirm(msg)) deleteObjectMutation.mutate(obj.key)
                                  }}
                                  className="p-1.5 text-gray-400 hover:text-red-600 transition-colors"
                                  title="Delete"
                                >
                                  <Trash2 className="w-4 h-4" />
                                </button>
                              </div>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                )}
              </div>
            </>
          )}
        </div>
      </div>

      {showCreateBucket && (
        <Modal
          title="Create New Bucket"
          onClose={() => setShowCreateBucket(false)}
          footer={
            <>
              <button onClick={() => setShowCreateBucket(false)} className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors">
                Cancel
              </button>
              <button
                onClick={() => createBucketMutation.mutate()}
                disabled={newBucketName.length < 3 || createBucketMutation.isPending}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {createBucketMutation.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
                Create Bucket
              </button>
            </>
          }
        >
          <div className="mb-5">
            <label className="block text-sm font-medium text-gray-700 mb-1">Bucket Name</label>
            <input
              type="text"
              autoFocus
              value={newBucketName}
              onChange={(e) => setNewBucketName(e.target.value.toLowerCase().replace(/[^a-z0-9.-]/g, ''))}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
              placeholder="avatars"
            />
            <p className="mt-1 text-xs text-gray-500">Lowercase letters, numbers, dots and hyphens (3-63 chars)</p>
          </div>
          <BucketSettingsFields draft={draft} onChange={setDraft} />
        </Modal>
      )}

      {showSettings && bucket && (
        <Modal
          wide
          title={`Settings · ${bucket.name}`}
          onClose={() => setShowSettings(false)}
          footer={
            <>
              <button
                onClick={() => {
                  if (confirm(`Delete bucket "${bucket.name}" and all its ${bucket.object_count} files? This cannot be undone.`)) {
                    deleteBucketMutation.mutate(bucket.name)
                  }
                }}
                className="mr-auto flex items-center gap-1.5 px-3 py-2 text-sm text-red-600 hover:bg-red-50 rounded-lg"
              >
                <Trash2 className="w-4 h-4" />
                Delete bucket
              </button>
              <button onClick={() => setShowSettings(false)} className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors">
                Cancel
              </button>
              <button
                onClick={() => updateBucketMutation.mutate()}
                disabled={updateBucketMutation.isPending}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors"
              >
                {updateBucketMutation.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
                Save
              </button>
            </>
          }
        >
          <BucketSettingsFields draft={draft} onChange={setDraft} />
          <p className="mt-4 text-xs text-gray-500">
            {bucket.object_count} {bucket.object_count === 1 ? 'file' : 'files'} · {formatBytes(bucket.total_size)}
          </p>
          <BucketRules bucket={bucket.name} />
        </Modal>
      )}

      {showCreateFolder && (
        <Modal
          title="Create New Folder"
          onClose={() => setShowCreateFolder(false)}
          footer={
            <>
              <button onClick={() => setShowCreateFolder(false)} className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors">
                Cancel
              </button>
              <button
                onClick={() => createFolderMutation.mutate()}
                disabled={!newFolderName.trim() || createFolderMutation.isPending}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {createFolderMutation.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
                Create Folder
              </button>
            </>
          }
        >
          <label className="block text-sm font-medium text-gray-700 mb-1">Folder Name</label>
          <input
            type="text"
            autoFocus
            value={newFolderName}
            onChange={(e) => setNewFolderName(e.target.value)}
            className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
            placeholder="my-folder"
          />
        </Modal>
      )}

      {linkFor && (
        <Modal
          title={`Link · ${linkFor.name}`}
          onClose={() => setLinkFor(null)}
          footer={
            <button onClick={() => setLinkFor(null)} className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors">
              Close
            </button>
          }
        >
          {linkFor.public_url && (
            <div className="mb-5">
              <p className="text-sm font-medium text-gray-700 mb-1">Public URL (permanent)</p>
              <div className="flex gap-2">
                <input readOnly value={linkFor.public_url} className="flex-1 min-w-0 px-3 py-2 text-xs font-mono border border-gray-300 rounded-lg bg-gray-50" />
                <button
                  onClick={() => copy(linkFor.public_url!, 'public')}
                  className="px-3 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700"
                >
                  {copied === 'public' ? <Check className="w-4 h-4" /> : 'Copy'}
                </button>
              </div>
            </div>
          )}
          <p className="text-sm font-medium text-gray-700 mb-1">Signed link (expires)</p>
          <p className="text-xs text-gray-500 mb-3">Anyone with the link can open this file until it expires, even if the bucket is private.</p>
          <div className="flex flex-wrap gap-2">
            {LINK_EXPIRIES.map(([minutes, label]) => (
              <button
                key={minutes}
                onClick={() => copySignedLink(linkFor, minutes)}
                className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-200 rounded-lg hover:bg-gray-50"
              >
                {copied === `${linkFor.key}:${minutes}` ? <Check className="w-4 h-4 text-green-600" /> : <Link className="w-4 h-4" />}
                Copy · {label}
              </button>
            ))}
          </div>
          {manualLink && (
            <div className="mt-4">
              <p className="text-xs text-gray-500 mb-1">The browser blocked the clipboard here. Copy the link by hand:</p>
              <input
                readOnly
                autoFocus
                value={manualLink}
                onFocus={(e) => e.currentTarget.select()}
                className="w-full px-3 py-2 text-xs font-mono border border-gray-300 rounded-lg bg-gray-50"
              />
            </div>
          )}
        </Modal>
      )}

      {preview && (
        <div className="fixed inset-0 bg-black/80 flex items-center justify-center z-50 p-4" onClick={() => setPreview(null)}>
          <div className="relative max-w-4xl w-full max-h-[90vh] flex flex-col items-center" onClick={(e) => e.stopPropagation()}>
            <button onClick={() => setPreview(null)} className="absolute -top-10 right-0 text-gray-900 hover:text-gray-600 transition-colors">
              <X className="w-6 h-6" />
            </button>
            {isPreviewableImage(preview.content_type) ? (
              <img src={preview.url} alt={preview.name} className="max-w-full max-h-[80vh] object-contain rounded-lg" />
            ) : preview.content_type.startsWith('video/') ? (
              <video src={preview.url} controls className="max-w-full max-h-[80vh] rounded-lg" />
            ) : preview.content_type.startsWith('audio/') ? (
              <audio src={preview.url} controls className="w-full" />
            ) : preview.content_type === 'application/pdf' ? (
              <iframe src={preview.url} title={preview.name} className="w-full h-[80vh] rounded-lg bg-white" />
            ) : (
              <div className="bg-white rounded-lg p-8 text-center">
                <File className="w-12 h-12 text-gray-300 mx-auto mb-3" />
                <p className="text-sm text-gray-600 mb-4">No preview for {preview.content_type}.</p>
                <button onClick={() => download(preview)} className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700">
                  Download
                </button>
              </div>
            )}
            <p className="text-center text-gray-900 mt-2 text-sm">
              {preview.name} · {formatBytes(preview.size)}
            </p>
          </div>
        </div>
      )}
    </div>
  )
}
