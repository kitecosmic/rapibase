import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { query } from '../lib/api'
import { Play, Loader2, AlertCircle, CheckCircle } from 'lucide-react'

export default function SQLEditor() {
  const [sql, setSql] = useState('SELECT * FROM ')
  const [result, setResult] = useState<any>(null)
  const [error, setError] = useState<string | null>(null)

  const executeMutation = useMutation({
    mutationFn: () => query.execute(sql),
    onSuccess: (data) => {
      setResult(data)
      setError(null)
    },
    onError: (err: any) => {
      setError(err.message)
      setResult(null)
    },
  })

  const handleExecute = () => {
    if (!sql.trim()) return
    executeMutation.mutate()
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      handleExecute()
    }
  }

  return (
    <div className="h-[calc(100vh-8rem)] flex flex-col">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">SQL Editor</h1>
          <p className="text-gray-600 text-sm">Execute raw SQL queries</p>
        </div>
        <button
          onClick={handleExecute}
          disabled={executeMutation.isPending || !sql.trim()}
          className="flex items-center gap-2 px-4 py-2 bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
        >
          {executeMutation.isPending ? (
            <Loader2 className="w-4 h-4 animate-spin" />
          ) : (
            <Play className="w-4 h-4" />
          )}
          Run Query
          <span className="text-xs opacity-75">(Ctrl+Enter)</span>
        </button>
      </div>

      {/* Editor */}
      <div className="flex-1 flex flex-col min-h-0">
        <div className="bg-white rounded-t-xl border border-gray-200 border-b-0 flex-shrink-0">
          <textarea
            value={sql}
            onChange={(e) => setSql(e.target.value)}
            onKeyDown={handleKeyDown}
            className="w-full h-48 p-4 font-mono text-sm resize-none focus:outline-none rounded-t-xl"
            placeholder="SELECT * FROM users WHERE ..."
            spellCheck={false}
          />
        </div>

        {/* Results */}
        <div className="flex-1 bg-white rounded-b-xl border border-gray-200 overflow-hidden flex flex-col min-h-0">
          <div className="px-4 py-2 bg-gray-50 border-b border-gray-200 flex items-center justify-between flex-shrink-0">
            <span className="text-sm font-medium text-gray-700">Results</span>
            {result && (
              <span className="text-xs text-gray-500">
                {result.rows?.length || 0} rows · {result.duration}
              </span>
            )}
          </div>

          <div className="flex-1 overflow-auto">
            {error && (
              <div className="p-4 flex items-start gap-3 text-red-700 bg-red-50">
                <AlertCircle className="w-5 h-5 flex-shrink-0 mt-0.5" />
                <div>
                  <p className="font-medium">Query Error</p>
                  <p className="text-sm mt-1">{error}</p>
                </div>
              </div>
            )}

            {result && !result.rows && (
              <div className="p-4 flex items-center gap-3 text-green-700 bg-green-50">
                <CheckCircle className="w-5 h-5" />
                <div>
                  <p className="font-medium">Query executed successfully</p>
                  <p className="text-sm">{result.rows_affected} rows affected</p>
                </div>
              </div>
            )}

            {result?.rows && result.rows.length > 0 && (
              <table className="w-full">
                <thead>
                  <tr className="bg-gray-50 border-b border-gray-200">
                    {result.columns?.map((col: string) => (
                      <th
                        key={col}
                        className="px-4 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider whitespace-nowrap"
                      >
                        {col}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200">
                  {result.rows.map((row: any, i: number) => (
                    <tr key={i} className="hover:bg-gray-50">
                      {result.columns?.map((col: string) => (
                        <td key={col} className="px-4 py-2 text-sm text-gray-900 whitespace-nowrap">
                          {row[col] === null ? (
                            <span className="text-gray-400 italic">null</span>
                          ) : typeof row[col] === 'object' ? (
                            <span className="font-mono text-xs">{JSON.stringify(row[col])}</span>
                          ) : (
                            String(row[col])
                          )}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            {result?.rows && result.rows.length === 0 && (
              <div className="p-8 text-center text-gray-500">
                Query returned no results
              </div>
            )}

            {!result && !error && (
              <div className="p-8 text-center text-gray-400">
                Run a query to see results
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
