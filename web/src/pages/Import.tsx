import { useState, useRef } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { importExport, tables, type UploadProgress } from '../lib/api'
import { Upload, FileJson, FileCode, FileSpreadsheet, Loader2, CheckCircle, AlertCircle, X } from 'lucide-react'

type ImportKind = 'sql' | 'json' | 'csv'
type ProgressState = { kind: ImportKind; fileName: string; fileSize: number; progress: UploadProgress | null }

function formatBytes(b: number): string {
  if (b < 1024) return `${b} B`
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`
  if (b < 1024 * 1024 * 1024) return `${(b / (1024 * 1024)).toFixed(1)} MB`
  return `${(b / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

// Mirrors isValidIdentifier in internal/database/schema.go so we never let the
// user start a multi-MB upload only to have the server reject the name.
function validateTableName(name: string): string | null {
  if (!name) return null
  if (name.length > 63) return 'Maximum 63 characters'
  if (!/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(name)) {
    return 'Only letters, numbers and underscores. Cannot start with a number.'
  }
  return null
}

export default function Import() {
  const queryClient = useQueryClient()
  const sqlFileRef = useRef<HTMLInputElement>(null)
  const jsonFileRef = useRef<HTMLInputElement>(null)
  const csvFileRef = useRef<HTMLInputElement>(null)
  
  // JSON state
  const [jsonTable, setJsonTable] = useState('')
  const [jsonNewTableName, setJsonNewTableName] = useState('')
  const [jsonCreateNewTable, setJsonCreateNewTable] = useState(false)
  const [jsonAutoCreate, setJsonAutoCreate] = useState(true)
  
  // CSV state
  const [csvTable, setCsvTable] = useState('')
  const [csvNewTableName, setCsvNewTableName] = useState('')
  const [csvCreateNewTable, setCsvCreateNewTable] = useState(false)
  const [csvAutoCreate, setCsvAutoCreate] = useState(true)
  
  const [result, setResult] = useState<{ type: 'success' | 'error'; message: string } | null>(null)
  const [active, setActive] = useState<ProgressState | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const { data: tablesData } = useQuery({
    queryKey: ['tables'],
    queryFn: tables.list,
  })

  const trackProgress = (kind: ImportKind, file: File) => {
    const controller = new AbortController()
    abortRef.current = controller
    setActive({ kind, fileName: file.name, fileSize: file.size, progress: null })
    return {
      signal: controller.signal,
      onProgress: (p: UploadProgress) => {
        setActive((prev) => (prev ? { ...prev, progress: p } : prev))
      },
    }
  }

  const clearActive = () => {
    abortRef.current = null
    setActive(null)
  }

  const sqlMutation = useMutation({
    mutationFn: (file: File) => importExport.importSQL(file, trackProgress('sql', file)),
    onSuccess: (data) => {
      setResult({ type: 'success', message: `SQL import completed. ${data.rows_affected} rows affected.` })
      queryClient.invalidateQueries({ queryKey: ['tables'] })
      clearActive()
    },
    onError: (err: any) => {
      setResult({ type: 'error', message: err.message })
      clearActive()
    },
  })

  const jsonMutation = useMutation({
    mutationFn: ({ table, file, autoCreate }: { table: string; file: File; autoCreate: boolean }) =>
      importExport.importJSON(table, file, autoCreate, trackProgress('json', file)),
    onSuccess: (data) => {
      setResult({ type: 'success', message: `JSON import completed. ${data.rows_affected} rows imported.` })
      queryClient.invalidateQueries({ queryKey: ['tables'] })
      clearActive()
      if (jsonFileRef.current) jsonFileRef.current.value = ''
    },
    onError: (err: any) => {
      setResult({ type: 'error', message: err.message })
      clearActive()
      if (jsonFileRef.current) jsonFileRef.current.value = ''
    },
  })

  const csvMutation = useMutation({
    mutationFn: ({ table, file, autoCreate }: { table: string; file: File; autoCreate: boolean }) =>
      importExport.importCSV(table, file, autoCreate, trackProgress('csv', file)),
    onSuccess: (data) => {
      setResult({ type: 'success', message: `CSV import completed. ${data.rows_affected} rows imported.` })
      queryClient.invalidateQueries({ queryKey: ['tables'] })
      clearActive()
      if (csvFileRef.current) csvFileRef.current.value = ''
    },
    onError: (err: any) => {
      setResult({ type: 'error', message: err.message })
      clearActive()
      if (csvFileRef.current) csvFileRef.current.value = ''
    },
  })

  const cancelUpload = () => abortRef.current?.abort()

  const handleSQLUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (file) {
      setResult(null)
      sqlMutation.mutate(file)
    }
  }

  const handleJSONUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    const tableName = jsonCreateNewTable ? jsonNewTableName : jsonTable
    if (file && tableName) {
      setResult(null)
      jsonMutation.mutate({ table: tableName, file, autoCreate: jsonAutoCreate })
    }
  }

  const handleCSVUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    const tableName = csvCreateNewTable ? csvNewTableName : csvTable
    if (file && tableName) {
      setResult(null)
      csvMutation.mutate({ table: tableName, file, autoCreate: csvAutoCreate })
    }
  }

  const tableList = tablesData?.tables || []

  const jsonNameError = jsonCreateNewTable ? validateTableName(jsonNewTableName) : null
  const csvNameError = csvCreateNewTable ? validateTableName(csvNewTableName) : null
  const jsonNameInvalid = jsonCreateNewTable && (!jsonNewTableName || jsonNameError !== null)
  const csvNameInvalid = csvCreateNewTable && (!csvNewTableName || csvNameError !== null)

  return (
    <div>
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900">Import Data</h1>
        <p className="text-gray-600 mt-1">Import data from SQL, JSON or CSV files</p>
      </div>

      {result && (
        <div className={`mb-6 p-4 rounded-lg flex items-start gap-3 ${
          result.type === 'success' ? 'bg-green-50 text-green-700' : 'bg-red-50 text-red-700'
        }`}>
          {result.type === 'success' ? (
            <CheckCircle className="w-5 h-5 flex-shrink-0 mt-0.5" />
          ) : (
            <AlertCircle className="w-5 h-5 flex-shrink-0 mt-0.5" />
          )}
          <p>{result.message}</p>
        </div>
      )}

      {active && (() => {
        const loaded = active.progress?.loaded ?? 0
        const total = active.progress?.total ?? active.fileSize
        const pct = total > 0 ? Math.min(100, Math.round((loaded / total) * 100)) : 0
        const phase = active.progress?.phase ?? 'uploading'
        return (
          <div className="mb-6 p-4 rounded-lg bg-blue-50 border border-blue-200">
            <div className="flex items-center justify-between mb-2">
              <div className="flex items-center gap-2 text-blue-800 text-sm font-medium min-w-0">
                <Loader2 className="w-4 h-4 animate-spin flex-shrink-0" />
                <span className="truncate">{active.fileName}</span>
                <span className="text-blue-600 flex-shrink-0">({formatBytes(active.fileSize)})</span>
              </div>
              <button
                onClick={cancelUpload}
                className="text-blue-700 hover:text-blue-900 p-1 rounded hover:bg-blue-100"
                title="Cancel upload"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="w-full bg-blue-200 rounded-full h-2 overflow-hidden">
              <div
                className={`h-full rounded-full transition-all ${phase === 'processing' ? 'bg-blue-400 animate-pulse' : 'bg-blue-600'}`}
                style={{ width: `${phase === 'processing' ? 100 : pct}%` }}
              />
            </div>
            <div className="mt-2 text-xs text-blue-700 flex justify-between">
              <span>
                {phase === 'uploading'
                  ? `Uploading: ${formatBytes(loaded)} / ${formatBytes(total)} (${pct}%)`
                  : 'Server is importing — bulk loading into Postgres…'}
              </span>
            </div>
          </div>
        )
      })()}

      <div className="grid md:grid-cols-2 gap-6">
        {/* SQL Import */}
        <div className="bg-white rounded-xl border border-gray-200 p-6">
          <div className="flex items-center gap-3 mb-4">
            <div className="p-3 bg-blue-100 rounded-lg">
              <FileCode className="w-6 h-6 text-blue-600" />
            </div>
            <div>
              <h2 className="text-lg font-semibold text-gray-900">SQL Import</h2>
              <p className="text-sm text-gray-600">Import from .sql file</p>
            </div>
          </div>

          <p className="text-sm text-gray-600 mb-4">
            Upload a SQL file containing CREATE TABLE and/or INSERT statements.
            The file will be executed directly against the database.
          </p>

          <input
            ref={sqlFileRef}
            type="file"
            accept=".sql"
            onChange={handleSQLUpload}
            className="hidden"
          />

          <button
            onClick={() => sqlFileRef.current?.click()}
            disabled={sqlMutation.isPending}
            className="w-full flex items-center justify-center gap-2 px-4 py-3 border-2 border-dashed border-gray-300 rounded-lg text-gray-600 hover:border-blue-500 hover:text-blue-600 transition-colors disabled:opacity-50"
          >
            {sqlMutation.isPending ? (
              <Loader2 className="w-5 h-5 animate-spin" />
            ) : (
              <Upload className="w-5 h-5" />
            )}
            {sqlMutation.isPending ? 'Importing...' : 'Upload SQL File'}
          </button>
        </div>

        {/* JSON Import */}
        <div className="bg-white rounded-xl border border-gray-200 p-6">
          <div className="flex items-center gap-3 mb-4">
            <div className="p-3 bg-green-100 rounded-lg">
              <FileJson className="w-6 h-6 text-green-600" />
            </div>
            <div>
              <h2 className="text-lg font-semibold text-gray-900">JSON Import</h2>
              <p className="text-sm text-gray-600">Import from .json file with auto-column creation</p>
            </div>
          </div>

          <p className="text-sm text-gray-600 mb-4">
            Upload a JSON file containing an <strong>array of objects</strong> (e.g., <code className="bg-gray-100 px-1 rounded">[{`{...}`}, {`{...}`}]</code>).
            Each object represents a row. Columns are auto-created from JSON keys.
          </p>

          <div className="grid grid-cols-2 gap-4 mb-4">
            <div>
              <label className="flex items-center gap-2 mb-2">
                <input
                  type="radio"
                  checked={!jsonCreateNewTable}
                  onChange={() => setJsonCreateNewTable(false)}
                  className="text-green-600 focus:ring-green-500"
                />
                <span className="text-sm font-medium text-gray-700">Existing Table</span>
              </label>
              <select
                value={jsonTable}
                onChange={(e) => setJsonTable(e.target.value)}
                disabled={jsonCreateNewTable}
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-green-500 focus:border-green-500 outline-none disabled:bg-gray-100 disabled:cursor-not-allowed text-sm"
              >
                <option value="">Select a table...</option>
                {tableList.map((table: any) => (
                  <option key={table.name} value={table.name}>
                    {table.name}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label className="flex items-center gap-2 mb-2">
                <input
                  type="radio"
                  checked={jsonCreateNewTable}
                  onChange={() => setJsonCreateNewTable(true)}
                  className="text-green-600 focus:ring-green-500"
                />
                <span className="text-sm font-medium text-gray-700">Create New Table</span>
              </label>
              <input
                type="text"
                value={jsonNewTableName}
                onChange={(e) => setJsonNewTableName(e.target.value)}
                disabled={!jsonCreateNewTable}
                placeholder="Enter table name..."
                className={`w-full px-3 py-2 border rounded-lg focus:ring-2 outline-none disabled:bg-gray-100 disabled:cursor-not-allowed text-sm ${
                  jsonNameError
                    ? 'border-red-400 focus:ring-red-500 focus:border-red-500'
                    : 'border-gray-300 focus:ring-green-500 focus:border-green-500'
                }`}
              />
              {jsonNameError && (
                <p className="mt-1 text-xs text-red-600">{jsonNameError}</p>
              )}
            </div>
          </div>

          <div className="mb-4">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={jsonAutoCreate}
                onChange={(e) => setJsonAutoCreate(e.target.checked)}
                className="rounded text-green-600 focus:ring-green-500"
              />
              <span className="text-sm text-gray-700">
                Auto-create missing columns (columns will be created as TEXT type)
              </span>
            </label>
          </div>

          <input
            ref={jsonFileRef}
            type="file"
            accept=".json"
            onChange={handleJSONUpload}
            className="hidden"
          />

          <button
            onClick={() => jsonFileRef.current?.click()}
            disabled={jsonMutation.isPending || (!jsonCreateNewTable && !jsonTable) || jsonNameInvalid}
            className="w-full flex items-center justify-center gap-2 px-4 py-3 border-2 border-dashed border-gray-300 rounded-lg text-gray-600 hover:border-green-500 hover:text-green-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {jsonMutation.isPending ? (
              <Loader2 className="w-5 h-5 animate-spin" />
            ) : (
              <Upload className="w-5 h-5" />
            )}
            {jsonMutation.isPending ? 'Importing...' : 'Upload JSON File'}
          </button>
        </div>

        {/* CSV Import */}
        <div className="bg-white rounded-xl border border-gray-200 p-6 md:col-span-2">
          <div className="flex items-center gap-3 mb-4">
            <div className="p-3 bg-orange-100 rounded-lg">
              <FileSpreadsheet className="w-6 h-6 text-orange-600" />
            </div>
            <div>
              <h2 className="text-lg font-semibold text-gray-900">CSV Import</h2>
              <p className="text-sm text-gray-600">Import from .csv file with auto-column creation</p>
            </div>
          </div>

          <p className="text-sm text-gray-600 mb-4">
            Upload a CSV file with headers. Columns will be automatically created based on the CSV headers.
            You can import into an existing table or create a new one.
          </p>

          <div className="grid md:grid-cols-2 gap-4 mb-4">
            <div>
              <label className="flex items-center gap-2 mb-2">
                <input
                  type="radio"
                  checked={!csvCreateNewTable}
                  onChange={() => setCsvCreateNewTable(false)}
                  className="text-orange-600 focus:ring-orange-500"
                />
                <span className="text-sm font-medium text-gray-700">Existing Table</span>
              </label>
              <select
                value={csvTable}
                onChange={(e) => setCsvTable(e.target.value)}
                disabled={csvCreateNewTable}
                className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-orange-500 focus:border-orange-500 outline-none disabled:bg-gray-100 disabled:cursor-not-allowed"
              >
                <option value="">Select a table...</option>
                {tableList.map((table: any) => (
                  <option key={table.name} value={table.name}>
                    {table.name}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label className="flex items-center gap-2 mb-2">
                <input
                  type="radio"
                  checked={csvCreateNewTable}
                  onChange={() => setCsvCreateNewTable(true)}
                  className="text-orange-600 focus:ring-orange-500"
                />
                <span className="text-sm font-medium text-gray-700">Create New Table</span>
              </label>
              <input
                type="text"
                value={csvNewTableName}
                onChange={(e) => setCsvNewTableName(e.target.value)}
                disabled={!csvCreateNewTable}
                placeholder="Enter table name..."
                className={`w-full px-3 py-2 border rounded-lg focus:ring-2 outline-none disabled:bg-gray-100 disabled:cursor-not-allowed ${
                  csvNameError
                    ? 'border-red-400 focus:ring-red-500 focus:border-red-500'
                    : 'border-gray-300 focus:ring-orange-500 focus:border-orange-500'
                }`}
              />
              {csvNameError && (
                <p className="mt-1 text-xs text-red-600">{csvNameError}</p>
              )}
            </div>
          </div>

          <div className="mb-4">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={csvAutoCreate}
                onChange={(e) => setCsvAutoCreate(e.target.checked)}
                className="rounded text-orange-600 focus:ring-orange-500"
              />
              <span className="text-sm text-gray-700">
                Auto-create missing columns (columns will be created as TEXT type)
              </span>
            </label>
          </div>

          <input
            ref={csvFileRef}
            type="file"
            accept=".csv"
            onChange={handleCSVUpload}
            className="hidden"
          />

          <button
            onClick={() => csvFileRef.current?.click()}
            disabled={csvMutation.isPending || (!csvCreateNewTable && !csvTable) || csvNameInvalid}
            className="w-full flex items-center justify-center gap-2 px-4 py-3 border-2 border-dashed border-gray-300 rounded-lg text-gray-600 hover:border-orange-500 hover:text-orange-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {csvMutation.isPending ? (
              <Loader2 className="w-5 h-5 animate-spin" />
            ) : (
              <Upload className="w-5 h-5" />
            )}
            {csvMutation.isPending ? 'Importing...' : 'Upload CSV File'}
          </button>
        </div>
      </div>

      {/* Example formats */}
      <div className="mt-8 bg-gray-50 rounded-xl p-6">
        <h3 className="text-lg font-semibold text-gray-900 mb-4">Example Formats</h3>
        
        <div className="grid md:grid-cols-3 gap-6">
          <div>
            <h4 className="text-sm font-medium text-gray-700 mb-2">SQL File</h4>
            <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-xs overflow-x-auto">
{`CREATE TABLE users (
  id SERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT UNIQUE
);

INSERT INTO users (name, email)
VALUES ('John', 'john@example.com');`}
            </pre>
          </div>
          
          <div>
            <h4 className="text-sm font-medium text-gray-700 mb-2">JSON File</h4>
            <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-xs overflow-x-auto">
{`[
  {
    "name": "John",
    "email": "john@example.com"
  },
  {
    "name": "Jane",
    "email": "jane@example.com"
  }
]`}
            </pre>
          </div>

          <div>
            <h4 className="text-sm font-medium text-gray-700 mb-2">CSV File</h4>
            <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-xs overflow-x-auto">
{`name,email,age
John,john@example.com,25
Jane,jane@example.com,30
Bob,bob@example.com,35`}
            </pre>
          </div>
        </div>
      </div>
    </div>
  )
}
