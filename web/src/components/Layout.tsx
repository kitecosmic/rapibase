import { Outlet, Link, useLocation } from 'react-router-dom'
import { useAuthStore } from '../store/auth'
import { useQuery } from '@tanstack/react-query'
import { tables } from '../lib/api'
import {
  Database,
  Table2,
  Terminal,
  Upload,
  Shield,
  LogOut,
  Menu,
  X,
  FileText,
  Webhook,
  Bell,
  ChevronLeft,
  ChevronRight,
  HardDrive,
  ChevronDown,
  Radio
} from 'lucide-react'
import { useState, useEffect } from 'react'

const navigation = [
  { name: 'Dashboard', href: '/', icon: Database },
  { name: 'Authentication', href: '/auth', icon: Shield },
  { name: 'Tables', href: '/tables', icon: Table2 },
  { name: 'Storage', href: '/storage', icon: HardDrive },
  { name: 'Webhooks', href: '/webhooks', icon: Webhook },
  { name: 'Realtime', href: '/realtime', icon: Radio },
  { name: 'Notifications', href: '/notifications', icon: Bell },
  { name: 'SQL Editor', href: '/sql', icon: Terminal },
  { name: 'Import', href: '/import', icon: Upload },
  { name: 'Documentation', href: '/docs', icon: FileText },
]

const SIDEBAR_COLLAPSED_KEY = 'rapibase-sidebar-collapsed'
const TABLES_EXPANDED_KEY = 'rapibase-tables-expanded'

