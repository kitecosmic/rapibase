import { useState, useCallback, useMemo } from 'react'
import { useMutation } from '@tanstack/react-query'
import { query } from '../lib/api'
import { Play, Loader2, AlertCircle, CheckCircle, Copy, Check, Table, Code } from 'lucide-react'
import CodeMirror from '@uiw/react-codemirror'
import { sql as sqlLang } from '@codemirror/lang-sql'

type ViewMode = 'table' | 'json'

// Renderizar decenas de miles de filas congela la pestaña: la tabla y el
// JSON se muestran por tramos, y copiar se bloquea cuando el resultado no
// cabe razonablemente en el portapapeles.
const ROW_STEP = 500
const JSON_PREVIEW_ROWS = 200
const MAX_COPY_BYTES = 8 * 1024 * 1024 // 8 MB

// Estimación barata del peso del JSON: muestrea filas en vez de serializar
// todo (serializar 100k filas es justamente lo que congela).
function estimateJsonBytes(result: any): number {
  const rows = result?.rows
  if (!rows || rows.length === 0) return JSON.stringify(result ?? {}).length
  const sample = rows.slice(0, 50)
  const sampleBytes = JSON.stringify(sample).length
  return Math.round((sampleBytes / sample.length) * rows.length) + 200
}

function fmtBytes(n: number) {
  if (n < 1024) return `${n} B`
  if (n < 1048576) return `${(n / 1024).toFixed(0)} KB`
  return `${(n / 1048576).toFixed(1)} MB`
}

