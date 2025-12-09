import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { tables } from '../lib/api'
import { 
  Table2, 
  Plus, 
  Trash2, 
  Loader2,
  X,
  ChevronRight
} from 'lucide-react'

const COLUMN_TYPES = [
  'SERIAL',
  'BIGSERIAL',
  'INTEGER',
  'BIGINT',
  'TEXT',
  'VARCHAR(255)',
  'BOOLEAN',
  'TIMESTAMP WITH TIME ZONE',
  'DATE',
  'NUMERIC',
  'JSONB',
  'UUID',
]

interface Column {
  name: string
  type: string
  nullable: boolean
  is_primary_key: boolean
  is_unique: boolean
  default_value?: string
}

export default function Tables() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [newTableName, setNewTableName] = useState('')
  const [columns, setColumns] = useState<Column[]>([
    { name: 'id', type: 'SERIAL', nullable: false, is_primary_key: true, is_unique: false }
  ])

  const { data, isLoading } = useQuery({
    queryKey: ['tables'],
    queryFn: tables.list,
  })

  const createMutation = useMutation({
    mutationFn: () => tables.create(newTableName, columns),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tables'] })
      setShowCreate(false)
      setNewTableName('')
      setColumns([{ name: 'id', type: 'SERIAL', nullable: false, is_primary_key: true, is_unique: false }])
    },
  })

  const deleteMutation = useMutation({
    mutationFn: tables.drop,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tables'] })
    },
  })

  const addColumn = () => {
    setColumns([...columns, { name: '', type: 'TEXT', nullable: true, is_primary_key: false, is_unique: false }])
  }

  const removeColumn = (index: number) => {
    setColumns(columns.filter((_, i) => i !== index))
  }

  const updateColumn = (index: number, field: keyof Column, value: any) => {
    const updated = [...columns]
    updated[index] = { ...updated[index], [field]: value }
    setColumns(updated)
  }

  const tableList = data?.tables || []

  return (
    <div>
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Tables</h1>
          <p className="text-gray-600 mt-1">Manage your database tables</p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
        >
          <Plus className="w-4 h-4" />
          Create Table
        </button>
      </div>

      {/* Create Table Modal */}
      {showCreate && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-xl shadow-xl w-full max-w-2xl max-h-[90vh] overflow-hidden">
            <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold">Create New Table</h2>
              <button onClick={() => setShowCreate(false)} className="text-gray-500 hover:text-gray-700">
                <X className="w-5 h-5" />
              </button>
            </div>

            <div className="p-6 overflow-y-auto max-h-[calc(90vh-140px)]">
              <div className="mb-6">
                <label className="block text-sm font-medium text-gray-700 mb-1">Table Name</label>
                <input
                  type="text"
                  value={newTableName}
                  onChange={(e) => setNewTableName(e.target.value)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                  placeholder="users"
                />
              </div>

              <div className="mb-4">
                <div className="flex items-center justify-between mb-2">
                  <label className="block text-sm font-medium text-gray-700">Columns</label>
                  <button
                    onClick={addColumn}
                    className="text-sm text-blue-600 hover:text-blue-700 flex items-center gap-1"
                  >
                    <Plus className="w-4 h-4" />
                    Add Column
                  </button>
                </div>

                <div className="space-y-3">
                  {columns.map((col, index) => (
                    <div key={index} className="flex items-start gap-2 p-3 bg-gray-50 rounded-lg">
                      <div className="flex-1 grid grid-cols-2 gap-2">
                        <input
                          type="text"
                          value={col.name}
                          onChange={(e) => updateColumn(index, 'name', e.target.value)}
                          className="px-2 py-1.5 text-sm border border-gray-300 rounded focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                          placeholder="column_name"
                        />
                        <select
                          value={col.type}
                          onChange={(e) => updateColumn(index, 'type', e.target.value)}
                          className="px-2 py-1.5 text-sm border border-gray-300 rounded focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                        >
                          {COLUMN_TYPES.map((type) => (
                            <option key={type} value={type}>{type}</option>
                          ))}
                        </select>
                      </div>
                      <div className="flex items-center gap-3 text-sm">
                        <label className="flex items-center gap-1">
                          <input
                            type="checkbox"
                            checked={col.is_primary_key}
                            onChange={(e) => updateColumn(index, 'is_primary_key', e.target.checked)}
                            className="rounded"
                          />
                          <span className="text-gray-600">PK</span>
                        </label>
                        <label className="flex items-center gap-1">
                          <input
                            type="checkbox"
                            checked={!col.nullable}
                            onChange={(e) => updateColumn(index, 'nullable', !e.target.checked)}
                            className="rounded"
                          />
                          <span className="text-gray-600">Required</span>
                        </label>
                      </div>
                      <button
                        onClick={() => removeColumn(index)}
                        className="p-1 text-gray-400 hover:text-red-600"
                        disabled={columns.length === 1}
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            </div>

            <div className="flex justify-end gap-3 px-6 py-4 border-t border-gray-200">
              <button
                onClick={() => setShowCreate(false)}
                className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => createMutation.mutate()}
                disabled={!newTableName || columns.some(c => !c.name) || createMutation.isPending}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {createMutation.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
                Create Table
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Tables List */}
      <div className="bg-white rounded-xl border border-gray-200">
        {isLoading ? (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
          </div>
        ) : tableList.length === 0 ? (
          <div className="text-center py-12">
            <Table2 className="w-12 h-12 text-gray-400 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-gray-900 mb-2">No tables yet</h3>
            <p className="text-gray-600">Create your first table to get started</p>
          </div>
        ) : (
          <div className="divide-y divide-gray-200">
            {tableList.map((table: any) => (
              <div
                key={table.name}
                className="flex items-center justify-between px-6 py-4 hover:bg-gray-50 transition-colors"
              >
                <Link
                  to={`/tables/${table.name}`}
                  className="flex items-center gap-3 flex-1"
                >
                  <Table2 className="w-5 h-5 text-gray-400" />
                  <div>
                    <span className="font-medium text-gray-900">{table.name}</span>
                    <span className="ml-3 text-sm text-gray-500">
                      {table.row_count?.toLocaleString() || 0} rows
                    </span>
                  </div>
                </Link>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => {
                      if (confirm(`Are you sure you want to delete table "${table.name}"?`)) {
                        deleteMutation.mutate(table.name)
                      }
                    }}
                    className="p-2 text-gray-400 hover:text-red-600 transition-colors"
                    title="Delete table"
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                  <Link
                    to={`/tables/${table.name}`}
                    className="p-2 text-gray-400 hover:text-gray-600 transition-colors"
                  >
                    <ChevronRight className="w-4 h-4" />
                  </Link>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
