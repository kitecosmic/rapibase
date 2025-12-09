import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { tables } from '../lib/api'
import { Table2, Plus, Database, Loader2 } from 'lucide-react'

export default function Dashboard() {
  const { data, isLoading } = useQuery({
    queryKey: ['tables'],
    queryFn: tables.list,
  })

  const tableList = data?.tables || []

  return (
    <div>
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900">Dashboard</h1>
        <p className="text-gray-600 mt-1">Overview of your database</p>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-8">
        <div className="bg-white rounded-xl border border-gray-200 p-6">
          <div className="flex items-center gap-4">
            <div className="p-3 bg-blue-100 rounded-lg">
              <Table2 className="w-6 h-6 text-blue-600" />
            </div>
            <div>
              <p className="text-sm text-gray-600">Total Tables</p>
              <p className="text-2xl font-bold text-gray-900">
                {isLoading ? '-' : tableList.length}
              </p>
            </div>
          </div>
        </div>

        <div className="bg-white rounded-xl border border-gray-200 p-6">
          <div className="flex items-center gap-4">
            <div className="p-3 bg-green-100 rounded-lg">
              <Database className="w-6 h-6 text-green-600" />
            </div>
            <div>
              <p className="text-sm text-gray-600">Total Rows</p>
              <p className="text-2xl font-bold text-gray-900">
                {isLoading ? '-' : tableList.reduce((acc: number, t: any) => acc + (t.row_count || 0), 0).toLocaleString()}
              </p>
            </div>
          </div>
        </div>

        <div className="bg-white rounded-xl border border-gray-200 p-6">
          <Link 
            to="/tables" 
            className="flex items-center gap-4 group"
          >
            <div className="p-3 bg-purple-100 rounded-lg group-hover:bg-purple-200 transition-colors">
              <Plus className="w-6 h-6 text-purple-600" />
            </div>
            <div>
              <p className="text-sm text-gray-600">Quick Action</p>
              <p className="text-lg font-semibold text-purple-600 group-hover:text-purple-700">
                Create Table
              </p>
            </div>
          </Link>
        </div>
      </div>

      {/* Recent Tables */}
      <div className="bg-white rounded-xl border border-gray-200">
        <div className="px-6 py-4 border-b border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900">Tables</h2>
        </div>

        {isLoading ? (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
          </div>
        ) : tableList.length === 0 ? (
          <div className="text-center py-12">
            <Table2 className="w-12 h-12 text-gray-400 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-gray-900 mb-2">No tables yet</h3>
            <p className="text-gray-600 mb-4">Create your first table to get started</p>
            <Link
              to="/tables"
              className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
            >
              <Plus className="w-4 h-4" />
              Create Table
            </Link>
          </div>
        ) : (
          <div className="divide-y divide-gray-200">
            {tableList.map((table: any) => (
              <Link
                key={table.name}
                to={`/tables/${table.name}`}
                className="flex items-center justify-between px-6 py-4 hover:bg-gray-50 transition-colors"
              >
                <div className="flex items-center gap-3">
                  <Table2 className="w-5 h-5 text-gray-400" />
                  <span className="font-medium text-gray-900">{table.name}</span>
                </div>
                <span className="text-sm text-gray-500">
                  {table.row_count?.toLocaleString() || 0} rows
                </span>
              </Link>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
