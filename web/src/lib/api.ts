import { useAuthStore } from '../store/auth'

const API_BASE = '/api/dashboard'

class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(
  endpoint: string,
  options: RequestInit = {}
): Promise<T> {
  const { token, logout } = useAuthStore.getState()

  const headers: HeadersInit = {
    'Content-Type': 'application/json',
    ...options.headers,
  }

  if (token) {
    (headers as Record<string, string>)['Authorization'] = `Bearer ${token}`
  }

  const response = await fetch(`${API_BASE}${endpoint}`, {
    ...options,
    headers,
  })

  if (response.status === 401) {
    logout()
    window.location.href = '/login'
    throw new ApiError(401, 'Unauthorized')
  }

  const data = await response.json()

  if (!response.ok) {
    throw new ApiError(response.status, data.error || 'Request failed')
  }

  return data
}

// Auth
export const auth = {
  login: (email: string, password: string) =>
    request<{ token: string; refresh_token: string; user: any }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    }),

  register: (email: string, password: string) =>
    request<{ token: string; refresh_token: string; user: any }>('/auth/register', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    }),

  forgotPassword: (email: string) =>
    request<{ message: string }>('/auth/forgot-password', {
      method: 'POST',
      body: JSON.stringify({ email }),
    }),

  resetPassword: (token: string, password: string) =>
    request<{ message: string }>('/auth/reset-password', {
      method: 'POST',
      body: JSON.stringify({ token, password }),
    }),

  me: () => request<any>('/auth/me'),
}

// Tables
export const tables = {
  list: () => request<{ tables: any[] }>('/tables'),

  get: (name: string) => request<any>(`/tables/${name}`),

  create: (name: string, columns: any[]) =>
    request<any>('/tables', {
      method: 'POST',
      body: JSON.stringify({ name, columns }),
    }),

  drop: (name: string) =>
    request<any>(`/tables/${name}`, { method: 'DELETE' }),

  getRows: (name: string, page = 1, pageSize = 50, orderBy?: string, order?: string) => {
    const params = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
    })
    if (orderBy) params.set('order_by', orderBy)
    if (order) params.set('order', order)
    return request<any>(`/tables/${name}/rows?${params}`)
  },

  insertRow: (table: string, data: any) =>
    request<any>(`/tables/${table}/rows`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  updateRow: (table: string, id: any, data: any) =>
    request<any>(`/tables/${table}/rows/${id}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  deleteRow: (table: string, id: any) =>
    request<any>(`/tables/${table}/rows/${id}`, { method: 'DELETE' }),
}

// Query
export const query = {
  execute: (sql: string, params?: any[]) =>
    request<any>('/query', {
      method: 'POST',
      body: JSON.stringify({ sql, params }),
    }),
}

// Import/Export
export const importExport = {
  importSQL: async (file: File) => {
    const { token } = useAuthStore.getState()
    const formData = new FormData()
    formData.append('file', file)

    const response = await fetch(`${API_BASE}/import/sql`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
      },
      body: formData,
    })

    const data = await response.json()
    if (!response.ok) throw new ApiError(response.status, data.error)
    return data
  },

  importJSON: async (table: string, file: File) => {
    const { token } = useAuthStore.getState()
    const formData = new FormData()
    formData.append('file', file)

    const response = await fetch(`${API_BASE}/import/json/${table}`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
      },
      body: formData,
    })

    const data = await response.json()
    if (!response.ok) throw new ApiError(response.status, data.error)
    return data
  },

  exportTable: (table: string, format: 'json' | 'sql' = 'json') => {
    const { token } = useAuthStore.getState()
    return fetch(`${API_BASE}/export/${table}?format=${format}`, {
      headers: {
        Authorization: `Bearer ${token}`,
      },
    }).then((res) => res.blob())
  },
}

// Webhooks
export const webhooks = {
  list: () => request<{ webhooks: any[] }>('/webhooks'),

  getEvents: () => request<{ events: string[] }>('/webhooks/events'),

  getLogs: (webhookId?: number, success?: boolean, limit = 50) => {
    const params = new URLSearchParams({ limit: String(limit) })
    if (webhookId) params.set('webhook_id', String(webhookId))
    if (success !== undefined) params.set('success', String(success))
    return request<{ logs: any[] }>(`/webhooks/logs?${params}`)
  },

  create: (data: {
    name: string
    url: string
    secret?: string
    events: string[]
    headers?: Record<string, string>
    enabled?: boolean
  }) =>
    request<any>('/webhooks', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  update: (id: number, data: any) =>
    request<any>(`/webhooks/${id}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  delete: (id: number) =>
    request<any>(`/webhooks/${id}`, { method: 'DELETE' }),

  toggle: (id: number, enabled: boolean) =>
    request<any>(`/webhooks/${id}/toggle`, {
      method: 'PATCH',
      body: JSON.stringify({ enabled }),
    }),
}

// Push Notifications
export const pushNotifications = {
  getConfigs: () => request<{ configs: any[] }>('/push/config'),

  setupWeb: (subject?: string) =>
    request<{ vapid_public_key: string }>('/push/config/web', {
      method: 'POST',
      body: JSON.stringify({ subject }),
    }),

  setupIOS: (data: {
    key_id: string
    team_id: string
    bundle_id: string
    private_key: string
    production?: boolean
  }) =>
    request<any>('/push/config/ios', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  setupAndroid: (data: {
    project_id: string
    private_key_id?: string
    private_key: string
    client_email: string
    client_id?: string
  }) =>
    request<any>('/push/config/android', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  toggle: (platform: string, enabled: boolean) =>
    request<any>(`/push/config/${platform}/toggle`, {
      method: 'PATCH',
      body: JSON.stringify({ enabled }),
    }),

  send: (data: { 
    title: string
    body?: string
    user_id?: string
    user_ids?: string[]
    filter?: {
      role?: string
      email_verified?: boolean
      created_after?: string
      created_before?: string
      metadata?: Record<string, any>
    }
    data?: any 
  }) =>
    request<any>('/push/send', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  list: (limit = 50) =>
    request<{ notifications: any[] }>(`/push/notifications?limit=${limit}`),
}