export default function SQLEditor() {
  const [sqlCode, setSqlCode] = useState('SELECT * FROM ')
  const [result, setResult] = useState<any>(null)
  const [error, setError] = useState<string | null>(null)
  const [viewMode, setViewMode] = useState<ViewMode>('table')
  const [copied, setCopied] = useState(false)
  const [copyBlocked, setCopyBlocked] = useState('')
  const [visibleRows, setVisibleRows] = useState(ROW_STEP)

  const executeMutation = useMutation({
    mutationFn: () => query.execute(sqlCode),
    onSuccess: (data) => {
      setResult(data)
      setError(null)
      setVisibleRows(ROW_STEP)
      setCopyBlocked('')
    },
    onError: (err: any) => {
      setError(err.message)
      setResult(null)
    },
  })

  const handleExecute = useCallback(() => {
    if (!sqlCode.trim()) return
    executeMutation.mutate()
  }, [sqlCode, executeMutation])

  const estimatedBytes = useMemo(() => (result ? estimateJsonBytes(result) : 0), [result])
  const jsonTooBig = estimatedBytes > 2 * 1024 * 1024

  const handleCopyJson = useCallback(async () => {
    if (estimatedBytes > MAX_COPY_BYTES) {
      setCopyBlocked(
        `Result is ~${fmtBytes(estimatedBytes)} — too large for the clipboard. Add a LIMIT or export instead.`
      )
      setTimeout(() => setCopyBlocked(''), 5000)
      return
    }
    const jsonString = JSON.stringify(result, null, 2)
    await navigator.clipboard.writeText(jsonString)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [result, estimatedBytes])

  const handleKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
      event.preventDefault()
      handleExecute()
    }
  }, [handleExecute])

  return (
    <div className="h-[calc(100vh-8rem)] flex flex-col">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">SQL Editor</h1>
          <p className="text-gray-600 text-sm">Execute raw SQL queries</p>
        </div>
        <button
          onClick={handleExecute}
          disabled={executeMutation.isPending || !sqlCode.trim()}
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
        <div className="bg-white rounded-t-xl border border-gray-200 border-b-0 flex-shrink-0 overflow-hidden">
          <CodeMirror
            value={sqlCode}
            height="200px"
            theme="dark"
            extensions={[sqlLang()]}
            onChange={(value: string) => setSqlCode(value)}
            onKeyDown={handleKeyDown}
            placeholder="SELECT * FROM users WHERE ..."
            className="text-sm"
            basicSetup={{
              lineNumbers: true,
              highlightActiveLineGutter: true,
              highlightActiveLine: true,
              foldGutter: true,
              autocompletion: true,
              bracketMatching: true,
              closeBrackets: true,
              indentOnInput: true,
            }}
          />
        </div>

        {/* Results */}
        <div className="flex-1 bg-white rounded-b-xl border border-gray-200 overflow-hidden flex flex-col min-h-0">
          <div className="px-4 py-2 bg-gray-50 border-b border-gray-200 flex items-center justify-between flex-shrink-0">
            <div className="flex items-center gap-4">
              <span className="text-sm font-medium text-gray-700">Results</span>
              {result && (
                <div className="flex items-center gap-1 bg-gray-200 rounded-lg p-0.5">
                  <button
                    onClick={() => setViewMode('table')}
                    className={`flex items-center gap-1.5 px-2 py-1 text-xs font-medium rounded-md transition-colors ${
                      viewMode === 'table'
                        ? 'bg-white text-gray-900 shadow-sm'
                        : 'text-gray-600 hover:text-gray-900'
                    }`}
                  >
                    <Table className="w-3.5 h-3.5" />
                    Table
                  </button>
                  <button
                    onClick={() => setViewMode('json')}
                    className={`flex items-center gap-1.5 px-2 py-1 text-xs font-medium rounded-md transition-colors ${
                      viewMode === 'json'
                        ? 'bg-white text-gray-900 shadow-sm'
                        : 'text-gray-600 hover:text-gray-900'
                    }`}
                  >
                    <Code className="w-3.5 h-3.5" />
                    JSON
                  </button>
                </div>
              )}
            </div>
            <div className="flex items-center gap-3">
              {copyBlocked && <span className="text-xs text-amber-600">{copyBlocked}</span>}
              {result && (
                <>
                  <span className="text-xs text-gray-500">
                    {(result.rows?.length || 0).toLocaleString()} rows · {result.duration}
                  </span>
                  <button
                    onClick={handleCopyJson}
                    className="flex items-center gap-1.5 px-2 py-1 text-xs font-medium text-gray-600 hover:text-gray-900 hover:bg-gray-200 rounded-md transition-colors"
                    title="Copy JSON response"
                  >
                    {copied ? (
                      <>
                        <Check className="w-3.5 h-3.5 text-green-600" />
                        <span className="text-green-600">Copied!</span>
                      </>
                    ) : (
                      <>
                        <Copy className="w-3.5 h-3.5" />
                        Copy JSON
                      </>
                    )}
                  </button>
                </>
              )}
            </div>
          </div>

          <div className="flex-1 overflow-auto">
            {error && (
              <div className="p-4 flex items-start gap-3 text-red-700 bg-red-50">
                <AlertCircle className="w-5 h-5 flex-shrink-0 mt-0.5" />
                <div className="flex-1">
                  <p className="font-medium">Query Error</p>
                  <pre className="text-sm mt-1 whitespace-pre-wrap font-mono bg-red-100 p-2 rounded mt-2">{error}</pre>
                </div>
              </div>
            )}

            {result && viewMode === 'json' && (
              <div className="p-4">
                {jsonTooBig && (
                  <p className="text-xs text-amber-600 mb-2">
                    Result is ~{fmtBytes(estimatedBytes)} — showing the first {JSON_PREVIEW_ROWS} rows to keep the tab responsive.
                  </p>
                )}
                <pre className="text-sm font-mono bg-gray-100 text-green-400 p-4 rounded-lg overflow-auto whitespace-pre-wrap">
                  {jsonTooBig
                    ? JSON.stringify({ ...result, rows: result.rows?.slice(0, JSON_PREVIEW_ROWS) }, null, 2)
                    : JSON.stringify(result, null, 2)}
                </pre>
              </div>
            )}

            {result && viewMode === 'table' && (
              <>
                {!result.rows && (
                  <div className="p-4 flex items-center gap-3 text-green-700 bg-green-50">
                    <CheckCircle className="w-5 h-5" />
                    <div>
                      <p className="font-medium">Query executed successfully</p>
                      <p className="text-sm">{result.rows_affected} rows affected</p>
                    </div>
                  </div>
                )}

                {result.rows && result.rows.length > 0 && (
                  <>
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
                        {result.rows.slice(0, visibleRows).map((row: any, i: number) => (
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
                    {result.rows.length > visibleRows && (
                      <div className="p-3 text-center border-t border-gray-200">
                        <button
                          onClick={() => setVisibleRows((v) => v + ROW_STEP)}
                          className="px-3 py-1.5 text-xs font-medium bg-gray-100 hover:bg-gray-200 border border-gray-300 rounded-lg transition-colors"
                        >
                          Show {Math.min(ROW_STEP, result.rows.length - visibleRows)} more
                          <span className="text-gray-500 ml-1.5">
                            ({(result.rows.length - visibleRows).toLocaleString()} remaining)
                          </span>
                        </button>
                      </div>
                    )}
                  </>
                )}

                {result.rows && result.rows.length === 0 && (
                  <div className="p-8 text-center text-gray-500">
                    Query returned no results
                  </div>
                )}
              </>
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