export default function Layout() {
  const { user, logout } = useAuthStore()
  const location = useLocation()
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => {
    const saved = localStorage.getItem(SIDEBAR_COLLAPSED_KEY)
    return saved === 'true'
  })
  const [tablesExpanded, setTablesExpanded] = useState(() => {
    const saved = localStorage.getItem(TABLES_EXPANDED_KEY)
    return saved !== 'false' // default to true
  })

  const { data: tablesData } = useQuery({
    queryKey: ['tables'],
    queryFn: tables.list,
  })

  const tableList = tablesData?.tables || []

  useEffect(() => {
    localStorage.setItem(SIDEBAR_COLLAPSED_KEY, String(collapsed))
  }, [collapsed])

  useEffect(() => {
    localStorage.setItem(TABLES_EXPANDED_KEY, String(tablesExpanded))
  }, [tablesExpanded])

  return (
    <div className="min-h-screen bg-gray-50">
      {/* Mobile sidebar backdrop */}
      {sidebarOpen && (
        <div 
          className="fixed inset-0 bg-black/50 z-40 lg:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside className={`
        fixed top-0 left-0 z-50 h-full bg-white border-r border-gray-200 
        transform transition-all duration-200 ease-in-out
        ${collapsed ? 'w-16' : 'w-64'}
        lg:translate-x-0 ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}
      `}>
        <div className={`flex items-center h-16 border-b border-gray-200 ${collapsed ? 'justify-center px-2' : 'justify-between px-4'}`}>
          <Link to="/" className="flex items-center gap-2">
            <Database className="w-8 h-8 text-blue-600 flex-shrink-0" />
            {!collapsed && <span className="text-xl font-bold text-gray-900">RapiBase</span>}
          </Link>
          <button 
            className="lg:hidden p-2 text-gray-500 hover:text-gray-700"
            onClick={() => setSidebarOpen(false)}
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Collapse toggle button - desktop only */}
        <button
          onClick={() => setCollapsed(!collapsed)}
          className="hidden lg:flex absolute -right-3 top-20 w-6 h-6 bg-white border border-gray-200 rounded-full items-center justify-center text-gray-500 hover:text-gray-700 hover:bg-gray-50 shadow-sm"
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {collapsed ? <ChevronRight className="w-4 h-4" /> : <ChevronLeft className="w-4 h-4" />}
        </button>

        <nav className={`p-2 space-y-1 ${collapsed ? 'px-2' : 'px-4'} overflow-y-auto`} style={{ maxHeight: 'calc(100vh - 180px)' }}>
          {navigation.map((item) => {
            const isActive = item.name === 'Tables' 
              ? location.pathname === '/tables'
              : location.pathname === item.href || 
                (item.href !== '/' && location.pathname.startsWith(item.href))
            
            // Special handling for Tables item with expandable sub-items
            if (item.name === 'Tables') {
              return (
                <div key={item.name}>
                  <div className="flex items-center">
                    <Link
                      to={item.href}
                      onClick={() => setSidebarOpen(false)}
                      title={collapsed ? item.name : undefined}
                      className={`
                        flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium
                        transition-colors duration-150 flex-1
                        ${collapsed ? 'justify-center' : ''}
                        ${isActive 
                          ? 'bg-blue-50 text-blue-700' 
                          : 'text-gray-700 hover:bg-gray-100'
                        }
                      `}
                    >
                      <item.icon className="w-5 h-5 flex-shrink-0" />
                      {!collapsed && <span>{item.name}</span>}
                    </Link>
                    {!collapsed && tableList.length > 0 && (
                      <button
                        onClick={() => setTablesExpanded(!tablesExpanded)}
                        className="p-1 text-gray-400 hover:text-gray-600 rounded"
                      >
                        <ChevronDown className={`w-4 h-4 transition-transform ${tablesExpanded ? '' : '-rotate-90'}`} />
                      </button>
                    )}
                  </div>
                  {/* Sub-items for tables */}
                  {!collapsed && tablesExpanded && tableList.length > 0 && (
                    <div className="ml-4 mt-1 space-y-0.5 border-l border-gray-200 pl-2">
                      {tableList.map((table: any) => {
                        const isTableActive = location.pathname === `/tables/${table.name}`
                        return (
                          <Link
                            key={table.name}
                            to={`/tables/${table.name}`}
                            onClick={() => setSidebarOpen(false)}
                            className={`
                              flex items-center gap-2 px-2 py-1.5 rounded text-sm
                              transition-colors duration-150
                              ${isTableActive 
                                ? 'bg-blue-50 text-blue-700 font-medium' 
                                : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900'
                              }
                            `}
                          >
                            <Table2 className="w-3.5 h-3.5 flex-shrink-0" />
                            <span className="truncate">{table.name}</span>
                          </Link>
                        )
                      })}
                    </div>
                  )}
                </div>
              )
            }
            
            return (
              <Link
                key={item.name}
                to={item.href}
                onClick={() => setSidebarOpen(false)}
                title={collapsed ? item.name : undefined}
                className={`
                  flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium
                  transition-colors duration-150
                  ${collapsed ? 'justify-center' : ''}
                  ${isActive 
                    ? 'bg-blue-50 text-blue-700' 
                    : 'text-gray-700 hover:bg-gray-100'
                  }
                `}
              >
                <item.icon className="w-5 h-5 flex-shrink-0" />
                {!collapsed && <span>{item.name}</span>}
              </Link>
            )
          })}
        </nav>

        <div className={`absolute bottom-0 left-0 right-0 border-t border-gray-200 ${collapsed ? 'p-2' : 'p-4'}`}>
          {collapsed ? (
            <div className="flex flex-col items-center gap-2">
              <div className="w-8 h-8 rounded-full bg-blue-100 flex items-center justify-center">
                <span className="text-sm font-medium text-blue-700">
                  {user?.email?.[0]?.toUpperCase() || 'U'}
                </span>
              </div>
              <button
                onClick={logout}
                className="p-2 text-gray-500 hover:text-red-600 transition-colors"
                title="Logout"
              >
                <LogOut className="w-5 h-5" />
              </button>
            </div>
          ) : (
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 min-w-0">
                <div className="w-8 h-8 rounded-full bg-blue-100 flex items-center justify-center flex-shrink-0">
                  <span className="text-sm font-medium text-blue-700">
                    {user?.email?.[0]?.toUpperCase() || 'U'}
                  </span>
                </div>
                <div className="min-w-0">
                  <p className="text-sm font-medium text-gray-900 truncate">
                    {user?.email}
                  </p>
                  <p className="text-xs text-gray-500 capitalize">{user?.role}</p>
                </div>
              </div>
              <button
                onClick={logout}
                className="p-2 text-gray-500 hover:text-red-600 transition-colors"
                title="Logout"
              >
                <LogOut className="w-5 h-5" />
              </button>
            </div>
          )}
        </div>
      </aside>

      {/* Main content */}
      <div className={`transition-all duration-200 ${collapsed ? 'lg:pl-16' : 'lg:pl-64'}`}>
        {/* Mobile header */}
        <header className="lg:hidden sticky top-0 z-30 bg-white border-b border-gray-200">
          <div className="flex items-center justify-between h-16 px-4">
            <button 
              className="p-2 text-gray-500 hover:text-gray-700"
              onClick={() => setSidebarOpen(true)}
            >
              <Menu className="w-6 h-6" />
            </button>
            <Link to="/" className="flex items-center gap-2">
              <Database className="w-6 h-6 text-blue-600" />
              <span className="text-lg font-bold text-gray-900">RapiBase</span>
            </Link>
            <div className="w-10" /> {/* Spacer */}
          </div>
        </header>

        {/* Page content */}
        <main className="p-4 lg:p-8">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
