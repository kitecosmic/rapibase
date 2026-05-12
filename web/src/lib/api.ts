import { useAuthStore } from '../store/auth'

const API_BASE = '/api/dashboard'

class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

let refreshPromise: Promise<string | null> | null = null

async function refreshAccessToken(): Promise<string | null> {
  if (refreshPromise) return refreshPromise

  refreshPromise = (async () => {
    const { refreshToken, user, setAuth, logout } = useAuthStore.getState()
    if (!refreshToken) return null

    try {
      const res = await fetch(`${API_BASE}/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refreshToken }),
      })
      if (!res.ok) {
        logout()
        return null
      }
      const data = await res.json()
      setAuth(data.token, data.refresh_token, data.user ?? user)
      return data.token as string
    } catch {
      return null
    }
  })()

  try {
    return await refreshPromise
  } finally {
    refreshPromise = null
  }
}

export async function authedFetch(
  endpoint: string,
  options: RequestInit = {}
): Promise<Response> {
  const buildHeaders = (token: string | null): HeadersInit => {
    const h: Record<string, string> = { ...(options.headers as Record<string, string> | undefined) }
    if (token) h['Authorization'] = `Bearer ${token}`
    return h
  }

  const { token } = useAuthStore.getState()
  let response = await fetch(`${API_BASE}${endpoint}`, {
    ...options,
    headers: buildHeaders(token),
  })

  if (response.status !== 401) return response

  const newToken = await refreshAccessToken()
  if (!newToken) {
    if (window.location.pathname !== '/login') {
      window.location.href = '/login'
    }
    return response
  }

  response = await fetch(`${API_BASE}${endpoint}`, {
    ...options,
    headers: buildHeaders(newToken),
  })
  return response
}

async function request<T>(
  endpoint: string,
  options: RequestInit = {}
): Promise<T> {
  const headers: HeadersInit = {
    'Content-Type': 'application/json',
    ...options.headers,
  }

  const response = await authedFetch(endpoint, { ...options, headers })

  if (response.status === 401) {
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

  getRows: (
    name: string,
    page = 1,
    pageSize = 50,
    orderBy?: string,
    order?: string,
    filters?: { column: string; operator: string; value: string }[]
  ) => {
    const params = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
    })
    if (orderBy) params.set('order_by', orderBy)
    if (order) params.set('order', order)
    if (filters) {
      for (const f of filters) {
        if (!f.column || !f.operator) continue
        if (f.operator !== 'is' && f.value === '') continue
        params.append(`${f.column}.${f.operator}`, f.value)
      }
    }
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
    const formData = new FormData()
    formData.append('file', file)

    const response = await authedFetch(`/import/sql`, {
      method: 'POST',
      body: formData,
    })

    const data = await response.json()
    if (!response.ok) throw new ApiError(response.status, data.error)
    return data
  },

  importJSON: async (table: string, file: File, autoCreate: boolean = true) => {
    const formData = new FormData()
    formData.append('file', file)

    const response = await authedFetch(`/import/json/${table}?auto_create=${autoCreate}`, {
      method: 'POST',
      body: formData,
    })

    const data = await response.json()
    if (!response.ok) throw new ApiError(response.status, data.error)
    return data
  },

  importCSV: async (table: string, file: File, autoCreate: boolean = true) => {
    const formData = new FormData()
    formData.append('file', file)

    const response = await authedFetch(`/import/csv/${table}?auto_create=${autoCreate}`, {
      method: 'POST',
      body: formData,
    })

    const data = await response.json()
    if (!response.ok) throw new ApiError(response.status, data.error)
    return data
  },

  exportTable: (table: string, format: 'json' | 'sql' = 'json') =>
    authedFetch(`/export/${table}?format=${format}`).then((res) => res.blob()),
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

// Storage (MinIO)
export const storage = {
  listBuckets: () => request<{ buckets: any[] }>('/storage/buckets'),

  createBucket: (name: string, isPublic: boolean = false) =>
    request<any>('/storage/buckets', {
      method: 'POST',
      body: JSON.stringify({ name, public: isPublic }),
    }),

  deleteBucket: (name: string, force: boolean = false) =>
    request<any>(`/storage/buckets/${name}?force=${force}`, { method: 'DELETE' }),

  getBucketInfo: (name: string) => request<any>(`/storage/buckets/${name}`),

  setBucketPolicy: (name: string, isPublic: boolean) =>
    request<any>(`/storage/buckets/${name}/policy`, {
      method: 'PATCH',
      body: JSON.stringify({ public: isPublic }),
    }),

  listObjects: (bucket: string, prefix: string = '') => {
    const params = new URLSearchParams()
    if (prefix) params.set('prefix', prefix)
    return request<{ objects: any[]; prefix: string }>(`/storage/buckets/${bucket}/objects?${params}`)
  },

  uploadObject: async (bucket: string, file: File, prefix: string = '') => {
    const formData = new FormData()
    formData.append('file', file)
    if (prefix) formData.append('prefix', prefix)

    const response = await authedFetch(`/storage/buckets/${bucket}/objects`, {
      method: 'POST',
      body: formData,
    })

    const data = await response.json()
    if (!response.ok) throw new ApiError(response.status, data.error)
    return data
  },

  deleteObject: (bucket: string, key: string) =>
    request<any>(`/storage/buckets/${bucket}/objects/${key}`, { method: 'DELETE' }),

  getPresignedUrl: (bucket: string, key: string, expiry: number = 60) =>
    request<{ url: string; expires_in: number }>(`/storage/buckets/${bucket}/presign/${key}?expiry=${expiry}`),

  createFolder: (bucket: string, path: string) =>
    request<any>(`/storage/buckets/${bucket}/folders`, {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  moveObject: (bucket: string, sourceKey: string, destinationKey: string, destBucket?: string) =>
    request<any>(`/storage/buckets/${bucket}/move`, {
      method: 'POST',
      body: JSON.stringify({ source_key: sourceKey, destination_key: destinationKey, destination_bucket: destBucket }),
    }),

  copyObject: (bucket: string, sourceKey: string, destinationKey: string, destBucket?: string) =>
    request<any>(`/storage/buckets/${bucket}/copy`, {
      method: 'POST',
      body: JSON.stringify({ source_key: sourceKey, destination_key: destinationKey, destination_bucket: destBucket }),
    }),

  getObjectUrl: (bucket: string, key: string) => `${API_BASE}/storage/buckets/${bucket}/objects/${key}`,
}

// ---------------------------------------------------------------------
// Realtime
// ---------------------------------------------------------------------
// Lazy singleton client. The dashboard normally only opens one realtime
// connection at a time; multiple callers of getRealtime() share it.
// External apps consuming rapibase should import directly from
// '@/lib/realtime' so they are not coupled to the dashboard's auth
// store.

import {
  createRealtimeClient,
  type RealtimeClient,
} from './realtime'

let realtimeClient: RealtimeClient | null = null

/**
 * Returns (or lazily creates) the singleton realtime client for the
 * dashboard. The apiKey is required on the first call; subsequent
 * calls ignore it and return the same instance.
 *
 * Token rotation: the client picks up the current token from the
 * auth store on creation; pages that observe token changes should
 * call `client.setAuth(newToken)` to propagate them.
 */
export function getRealtime(apiKey: string): RealtimeClient {
  if (realtimeClient) return realtimeClient
  const { token } = useAuthStore.getState()
  realtimeClient = createRealtimeClient({
    url: window.location.origin,
    apiKey,
    token: token ?? undefined,
  })
  return realtimeClient
}

/**
 * Tears down the realtime client. Call on logout so the next user's
 * session starts fresh.
 */
export function closeRealtime(): void {
  realtimeClient?.close()
  realtimeClient = null
}
