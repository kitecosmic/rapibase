import { useState, useRef } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { importExport, tables } from '../lib/api'
import { Upload, FileJson, FileCode, Loader2, CheckCircle, AlertCircle } from 'lucide-react'

export default function Import() {
  const queryClient = useQueryClient()
  const sqlFileRef = useRef<HTMLInputElement>(null)
  const jsonFileRef = useRef<HTMLInputElement>(null)
  const [selectedTable, setSelectedTable] = useState('')
  const [result, setResult] = useState<{ type: 'success' | 'error'; message: string } | null>(null)

  const { data: tablesData } = useQuery({
    queryKey: ['tables'],
    queryFn: tables.list,
  })

  const sqlMutation = useMutation({
    mutationFn: (file: File) => importExport.importSQL(file),
    onSuccess: (data) => {
      setResult({ type: 'success', message: `SQL import completed. ${data.rows_affected} rows affected.` })
      queryClient.invalidateQueries({ queryKey: ['tables'] })
    },
    onError: (err: any) => {
      setResult({ type: 'error', message: err.message })
    },
  })

  const jsonMutation = useMutation({
    mutationFn: ({ table, file }: { table: string; file: File }) => importExport.importJSON(table, file),
    onSuccess: (data) => {
      setResult({ type: 'success', message: `JSON import completed. ${data.rows_affected} rows imported.` })
      queryClient.invalidateQueries({ queryKey: ['tables'] })
    },
    onError: (err: any) => {
      setResult({ type: 'error', message: err.message })
    },
  })

  const handleSQLUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (file) {
      setResult(null)
      sqlMutation.mutate(file)
    }
  }

  const handleJSONUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (file && selectedTable) {
      setResult(null)
      jsonMutation.mutate({ table: selectedTable, file })
    }
  }

  const tableList = tablesData?.tables || []

  return (
    <div>
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900">Import Data</h1>
        <p className="text-gray-600 mt-1">Import data from SQL or JSON files</p>
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
              <p className="text-sm text-gray-600">Import from .json file</p>
            </div>
          </div>

          <p className="text-sm text-gray-600 mb-4">
            Upload a JSON file containing an array of objects.
            Each object will be inserted as a row in the selected table.
          </p>

          <div className="mb-4">
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Target Table
            </label>
            <select
              value={selectedTable}
              onChange={(e) => setSelectedTable(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
            >
              <option value="">Select a table...</option>
              {tableList.map((table: any) => (
                <option key={table.name} value={table.name}>
                  {table.name}
                </option>
              ))}
            </select>
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
            disabled={jsonMutation.isPending || !selectedTable}
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
      </div>

      {/* Example formats */}
      <div className="mt-8 bg-gray-50 rounded-xl p-6">
        <h3 className="text-lg font-semibold text-gray-900 mb-4">Example Formats</h3>
        
        <div className="grid md:grid-cols-2 gap-6">
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
        </div>
      </div>
    </div>
  )
}
