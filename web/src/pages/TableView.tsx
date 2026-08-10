import { useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { tables, importExport } from '../lib/api'
import {
  ArrowLeft,
  Plus,
  Trash2,
  Edit2,
  Save,
  X,
  Download,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Loader2,
  Code,
  Table2,
  Copy,
  Check,
  Filter as FilterIcon,
  ShieldCheck,
} from 'lucide-react'
import TableRlsPanel from '../components/TableRlsPanel'

type TabType = 'data' | 'api' | 'rls'

type RowFilter = { column: string; operator: string; value: string }

const FILTER_OPERATORS: { value: string; label: string; description: string; placeholder?: string }[] = [
  { value: 'eq', label: '=', description: 'equals' },
  { value: 'neq', label: '!=', description: 'not equals' },
  { value: 'gt', label: '>', description: 'greater than' },
  { value: 'gte', label: '>=', description: 'greater or equal' },
  { value: 'lt', label: '<', description: 'less than' },
  { value: 'lte', label: '<=', description: 'less or equal' },
  { value: 'like', label: 'like', description: 'contains, case-sensitive', placeholder: '%text%' },
  { value: 'ilike', label: 'ilike', description: 'contains, case-insensitive', placeholder: '%text%' },
  { value: 'is', label: 'is', description: 'null / true / false', placeholder: 'null | true | false' },
  { value: 'in', label: 'in', description: 'in list', placeholder: 'a,b,c' },
]

function getPageNumbers(current: number, total: number): (number | 'ellipsis')[] {
  if (total <= 1) return [1]
  const delta = 1
  const pages = new Set<number>([1, total, current])
  for (let i = current - delta; i <= current + delta; i++) {
    if (i > 1 && i < total) pages.add(i)
  }
  const sorted = Array.from(pages).sort((a, b) => a - b)
  const result: (number | 'ellipsis')[] = []
  let prev = 0
  for (const p of sorted) {
    if (prev && p - prev > 1) result.push('ellipsis')
    result.push(p)
    prev = p
  }
  return result
}

export default function TableView() {
  const { name } = useParams<{ name: string }>()
  const queryClient = useQueryClient()
  const [activeTab, setActiveTab] = useState<TabType>('data')
  const [page, setPage] = useState(1)
  const [pageInput, setPageInput] = useState('')
  const [editingRow, setEditingRow] = useState<any>(null)
  const [editData, setEditData] = useState<any>({})
  const [showInsert, setShowInsert] = useState(false)
  const [insertData, setInsertData] = useState<any>({})
  const [selectedLang, setSelectedLang] = useState('javascript')
  const [copiedCode, setCopiedCode] = useState<string | null>(null)
  const [draftFilters, setDraftFilters] = useState<RowFilter[]>([])
  const [appliedFilters, setAppliedFilters] = useState<RowFilter[]>([])

  const { data: schema } = useQuery({
    queryKey: ['table-schema', name],
    queryFn: () => tables.get(name!),
    enabled: !!name,
  })

  // Fetch project info (API keys)
  const { data: projectData } = useQuery({
    queryKey: ['project'],
    queryFn: async () => {
      const stored = localStorage.getItem('rapibase-auth')
      if (!stored) return null
      const parsed = JSON.parse(stored)
      const token = parsed?.state?.token
      if (!token) return null
      const res = await fetch('/api/dashboard/project', {
        headers: { Authorization: `Bearer ${token}` }
      })
      if (!res.ok) return null
      return res.json()
    },
  })

  const anonKey = projectData?.anon_key || 'YOUR_ANON_KEY'

  const { data: rowsData, isLoading } = useQuery({
    queryKey: ['table-rows', name, page, appliedFilters],
    queryFn: () => tables.getRows(name!, page, 50, undefined, undefined, appliedFilters),
    enabled: !!name,
  })

  const insertMutation = useMutation({
    mutationFn: (data: any) => tables.insertRow(name!, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['table-rows', name] })
      queryClient.invalidateQueries({ queryKey: ['tables'] })
      setShowInsert(false)
      setInsertData({})
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: any; data: any }) => tables.updateRow(name!, id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['table-rows', name] })
      setEditingRow(null)
      setEditData({})
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: any) => tables.deleteRow(name!, id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['table-rows', name] })
      queryClient.invalidateQueries({ queryKey: ['tables'] })
    },
  })

  const addFilter = () => {
    const firstCol = (schema?.columns || [])[0]?.name || ''
    setDraftFilters([...draftFilters, { column: firstCol, operator: 'ilike', value: '' }])
  }

  const updateFilter = (idx: number, patch: Partial<RowFilter>) => {
    setDraftFilters(draftFilters.map((f, i) => (i === idx ? { ...f, ...patch } : f)))
  }

  const removeFilter = (idx: number) => {
    const next = draftFilters.filter((_, i) => i !== idx)
    setDraftFilters(next)
    // If user removes a filter that was applied, re-sync immediately so the result reflects it.
    if (appliedFilters.length > 0) {
      setAppliedFilters(next)
      setPage(1)
    }
  }

  const applyFilters = () => {
    setAppliedFilters(draftFilters.filter(f => f.column && (f.operator === 'is' || f.value !== '')))
    setPage(1)
  }

  const clearFilters = () => {
    setDraftFilters([])
    setAppliedFilters([])
    setPage(1)
  }

  const goToPage = (n: number) => {
    if (Number.isFinite(n) && n >= 1 && n <= (rowsData?.total_pages || 1)) {
      setPage(n)
      setPageInput('')
    }
  }

  const handleExport = async (format: 'json' | 'sql') => {
    const blob = await importExport.exportTable(name!, format)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${name}.${format}`
    a.click()
    URL.revokeObjectURL(url)
  }

  const columns = schema?.columns || []
  const rows = rowsData?.data || []
  const totalPages = rowsData?.total_pages || 1
  const primaryKey = schema?.primary_key || 'id'
  const baseUrl = window.location.origin

  const copyToClipboard = (code: string, id: string) => {
    navigator.clipboard.writeText(code)
    setCopiedCode(id)
    setTimeout(() => setCopiedCode(null), 2000)
  }

  // Generate API snippets for this table
  // Uses the REST API endpoint /api/v1/rest/:table with apikey
  const getTableSnippets = () => ({
    javascript: {
      name: 'JavaScript',
      select: `// Option 1: Client-side (user authenticated)
const ANON_KEY = '${anonKey}';
const response = await fetch('${baseUrl}/api/v1/rest/${name}', {
  headers: {
    'apikey': ANON_KEY,
    'Authorization': \`Bearer \${token}\`  // JWT from signin
  }
});

// Option 2: Server-side / Admin (no user auth needed)
const SERVICE_KEY = 'YOUR_SERVICE_KEY';  // Keep secret!
const response = await fetch('${baseUrl}/api/v1/rest/${name}', {
  headers: { 'apikey': SERVICE_KEY }
});

const { data, total_rows, page, total_pages } = await response.json();`,

      selectFilter: `// Get rows with pagination
const response = await fetch('${baseUrl}/api/v1/rest/${name}?page=1&limit=50', {
  headers: {
    'apikey': ANON_KEY,
    'Authorization': \`Bearer \${token}\`
  }
});`,

      insert: `// Insert a new row
const response = await fetch('${baseUrl}/api/v1/rest/${name}', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'apikey': ANON_KEY,
    'Authorization': \`Bearer \${token}\`
  },
  body: JSON.stringify({
    ${columns.filter((c: any) => !c.type?.includes('SERIAL')).slice(0, 3).map((c: any) => `${c.name}: 'value'`).join(',\n    ')}
  })
});`,

      update: `// Update a row by ${primaryKey}
const response = await fetch('${baseUrl}/api/v1/rest/${name}/\${${primaryKey}}', {
  method: 'PUT',
  headers: {
    'Content-Type': 'application/json',
    'apikey': ANON_KEY,
    'Authorization': \`Bearer \${token}\`
  },
  body: JSON.stringify({
    ${columns.filter((c: any) => !c.is_primary_key).slice(0, 2).map((c: any) => `${c.name}: 'new_value'`).join(',\n    ')}
  })
});`,

      delete: `// Delete a row by ${primaryKey}
const response = await fetch('${baseUrl}/api/v1/rest/${name}/\${${primaryKey}}', {
  method: 'DELETE',
  headers: {
    'apikey': ANON_KEY,
    'Authorization': \`Bearer \${token}\`
  }
});`
    },
    curl: {
      name: 'cURL',
      select: `# Option 1: Client-side (user authenticated)
curl ${baseUrl}/api/v1/rest/${name} \\
  -H "apikey: ${anonKey}" \\
  -H "Authorization: Bearer YOUR_TOKEN"

# Option 2: Server-side / Admin (no user auth needed)
curl ${baseUrl}/api/v1/rest/${name} \\
  -H "apikey: YOUR_SERVICE_KEY"`,

      selectFilter: `# Get rows with pagination
curl "${baseUrl}/api/v1/rest/${name}?page=1&limit=50" \\
  -H "apikey: ${anonKey}" \\
  -H "Authorization: Bearer YOUR_TOKEN"`,

      insert: `# Insert a new row
curl -X POST ${baseUrl}/api/v1/rest/${name} \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -H "Authorization: Bearer YOUR_TOKEN" \\
  -d '{ ${columns.filter((c: any) => !c.type?.includes('SERIAL')).slice(0, 2).map((c: any) => `"${c.name}": "value"`).join(', ')} }'`,

      update: `# Update a row
curl -X PUT ${baseUrl}/api/v1/rest/${name}/ID \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -H "Authorization: Bearer YOUR_TOKEN" \\
  -d '{ ${columns.filter((c: any) => !c.is_primary_key).slice(0, 2).map((c: any) => `"${c.name}": "new_value"`).join(', ')} }'`,

      delete: `# Delete a row
curl -X DELETE ${baseUrl}/api/v1/rest/${name}/ID \\
  -H "apikey: ${anonKey}" \\
  -H "Authorization: Bearer YOUR_TOKEN"`
    },
    python: {
      name: 'Python',
      select: `import requests

ANON_KEY = '${anonKey}'

# Get all rows from ${name}
headers = {
    'apikey': ANON_KEY,
    'Authorization': f'Bearer {token}'
}
response = requests.get('${baseUrl}/api/v1/rest/${name}', headers=headers)
data = response.json()`,

      selectFilter: `# Get rows with pagination
response = requests.get(
    '${baseUrl}/api/v1/rest/${name}',
    params={'page': 1, 'limit': 50},
    headers=headers
)`,

      insert: `# Insert a new row
response = requests.post(
    '${baseUrl}/api/v1/rest/${name}',
    headers=headers,
    json={
        ${columns.filter((c: any) => !c.type?.includes('SERIAL')).slice(0, 3).map((c: any) => `'${c.name}': 'value'`).join(',\n        ')}
    }
)`,

      update: `# Update a row
response = requests.put(
    f'${baseUrl}/api/v1/rest/${name}/{${primaryKey}}',
    headers=headers,
    json={
        ${columns.filter((c: any) => !c.is_primary_key).slice(0, 2).map((c: any) => `'${c.name}': 'new_value'`).join(',\n        ')}
    }
)`,

      delete: `# Delete a row
response = requests.delete(
    f'${baseUrl}/api/v1/rest/${name}/{${primaryKey}}',
    headers=headers
)`
    },
    go: {
      name: 'Go',
      select: `package main

import (
    "net/http"
    "io"
    "fmt"
)

const ANON_KEY = "${anonKey}"

// Get all rows from ${name}
req, _ := http.NewRequest("GET", "${baseUrl}/api/v1/rest/${name}", nil)
req.Header.Set("apikey", ANON_KEY)
req.Header.Set("Authorization", "Bearer "+token)

client := &http.Client{}
resp, _ := client.Do(req)
defer resp.Body.Close()

body, _ := io.ReadAll(resp.Body)
fmt.Println(string(body))`,

      selectFilter: `// Get rows with pagination
req, _ := http.NewRequest("GET", "${baseUrl}/api/v1/rest/${name}?page=1&limit=50", nil)
req.Header.Set("apikey", ANON_KEY)
req.Header.Set("Authorization", "Bearer "+token)`,

      insert: `// Insert a new row
import "bytes"

data := []byte(\`{"${columns.filter((c: any) => !c.type?.includes('SERIAL')).slice(0, 2).map((c: any) => `${c.name}`).join('": "value", "')}": "value"}\`)
req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/rest/${name}", bytes.NewBuffer(data))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("apikey", ANON_KEY)
req.Header.Set("Authorization", "Bearer "+token)

resp, _ := client.Do(req)`,

      update: `// Update a row
data := []byte(\`{"${columns.filter((c: any) => !c.is_primary_key).slice(0, 2).map((c: any) => `${c.name}`).join('": "new_value", "')}": "new_value"}\`)
req, _ := http.NewRequest("PUT", "${baseUrl}/api/v1/rest/${name}/"+id, bytes.NewBuffer(data))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("apikey", ANON_KEY)
req.Header.Set("Authorization", "Bearer "+token)

resp, _ := client.Do(req)`,

      delete: `// Delete a row
req, _ := http.NewRequest("DELETE", "${baseUrl}/api/v1/rest/${name}/"+id, nil)
req.Header.Set("apikey", ANON_KEY)
req.Header.Set("Authorization", "Bearer "+token)

resp, _ := client.Do(req)`
    },
    php: {
      name: 'PHP',
      select: `<?php
$ANON_KEY = '${anonKey}';

// Get all rows from ${name}
$ch = curl_init('${baseUrl}/api/v1/rest/${name}');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'apikey: ' . $ANON_KEY,
    'Authorization: Bearer ' . $token
]);

$response = curl_exec($ch);
$data = json_decode($response, true);
curl_close($ch);`,

      selectFilter: `// Get rows with pagination
$ch = curl_init('${baseUrl}/api/v1/rest/${name}?page=1&limit=50');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'apikey: ' . $ANON_KEY,
    'Authorization: Bearer ' . $token
]);`,

      insert: `// Insert a new row
$ch = curl_init('${baseUrl}/api/v1/rest/${name}');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY,
    'Authorization: Bearer ' . $token
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    ${columns.filter((c: any) => !c.type?.includes('SERIAL')).slice(0, 3).map((c: any) => `'${c.name}' => 'value'`).join(',\n    ')}
]));

$response = curl_exec($ch);`,

      update: `// Update a row
$ch = curl_init('${baseUrl}/api/v1/rest/${name}/' . $id);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_CUSTOMREQUEST, 'PUT');
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY,
    'Authorization: Bearer ' . $token
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    ${columns.filter((c: any) => !c.is_primary_key).slice(0, 2).map((c: any) => `'${c.name}' => 'new_value'`).join(',\n    ')}
]));

$response = curl_exec($ch);`,

      delete: `// Delete a row
$ch = curl_init('${baseUrl}/api/v1/rest/${name}/' . $id);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_CUSTOMREQUEST, 'DELETE');
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'apikey: ' . $ANON_KEY,
    'Authorization: Bearer ' . $token
]);

curl_exec($ch);
curl_close($ch);`
    }
  })

  const snippets = getTableSnippets()
  const currentSnippets = snippets[selectedLang as keyof typeof snippets]

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-4">
          <Link
            to="/tables"
            className="p-2 text-gray-500 hover:text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
          >
            <ArrowLeft className="w-5 h-5" />
          </Link>
          <div>
            <h1 className="text-2xl font-bold text-gray-900">{name}</h1>
            <p className="text-gray-600 text-sm">
              {rowsData?.total_rows?.toLocaleString() || 0} rows · {columns.length} columns
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <div className="relative group">
            <button className="flex items-center gap-2 px-3 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors">
              <Download className="w-4 h-4" />
              Export
            </button>
            <div className="absolute right-0 mt-1 w-32 bg-white border border-gray-200 rounded-lg shadow-lg opacity-0 invisible group-hover:opacity-100 group-hover:visible transition-all z-10">
              <button
                onClick={() => handleExport('json')}
                className="w-full px-4 py-2 text-left text-sm hover:bg-gray-50"
              >
                JSON
              </button>
              <button
                onClick={() => handleExport('sql')}
                className="w-full px-4 py-2 text-left text-sm hover:bg-gray-50"
              >
                SQL
              </button>
            </div>
          </div>
          {activeTab === 'data' && (
            <button
              onClick={() => setShowInsert(true)}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
            >
              <Plus className="w-4 h-4" />
              Insert Row
            </button>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 mb-6 bg-gray-100 p-1 rounded-lg w-fit">
        <button
          onClick={() => setActiveTab('data')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'data' 
              ? 'bg-white text-gray-900 shadow-sm' 
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <span className="flex items-center gap-2">
            <Table2 className="w-4 h-4" />
            Data
          </span>
        </button>
        <button
          onClick={() => setActiveTab('api')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'api'
              ? 'bg-white text-gray-900 shadow-sm'
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <span className="flex items-center gap-2">
            <Code className="w-4 h-4" />
            API Docs
          </span>
        </button>
        <button
          onClick={() => setActiveTab('rls')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'rls'
              ? 'bg-white text-gray-900 shadow-sm'
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <span className="flex items-center gap-2">
            <ShieldCheck className="w-4 h-4" />
            RLS
          </span>
        </button>
      </div>

      {activeTab === 'rls' && (
        <div className="bg-white rounded-xl border border-gray-200 p-6">
          <TableRlsPanel table={name!} columns={columns} />
        </div>
      )}

      {/* Insert Modal */}
      {showInsert && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-xl shadow-xl w-full max-w-lg max-h-[90vh] overflow-hidden">
            <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold">Insert Row</h2>
              <button onClick={() => setShowInsert(false)} className="text-gray-500 hover:text-gray-700">
                <X className="w-5 h-5" />
              </button>
            </div>
            <div className="p-6 overflow-y-auto max-h-[calc(90vh-140px)] space-y-4">
              {columns
                .filter((col: any) => !col.type.includes('SERIAL'))
                .map((col: any) => (
                  <div key={col.name}>
                    <label className="block text-sm font-medium text-gray-700 mb-1">
                      {col.name}
                      {!col.nullable && <span className="text-red-500 ml-1">*</span>}
                      <span className="text-gray-400 font-normal ml-2">{col.type}</span>
                    </label>
                    <input
                      type="text"
                      value={insertData[col.name] || ''}
                      onChange={(e) => setInsertData({ ...insertData, [col.name]: e.target.value })}
                      className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                    />
                  </div>
                ))}
            </div>
            <div className="flex justify-end gap-3 px-6 py-4 border-t border-gray-200">
              <button
                onClick={() => setShowInsert(false)}
                className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => insertMutation.mutate(insertData)}
                disabled={insertMutation.isPending}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors"
              >
                {insertMutation.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
                Insert
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Filters bar (Data tab only) */}
      {activeTab === 'data' && (
        <div className="bg-white border border-gray-200 rounded-xl mb-4 p-4">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2">
              <FilterIcon className="w-4 h-4 text-gray-500" />
              <span className="text-sm font-medium text-gray-700">Filters</span>
              {appliedFilters.length > 0 && (
                <span className="text-xs bg-blue-100 text-blue-700 px-2 py-0.5 rounded">
                  {appliedFilters.length} active
                </span>
              )}
            </div>
            <button
              onClick={addFilter}
              className="flex items-center gap-1 px-2 py-1 text-xs text-blue-700 hover:bg-blue-50 rounded transition-colors"
            >
              <Plus className="w-3 h-3" />
              Add filter
            </button>
          </div>
          {draftFilters.length === 0 ? (
            <p className="text-xs text-gray-500">No filters. Click "Add filter" to narrow the results.</p>
          ) : (
            <div className="space-y-2">
              {draftFilters.map((f, idx) => {
                const op = FILTER_OPERATORS.find(o => o.value === f.operator)
                return (
                  <div key={idx}>
                    <div className="flex items-center gap-2 flex-wrap">
                      <select
                        value={f.column}
                        onChange={(e) => updateFilter(idx, { column: e.target.value })}
                        className="px-2 py-1.5 border border-gray-300 rounded-md text-sm focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none min-w-[10rem]"
                      >
                        <option value="">column…</option>
                        {columns.map((c: any) => (
                          <option key={c.name} value={c.name}>{c.name}</option>
                        ))}
                      </select>
                      <select
                        value={f.operator}
                        onChange={(e) => updateFilter(idx, { operator: e.target.value })}
                        className="px-2 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none min-w-[20rem]"
                      >
                        {FILTER_OPERATORS.map(o => (
                          <option key={o.value} value={o.value}>
                            {o.label.padEnd(7, ' ')}{o.description}
                          </option>
                        ))}
                      </select>
                      <input
                        type="text"
                        value={f.value}
                        placeholder={op?.placeholder || 'value'}
                        onChange={(e) => updateFilter(idx, { value: e.target.value })}
                        onKeyDown={(e) => { if (e.key === 'Enter') applyFilters() }}
                        className="flex-1 min-w-[12rem] px-2 py-1.5 border border-gray-300 rounded-md text-sm focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                      />
                      <button
                        onClick={() => removeFilter(idx)}
                        className="p-1.5 text-gray-400 hover:text-red-600 hover:bg-red-50 rounded transition-colors"
                        title="Remove filter"
                      >
                        <X className="w-4 h-4" />
                      </button>
                    </div>
                    {op && (
                      <p className="text-[11px] text-gray-500 mt-1 ml-1">
                        {op.value === 'eq' && 'Exact match. Case-sensitive.'}
                        {op.value === 'neq' && 'Excludes rows where the column equals this value.'}
                        {op.value === 'gt' && 'Numeric or date comparison.'}
                        {op.value === 'gte' && 'Numeric or date comparison, inclusive.'}
                        {op.value === 'lt' && 'Numeric or date comparison.'}
                        {op.value === 'lte' && 'Numeric or date comparison, inclusive.'}
                        {op.value === 'like' && 'Case-sensitive text search. Use % as wildcard, e.g. %juan%.'}
                        {op.value === 'ilike' && 'Case-insensitive text search. Use % as wildcard, e.g. %juan% matches "Juan", "JUAN", "juancho".'}
                        {op.value === 'is' && 'Special checks: type "null", "true" or "false".'}
                        {op.value === 'in' && 'Matches any value from a comma-separated list, e.g. active,pending.'}
                      </p>
                    )}
                  </div>
                )
              })}
              <div className="flex items-center gap-2 pt-1">
                <button
                  onClick={applyFilters}
                  className="px-3 py-1.5 text-sm bg-blue-600 text-white rounded-md hover:bg-blue-700 transition-colors"
                >
                  Apply
                </button>
                <button
                  onClick={clearFilters}
                  className="px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-100 rounded-md transition-colors"
                >
                  Clear
                </button>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Data Tab */}
      {activeTab === 'data' && (
        <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="bg-gray-50 border-b border-gray-200">
                  {columns.map((col: any) => (
                    <th
                      key={col.name}
                      className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider"
                    >
                      {col.name}
                      {col.is_primary_key && (
                        <span className="ml-1 text-blue-500">PK</span>
                      )}
                    </th>
                  ))}
                  <th className="px-4 py-3 text-right text-xs font-medium text-gray-500 uppercase tracking-wider w-24">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200">
                {isLoading ? (
                  <tr>
                    <td colSpan={columns.length + 1} className="px-4 py-12 text-center">
                      <Loader2 className="w-8 h-8 text-blue-600 animate-spin mx-auto" />
                    </td>
                  </tr>
                ) : rows.length === 0 ? (
                  <tr>
                    <td colSpan={columns.length + 1} className="px-4 py-12 text-center text-gray-500">
                      No data
                    </td>
                  </tr>
                ) : (
                  rows.map((row: any, rowIndex: number) => (
                    <tr key={rowIndex} className="hover:bg-gray-50">
                      {columns.map((col: any) => (
                        <td key={col.name} className="px-4 py-3 text-sm text-gray-900">
                          {editingRow === row[primaryKey] ? (
                            <input
                              type="text"
                              value={editData[col.name] ?? row[col.name] ?? ''}
                              onChange={(e) => setEditData({ ...editData, [col.name]: e.target.value })}
                              className={`w-full px-2 py-1 border border-gray-300 rounded text-sm ${
                                col.is_primary_key || col.name === 'created_at' || col.name === 'updated_at' || col.type.includes('SERIAL')
                                  ? 'bg-gray-100 text-gray-500 cursor-not-allowed'
                                  : ''
                              }`}
                              disabled={col.is_primary_key || col.name === 'created_at' || col.name === 'updated_at' || col.type.includes('SERIAL')}
                            />
                          ) : (
                            <span className="truncate block max-w-xs">
                              {row[col.name] === null ? (
                                <span className="text-gray-400 italic">null</span>
                              ) : typeof row[col.name] === 'object' ? (
                                JSON.stringify(row[col.name])
                              ) : (
                                String(row[col.name])
                              )}
                            </span>
                          )}
                        </td>
                      ))}
                      <td className="px-4 py-3 text-right">
                        {editingRow === row[primaryKey] ? (
                          <div className="flex items-center justify-end gap-1">
                            <button
                              onClick={() => updateMutation.mutate({ id: row[primaryKey], data: editData })}
                              className="p-1 text-green-600 hover:bg-green-50 rounded"
                            >
                              <Save className="w-4 h-4" />
                            </button>
                            <button
                              onClick={() => {
                                setEditingRow(null)
                                setEditData({})
                              }}
                              className="p-1 text-gray-500 hover:bg-gray-100 rounded"
                            >
                              <X className="w-4 h-4" />
                            </button>
                          </div>
                        ) : (
                          <div className="flex items-center justify-end gap-1">
                            <button
                              onClick={() => {
                                setEditingRow(row[primaryKey])
                                setEditData(row)
                              }}
                              className="p-1 text-gray-500 hover:bg-gray-100 rounded"
                            >
                              <Edit2 className="w-4 h-4" />
                            </button>
                            <button
                              onClick={() => {
                                if (confirm('Delete this row?')) {
                                  deleteMutation.mutate(row[primaryKey])
                                }
                              }}
                              className="p-1 text-gray-500 hover:text-red-600 hover:bg-red-50 rounded"
                            >
                              <Trash2 className="w-4 h-4" />
                            </button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          {totalPages > 1 && (
            <div className="flex items-center justify-between px-4 py-3 border-t border-gray-200 flex-wrap gap-3">
              <p className="text-sm text-gray-600">
                Page {page} of {totalPages} · {(rowsData?.total_rows || 0).toLocaleString()} rows
              </p>
              <div className="flex items-center gap-1 flex-wrap">
                <button
                  onClick={() => goToPage(1)}
                  disabled={page === 1}
                  className="p-1.5 text-gray-500 hover:bg-gray-100 rounded disabled:opacity-50 disabled:cursor-not-allowed"
                  title="First page"
                >
                  <ChevronsLeft className="w-4 h-4" />
                </button>
                <button
                  onClick={() => goToPage(page - 1)}
                  disabled={page === 1}
                  className="p-1.5 text-gray-500 hover:bg-gray-100 rounded disabled:opacity-50 disabled:cursor-not-allowed"
                  title="Previous page"
                >
                  <ChevronLeft className="w-4 h-4" />
                </button>
                {getPageNumbers(page, totalPages).map((p, idx) =>
                  p === 'ellipsis' ? (
                    <span key={`e-${idx}`} className="px-2 text-gray-400 select-none">…</span>
                  ) : (
                    <button
                      key={p}
                      onClick={() => goToPage(p)}
                      className={`min-w-[2rem] px-2 py-1 text-sm rounded transition-colors ${
                        p === page
                          ? 'bg-blue-600 text-white'
                          : 'text-gray-700 hover:bg-gray-100'
                      }`}
                    >
                      {p}
                    </button>
                  )
                )}
                <button
                  onClick={() => goToPage(page + 1)}
                  disabled={page === totalPages}
                  className="p-1.5 text-gray-500 hover:bg-gray-100 rounded disabled:opacity-50 disabled:cursor-not-allowed"
                  title="Next page"
                >
                  <ChevronRight className="w-4 h-4" />
                </button>
                <button
                  onClick={() => goToPage(totalPages)}
                  disabled={page === totalPages}
                  className="p-1.5 text-gray-500 hover:bg-gray-100 rounded disabled:opacity-50 disabled:cursor-not-allowed"
                  title="Last page"
                >
                  <ChevronsRight className="w-4 h-4" />
                </button>
                <div className="flex items-center gap-1 ml-2 pl-3 border-l border-gray-200">
                  <span className="text-xs text-gray-500">Go to</span>
                  <input
                    type="number"
                    min={1}
                    max={totalPages}
                    value={pageInput}
                    placeholder={String(page)}
                    onChange={(e) => setPageInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') goToPage(parseInt(pageInput, 10))
                    }}
                    className="w-16 px-2 py-1 border border-gray-300 rounded text-sm text-center focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                  />
                </div>
              </div>
            </div>
          )}
        </div>
      )}

      {/* API Docs Tab */}
      {activeTab === 'api' && (
        <div className="space-y-6">
          {/* Language Selector */}
          <div className="flex items-center gap-4">
            <span className="text-sm font-medium text-gray-700">Language:</span>
            <select
              value={selectedLang}
              onChange={(e) => setSelectedLang(e.target.value)}
              className="px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
            >
              {Object.entries(snippets).map(([key, value]) => (
                <option key={key} value={key}>{value.name}</option>
              ))}
            </select>
          </div>

          {/* API Keys Info */}
          <div className="bg-gradient-to-r from-blue-50 to-purple-50 rounded-xl border border-blue-200 p-6">
            <h3 className="text-lg font-semibold text-gray-900 mb-3">🔑 API Keys</h3>
            <div className="grid md:grid-cols-2 gap-4">
              <div className="bg-white rounded-lg p-4 border border-blue-100">
                <h4 className="font-medium text-blue-700 mb-2">Anon Key (Public)</h4>
                <p className="text-sm text-gray-600 mb-2">For client-side apps. Requires user JWT for authenticated access.</p>
                <code className="text-xs bg-blue-50 px-2 py-1 rounded block truncate">{anonKey}</code>
              </div>
              <div className="bg-white rounded-lg p-4 border border-purple-100">
                <h4 className="font-medium text-purple-700 mb-2">Service Key (Secret)</h4>
                <p className="text-sm text-gray-600 mb-2">For server-side/admin. Full access without user JWT.</p>
                <p className="text-xs text-red-600">⚠️ Never expose in client-side code. Use for dashboards, scripts, etc.</p>
              </div>
            </div>
          </div>

          {/* Schema Info */}
          <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">Table Schema</h2>
            </div>
            <div className="p-6">
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-gray-500">
                      <th className="pb-2 font-medium">Column</th>
                      <th className="pb-2 font-medium">Type</th>
                      <th className="pb-2 font-medium">Nullable</th>
                      <th className="pb-2 font-medium">Default</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {columns.map((col: any) => (
                      <tr key={col.name}>
                        <td className="py-2 font-mono text-blue-600">
                          {col.name}
                          {col.is_primary_key && <span className="ml-2 text-xs bg-blue-100 text-blue-700 px-1.5 py-0.5 rounded">PK</span>}
                        </td>
                        <td className="py-2 font-mono text-gray-600">{col.type}</td>
                        <td className="py-2">{col.nullable ? 'Yes' : 'No'}</td>
                        <td className="py-2 font-mono text-gray-500 text-xs">{col.default_value || '-'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>

          {/* Select */}
          <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">Read Rows</h2>
              <p className="text-sm text-gray-600 mt-1">Fetch data from the table with pagination support.</p>
            </div>
            <div className="p-6 space-y-4">
              <div className="mb-4">
                <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-blue-100 text-blue-700">GET</span>
                <code className="ml-2 text-sm text-gray-700">/api/v1/rest/{name}</code>
              </div>
              <div className="relative">
                <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                  <code>{currentSnippets.select}</code>
                </pre>
                <button
                  onClick={() => copyToClipboard(currentSnippets.select, 'select')}
                  className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                >
                  {copiedCode === 'select' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                </button>
              </div>
            </div>
          </div>

          {/* Query Parameters */}
          <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">Query Parameters</h2>
              <p className="text-sm text-gray-600 mt-1">Filter, paginate, and sort your data.</p>
            </div>
            <div className="p-6 space-y-6">
              {/* Pagination */}
              <div>
                <h3 className="text-sm font-semibold text-gray-900 mb-2">Pagination</h3>
                <div className="bg-gray-50 rounded-lg p-4 space-y-2">
                  <div className="flex items-start gap-2">
                    <code className="text-xs bg-gray-200 px-2 py-1 rounded">page</code>
                    <span className="text-sm text-gray-600">Page number (default: 1)</span>
                  </div>
                  <div className="flex items-start gap-2">
                    <code className="text-xs bg-gray-200 px-2 py-1 rounded">page_size</code>
                    <span className="text-sm text-gray-600">Rows per page (default: 50, max: 1000)</span>
                  </div>
                </div>
                <pre className="bg-gray-900 text-gray-100 p-3 rounded-lg text-xs mt-2 overflow-x-auto">
                  <code>{`GET /api/v1/rest/${name}?page=2&page_size=25`}</code>
                </pre>
              </div>

              {/* Ordering */}
              <div>
                <h3 className="text-sm font-semibold text-gray-900 mb-2">Ordering</h3>
                <div className="bg-gray-50 rounded-lg p-4 space-y-2">
                  <div className="flex items-start gap-2">
                    <code className="text-xs bg-gray-200 px-2 py-1 rounded">order_by</code>
                    <span className="text-sm text-gray-600">Column to sort by</span>
                  </div>
                  <div className="flex items-start gap-2">
                    <code className="text-xs bg-gray-200 px-2 py-1 rounded">order</code>
                    <span className="text-sm text-gray-600">Sort direction: <code className="text-xs">asc</code> or <code className="text-xs">desc</code></span>
                  </div>
                </div>
                <pre className="bg-gray-900 text-gray-100 p-3 rounded-lg text-xs mt-2 overflow-x-auto">
                  <code>{`GET /api/v1/rest/${name}?order_by=created_at&order=desc`}</code>
                </pre>
              </div>

              {/* Select Columns */}
              <div>
                <h3 className="text-sm font-semibold text-gray-900 mb-2">Select Columns</h3>
                <div className="bg-gray-50 rounded-lg p-4">
                  <div className="flex items-start gap-2">
                    <code className="text-xs bg-gray-200 px-2 py-1 rounded">select</code>
                    <span className="text-sm text-gray-600">Comma-separated column names to return</span>
                  </div>
                </div>
                <pre className="bg-gray-900 text-gray-100 p-3 rounded-lg text-xs mt-2 overflow-x-auto">
                  <code>{`GET /api/v1/rest/${name}?select=id,name,price`}</code>
                </pre>
              </div>

              {/* Filtering */}
              <div>
                <h3 className="text-sm font-semibold text-gray-900 mb-2">Filtering</h3>
                <p className="text-sm text-gray-600 mb-3">Use <code className="text-xs bg-gray-200 px-1 rounded">column.operator=value</code> format</p>
                <div className="bg-gray-50 rounded-lg p-4">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="text-left text-gray-500">
                        <th className="pb-2 font-medium">Operator</th>
                        <th className="pb-2 font-medium">Description</th>
                        <th className="pb-2 font-medium">Example</th>
                      </tr>
                    </thead>
                    <tbody className="text-gray-700">
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">eq</code></td><td>Equals</td><td><code className="text-xs">status.eq=active</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">neq</code></td><td>Not equals</td><td><code className="text-xs">status.neq=deleted</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">gt</code></td><td>Greater than</td><td><code className="text-xs">price.gt=100</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">gte</code></td><td>Greater or equal</td><td><code className="text-xs">price.gte=100</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">lt</code></td><td>Less than</td><td><code className="text-xs">stock.lt=10</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">lte</code></td><td>Less or equal</td><td><code className="text-xs">stock.lte=10</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">like</code></td><td>Pattern match (case-sensitive)</td><td><code className="text-xs">name.like=%phone%</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">ilike</code></td><td>Pattern match (case-insensitive)</td><td><code className="text-xs">name.ilike=%phone%</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">is</code></td><td>NULL/boolean check</td><td><code className="text-xs">deleted_at.is=null</code></td></tr>
                      <tr><td className="py-1"><code className="text-xs bg-blue-100 px-1 rounded">in</code></td><td>In list</td><td><code className="text-xs">category.in=Electronics,Furniture</code></td></tr>
                    </tbody>
                  </table>
                </div>
                <pre className="bg-gray-900 text-gray-100 p-3 rounded-lg text-xs mt-2 overflow-x-auto">
                  <code>{`# Multiple filters (AND)
GET /api/v1/rest/${name}?category.eq=Electronics&price.gte=100&price.lte=500

# Search with pattern
GET /api/v1/rest/${name}?name.ilike=%wireless%

# Combined with pagination and ordering
GET /api/v1/rest/${name}?is_active.eq=true&order_by=price&order=asc&page=1&page_size=20`}</code>
                </pre>
              </div>
            </div>
          </div>

          {/* Insert */}
          <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">Insert Row</h2>
              <p className="text-sm text-gray-600 mt-1">Add a new row to the table.</p>
            </div>
            <div className="p-6">
              <div className="mb-4">
                <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-green-100 text-green-700">POST</span>
                <code className="ml-2 text-sm text-gray-700">/api/v1/rest/{name}</code>
              </div>
              <div className="relative">
                <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                  <code>{currentSnippets.insert}</code>
                </pre>
                <button
                  onClick={() => copyToClipboard(currentSnippets.insert, 'insert')}
                  className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                >
                  {copiedCode === 'insert' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                </button>
              </div>
            </div>
          </div>

          {/* Update */}
          <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">Update Row</h2>
              <p className="text-sm text-gray-600 mt-1">Update an existing row by its primary key.</p>
            </div>
            <div className="p-6">
              <div className="mb-4">
                <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-yellow-100 text-yellow-700">PUT</span>
                <code className="ml-2 text-sm text-gray-700">/api/v1/rest/{name}/:id</code>
              </div>
              <div className="relative">
                <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                  <code>{currentSnippets.update}</code>
                </pre>
                <button
                  onClick={() => copyToClipboard(currentSnippets.update, 'update')}
                  className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                >
                  {copiedCode === 'update' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                </button>
              </div>
            </div>
          </div>

          {/* Delete */}
          <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">Delete Row</h2>
              <p className="text-sm text-gray-600 mt-1">Remove a row from the table.</p>
            </div>
            <div className="p-6">
              <div className="mb-4">
                <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-red-100 text-red-700">DELETE</span>
                <code className="ml-2 text-sm text-gray-700">/api/v1/rest/{name}/:id</code>
              </div>
              <div className="relative">
                <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                  <code>{currentSnippets.delete}</code>
                </pre>
                <button
                  onClick={() => copyToClipboard(currentSnippets.delete, 'delete')}
                  className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                >
                  {copiedCode === 'delete' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                </button>
              </div>
            </div>
          </div>

          {/* MCP — use this table from an AI agent */}
          <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <div className="px-6 py-4 border-b border-gray-200 flex items-start justify-between gap-4">
              <div>
                <h2 className="text-lg font-semibold text-gray-900">Use from an AI agent (MCP)</h2>
                <p className="text-sm text-gray-600 mt-1">
                  The built-in MCP server lets Claude, ChatGPT or any MCP-compatible agent query this table. See the full reference in <a href={`${import.meta.env.BASE_URL}docs`} className="text-blue-600 hover:underline">Docs → MCP for AI Agents</a>.
                </p>
              </div>
              <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-orange-100 text-orange-700 whitespace-nowrap">/mcp</span>
            </div>
            <div className="p-6 space-y-4">
              <div>
                <h3 className="text-sm font-semibold text-gray-700 mb-2">Connect Claude Desktop</h3>
                <div className="relative">
                  <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-xs overflow-x-auto">
                    <code>{`{
  "mcpServers": {
    "rapibase": {
      "type": "http",
      "url": "${projectData?.app_url || 'https://yourdomain.com'}/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_SERVICE_KEY"
      }
    }
  }
}`}</code>
                  </pre>
                  <button
                    onClick={() => copyToClipboard(`{
  "mcpServers": {
    "rapibase": {
      "type": "http",
      "url": "${projectData?.app_url || 'https://yourdomain.com'}/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_SERVICE_KEY"
      }
    }
  }
}`, 'mcp-config')}
                    className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                  >
                    {copiedCode === 'mcp-config' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                  </button>
                </div>
                <p className="text-xs text-gray-500 mt-1">
                  Replace <code className="font-mono">YOUR_SERVICE_KEY</code> with your service key. <code className="font-mono">apikey: YOUR_SERVICE_KEY</code> works as an alternative header.
                </p>
              </div>

              <div>
                <h3 className="text-sm font-semibold text-gray-700 mb-2">Sample prompts for the agent</h3>
                <ul className="text-sm text-gray-700 space-y-1 list-disc pl-5">
                  <li>"Show me the schema and first 10 rows of <code className="font-mono">{name}</code>."</li>
                  <li>"Find rows in <code className="font-mono">{name}</code> where <em>(your filter)</em>, ordered by <em>(column)</em>."</li>
                  <li>"Insert a new row into <code className="font-mono">{name}</code> with these values: ..."</li>
                </ul>
                <p className="text-xs text-gray-500 mt-2">
                  Behind the scenes the agent calls <code className="font-mono">describe_table</code>, <code className="font-mono">query_rows</code>, <code className="font-mono">insert_row</code> etc., always passing <code className="font-mono">"table": "{name}"</code>.
                </p>
              </div>

              <div>
                <h3 className="text-sm font-semibold text-gray-700 mb-2">Test from curl</h3>
                <div className="relative">
                  <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-xs overflow-x-auto">
                    <code>{`curl -s -X POST ${projectData?.app_url || 'https://yourdomain.com'}/mcp \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H "Authorization: Bearer $SERVICE_KEY" \\
  -d '{
    "jsonrpc":"2.0","id":1,"method":"tools/call",
    "params":{"name":"query_rows","arguments":{"table":"${name}","page_size":5}}
  }'`}</code>
                  </pre>
                  <button
                    onClick={() => copyToClipboard(`curl -s -X POST ${projectData?.app_url || 'https://yourdomain.com'}/mcp \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H "Authorization: Bearer $SERVICE_KEY" \\
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"query_rows","arguments":{"table":"${name}","page_size":5}}}'`, 'mcp-curl')}
                    className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                  >
                    {copiedCode === 'mcp-curl' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
