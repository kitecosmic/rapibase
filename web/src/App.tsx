import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { useAuthStore } from './store/auth'
import Layout from './components/Layout'
import Login from './pages/Login'
import ForgotPassword from './pages/ForgotPassword'
import ResetPassword from './pages/ResetPassword'
import Dashboard from './pages/Dashboard'
import Auth from './pages/Auth'
import Docs from './pages/Docs'
import Tables from './pages/Tables'
import TableView from './pages/TableView'
import SQLEditor from './pages/SQLEditor'
import Import from './pages/Import'
import Webhooks from './pages/Webhooks'
import Notifications from './pages/Notifications'
import Storage from './pages/Storage'
import Realtime from './pages/Realtime'
import Security from './pages/Security'

function PrivateRoute({ children }: { children: React.ReactNode }) {
  const { token } = useAuthStore()
  return token ? <>{children}</> : <Navigate to="/login" />
}

function PublicRoute({ children }: { children: React.ReactNode }) {
  const { token } = useAuthStore()
  return !token ? <>{children}</> : <Navigate to="/" />
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        {/* Public routes */}
        <Route path="/login" element={<PublicRoute><Login /></PublicRoute>} />
        <Route path="/forgot-password" element={<PublicRoute><ForgotPassword /></PublicRoute>} />
        <Route path="/reset-password" element={<ResetPassword />} />

        {/* Protected routes */}
        <Route path="/" element={<PrivateRoute><Layout /></PrivateRoute>}>
          <Route index element={<Dashboard />} />
          <Route path="tables" element={<Tables />} />
          <Route path="tables/:name" element={<TableView />} />
          <Route path="sql" element={<SQLEditor />} />
          <Route path="import" element={<Import />} />
          <Route path="auth" element={<Auth />} />
          <Route path="webhooks" element={<Webhooks />} />
          <Route path="notifications" element={<Notifications />} />
          <Route path="storage" element={<Storage />} />
          <Route path="realtime" element={<Realtime />} />
          <Route path="security" element={<Security />} />
          <Route path="docs" element={<Docs />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
