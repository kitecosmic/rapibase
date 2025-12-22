import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { 
  Bell, 
  Send, 
  Settings, 
  Smartphone, 
  Globe, 
  Apple,
  CheckCircle,
  XCircle,
  Loader2,
  Copy,
  Check,
  RefreshCw,
  Code
} from 'lucide-react'
import { pushNotifications } from '../lib/api'

interface PushConfig {
  id?: number
  platform: string
  enabled: boolean
  configured: boolean
  vapid_public_key?: string
}

interface Notification {
  id: number
  user_id: string | null
  title: string
  body: string
  data: Record<string, any>
  sent_at: string | null
  read_at: string | null
  created_at: string
}

export default function Notifications() {
  const queryClient = useQueryClient()
  const [activeTab, setActiveTab] = useState<'send' | 'config' | 'api' | 'history'>('send')
  const [copied, setCopied] = useState(false)
  const [copiedCode, setCopiedCode] = useState<string | null>(null)

  const { data: configsData, isLoading: configsLoading } = useQuery({
    queryKey: ['push-configs'],
    queryFn: pushNotifications.getConfigs,
  })

  const { data: notificationsData, isLoading: notificationsLoading } = useQuery({
    queryKey: ['push-notifications'],
    queryFn: () => pushNotifications.list(),
    enabled: activeTab === 'history',
  })

  const configs: PushConfig[] = configsData?.configs || []
  const notifications: Notification[] = notificationsData?.notifications || []

  const copyToClipboard = (text: string, id?: string) => {
    navigator.clipboard.writeText(text)
    if (id) {
      setCopiedCode(id)
      setTimeout(() => setCopiedCode(null), 2000)
    } else {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Push Notifications</h1>
          <p className="text-gray-600 mt-1">Send notifications to your users</p>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 mb-6 bg-gray-100 p-1 rounded-lg w-fit">
        <button
          onClick={() => setActiveTab('send')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'send'
              ? 'bg-white text-gray-900 shadow-sm'
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <Send className="w-4 h-4 inline mr-2" />
          Send
        </button>
        <button
          onClick={() => setActiveTab('config')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'config'
              ? 'bg-white text-gray-900 shadow-sm'
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <Settings className="w-4 h-4 inline mr-2" />
          Configuration
        </button>
        <button
          onClick={() => setActiveTab('api')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'api'
              ? 'bg-white text-gray-900 shadow-sm'
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <Code className="w-4 h-4 inline mr-2" />
          API
        </button>
        <button
          onClick={() => setActiveTab('history')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'history'
              ? 'bg-white text-gray-900 shadow-sm'
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <Bell className="w-4 h-4 inline mr-2" />
          History
        </button>
      </div>

      {/* Send Tab */}
      {activeTab === 'send' && (
        <SendNotificationForm 
          onSuccess={() => queryClient.invalidateQueries({ queryKey: ['push-notifications'] })}
        />
      )}

      {/* Config Tab */}
      {activeTab === 'config' && (
        <div className="space-y-4">
          {configsLoading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
            </div>
          ) : (
            configs.map((config) => (
              <PlatformConfig 
                key={config.platform} 
                config={config} 
                onCopy={copyToClipboard}
                copied={copied}
              />
            ))
          )}
        </div>
      )}

      {/* API Tab */}
      {activeTab === 'api' && (
        <APIExamples 
          copyToClipboard={copyToClipboard} 
          copiedCode={copiedCode}
        />
      )}

      {/* History Tab */}
      {activeTab === 'history' && (
        <div className="bg-white rounded-xl border border-gray-200">
          <div className="px-6 py-4 border-b border-gray-200">
            <h2 className="text-lg font-semibold text-gray-900">Sent Notifications</h2>
          </div>
          {notificationsLoading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
            </div>
          ) : notifications.length === 0 ? (
            <div className="text-center py-12 text-gray-500">
              No notifications sent yet
            </div>
          ) : (
            <div className="divide-y divide-gray-200">
              {notifications.map((notif) => (
                <div key={notif.id} className="p-4">
                  <div className="flex items-start justify-between">
                    <div>
                      <h3 className="font-medium text-gray-900">{notif.title}</h3>
                      <p className="text-sm text-gray-600 mt-1">{notif.body}</p>
                      <div className="flex items-center gap-3 mt-2 text-xs text-gray-500">
                        <span>
                          {notif.user_id ? `User: ${notif.user_id.slice(0, 8)}...` : 'Broadcast'}
                        </span>
                        <span>•</span>
                        <span>{new Date(notif.created_at).toLocaleString()}</span>
                      </div>
                    </div>
                    {notif.sent_at ? (
                      <CheckCircle className="w-5 h-5 text-green-600 flex-shrink-0" />
                    ) : (
                      <XCircle className="w-5 h-5 text-gray-400 flex-shrink-0" />
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

type TargetType = 'broadcast' | 'user' | 'users' | 'filter'

function SendNotificationForm({ onSuccess }: { onSuccess: () => void }) {
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [targetType, setTargetType] = useState<TargetType>('broadcast')
  const [userId, setUserId] = useState('')
  const [userIds, setUserIds] = useState('')
  const [filterRole, setFilterRole] = useState('')
  const [filterVerified, setFilterVerified] = useState<string>('')

  const sendMutation = useMutation({
    mutationFn: pushNotifications.send,
    onSuccess: () => {
      setTitle('')
      setBody('')
      setUserId('')
      setUserIds('')
      onSuccess()
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    
    const payload: any = { title, body }
    
    if (targetType === 'user' && userId) {
      payload.user_id = userId
    } else if (targetType === 'users' && userIds) {
      payload.user_ids = userIds.split(',').map(id => id.trim()).filter(Boolean)
    } else if (targetType === 'filter') {
      payload.filter = {}
      if (filterRole) payload.filter.role = filterRole
      if (filterVerified === 'true') payload.filter.email_verified = true
      if (filterVerified === 'false') payload.filter.email_verified = false
    }
    
    sendMutation.mutate(payload)
  }

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-6">
      <h2 className="text-lg font-semibold text-gray-900 mb-4">Send Notification</h2>
      
      <form onSubmit={handleSubmit} className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Title</label>
          <input
            type="text"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Notification title"
            className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            required
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Body</label>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Notification message"
            rows={3}
            className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 mb-2">Target Audience</label>
          <div className="grid grid-cols-2 gap-2 mb-3">
            {[
              { value: 'broadcast', label: 'All users' },
              { value: 'user', label: 'Single user' },
              { value: 'users', label: 'Multiple users' },
              { value: 'filter', label: 'By conditions' },
            ].map((option) => (
              <button
                key={option.value}
                type="button"
                onClick={() => setTargetType(option.value as TargetType)}
                className={`px-3 py-2 text-sm rounded-lg border transition-colors ${
                  targetType === option.value
                    ? 'bg-blue-100 border-blue-300 text-blue-700'
                    : 'bg-gray-50 border-gray-200 text-gray-600 hover:bg-gray-100'
                }`}
              >
                {option.label}
              </button>
            ))}
          </div>

          {targetType === 'user' && (
            <input
              type="text"
              value={userId}
              onChange={(e) => setUserId(e.target.value)}
              placeholder="User ID (UUID)"
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            />
          )}

          {targetType === 'users' && (
            <textarea
              value={userIds}
              onChange={(e) => setUserIds(e.target.value)}
              placeholder="User IDs separated by commas (uuid1, uuid2, uuid3)"
              rows={2}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            />
          )}

          {targetType === 'filter' && (
            <div className="space-y-3 p-3 bg-gray-50 rounded-lg">
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Role</label>
                <input
                  type="text"
                  value={filterRole}
                  onChange={(e) => setFilterRole(e.target.value)}
                  placeholder="e.g., premium, admin"
                  className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Email Verified</label>
                <select
                  value={filterVerified}
                  onChange={(e) => setFilterVerified(e.target.value)}
                  className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                >
                  <option value="">Any</option>
                  <option value="true">Verified only</option>
                  <option value="false">Unverified only</option>
                </select>
              </div>
            </div>
          )}
        </div>

        {sendMutation.isSuccess && (
          <div className="p-3 bg-green-50 text-green-700 rounded-lg text-sm">
            Notification sent successfully!
          </div>
        )}

        {sendMutation.isError && (
          <div className="p-3 bg-red-50 text-red-700 rounded-lg text-sm">
            Failed to send notification
          </div>
        )}

        <button
          type="submit"
          disabled={sendMutation.isPending || !title}
          className="w-full px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors flex items-center justify-center gap-2"
        >
          {sendMutation.isPending ? (
            <Loader2 className="w-4 h-4 animate-spin" />
          ) : (
            <>
              <Send className="w-4 h-4" />
              Send Notification
            </>
          )}
        </button>
      </form>
    </div>
  )
}

function PlatformConfig({ config, onCopy, copied }: { config: PushConfig; onCopy: (text: string) => void; copied: boolean }) {
  const queryClient = useQueryClient()
  
  const setupWebMutation = useMutation({
    mutationFn: pushNotifications.setupWeb,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['push-configs'] })
    },
  })

  const toggleMutation = useMutation({
    mutationFn: ({ platform, enabled }: { platform: string; enabled: boolean }) =>
      pushNotifications.toggle(platform, enabled),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['push-configs'] })
    },
  })

  const getPlatformIcon = () => {
    switch (config.platform) {
      case 'web':
        return <Globe className="w-6 h-6" />
      case 'ios':
        return <Apple className="w-6 h-6" />
      case 'android':
        return <Smartphone className="w-6 h-6" />
      default:
        return <Bell className="w-6 h-6" />
    }
  }

  const getPlatformName = () => {
    switch (config.platform) {
      case 'web':
        return 'Web Push'
      case 'ios':
        return 'iOS (APNS)'
      case 'android':
        return 'Android (FCM)'
      default:
        return config.platform
    }
  }

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-3">
          <div className={`p-2 rounded-lg ${config.enabled ? 'bg-green-100 text-green-600' : 'bg-gray-100 text-gray-400'}`}>
            {getPlatformIcon()}
          </div>
          <div>
            <h3 className="font-semibold text-gray-900">{getPlatformName()}</h3>
            <p className="text-sm text-gray-500">
              {config.configured ? (
                <span className="text-green-600">Configured</span>
              ) : (
                <span className="text-gray-400">Not configured</span>
              )}
            </p>
          </div>
        </div>
        {config.configured && (
          <button
            onClick={() => toggleMutation.mutate({ platform: config.platform, enabled: !config.enabled })}
            className={`px-3 py-1 rounded-full text-sm font-medium transition-colors ${
              config.enabled
                ? 'bg-green-100 text-green-700 hover:bg-green-200'
                : 'bg-gray-100 text-gray-600 hover:bg-gray-200'
            }`}
          >
            {config.enabled ? 'Enabled' : 'Disabled'}
          </button>
        )}
      </div>

      {config.platform === 'web' && (
        <div className="space-y-4">
          {config.configured && config.vapid_public_key ? (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                VAPID Public Key
              </label>
              <div className="flex items-center gap-2">
                <code className="flex-1 p-2 bg-gray-50 rounded text-xs text-gray-600 overflow-x-auto">
                  {config.vapid_public_key}
                </code>
                <button
                  onClick={() => onCopy(config.vapid_public_key!)}
                  className="p-2 text-gray-500 hover:text-blue-600 transition-colors"
                >
                  {copied ? <Check className="w-4 h-4 text-green-600" /> : <Copy className="w-4 h-4" />}
                </button>
              </div>
              <p className="text-xs text-gray-500 mt-2">
                Use this key in your frontend to subscribe users to push notifications.
              </p>
            </div>
          ) : (
            <div>
              <p className="text-sm text-gray-600 mb-3">
                Generate VAPID keys to enable Web Push notifications. No external services required.
              </p>
              <button
                onClick={() => setupWebMutation.mutate(undefined)}
                disabled={setupWebMutation.isPending}
                className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors flex items-center gap-2"
              >
                {setupWebMutation.isPending ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  <RefreshCw className="w-4 h-4" />
                )}
                Generate VAPID Keys
              </button>
            </div>
          )}
        </div>
      )}

      {config.platform === 'ios' && <IOSConfigForm configured={config.configured} />}

      {config.platform === 'android' && <AndroidConfigForm configured={config.configured} />}
    </div>
  )
}

function IOSConfigForm({ configured }: { configured: boolean }) {
  const queryClient = useQueryClient()
  const [keyId, setKeyId] = useState('')
  const [teamId, setTeamId] = useState('')
  const [bundleId, setBundleId] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [production, setProduction] = useState(false)

  const setupMutation = useMutation({
    mutationFn: pushNotifications.setupIOS,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['push-configs'] })
      setKeyId('')
      setTeamId('')
      setBundleId('')
      setPrivateKey('')
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setupMutation.mutate({
      key_id: keyId,
      team_id: teamId,
      bundle_id: bundleId,
      private_key: privateKey,
      production,
    })
  }

  if (configured) {
    return (
      <div className="p-3 bg-green-50 rounded-lg text-sm text-green-700">
        iOS Push is configured. Use the toggle above to enable/disable.
      </div>
    )
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-3">
      <p className="text-sm text-gray-600 mb-3">
        Configure iOS push notifications with your Apple Developer credentials.
      </p>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="block text-xs font-medium text-gray-600 mb-1">Key ID</label>
          <input
            type="text"
            value={keyId}
            onChange={(e) => setKeyId(e.target.value)}
            placeholder="ABC123DEFG"
            className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            required
          />
        </div>
        <div>
          <label className="block text-xs font-medium text-gray-600 mb-1">Team ID</label>
          <input
            type="text"
            value={teamId}
            onChange={(e) => setTeamId(e.target.value)}
            placeholder="ABCD1234EF"
            className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            required
          />
        </div>
      </div>
      <div>
        <label className="block text-xs font-medium text-gray-600 mb-1">Bundle ID</label>
        <input
          type="text"
          value={bundleId}
          onChange={(e) => setBundleId(e.target.value)}
          placeholder="com.yourapp.bundle"
          className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          required
        />
      </div>
      <div>
        <label className="block text-xs font-medium text-gray-600 mb-1">Private Key (.p8 contents)</label>
        <textarea
          value={privateKey}
          onChange={(e) => setPrivateKey(e.target.value)}
          placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
          rows={4}
          className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent font-mono"
          required
        />
      </div>
      <div className="flex items-center gap-2">
        <input
          type="checkbox"
          id="production"
          checked={production}
          onChange={(e) => setProduction(e.target.checked)}
          className="w-4 h-4 text-blue-600 rounded"
        />
        <label htmlFor="production" className="text-sm text-gray-700">Production environment</label>
      </div>
      {setupMutation.isError && (
        <div className="p-2 bg-red-50 text-red-700 rounded text-sm">
          Failed to configure iOS Push
        </div>
      )}
      <button
        type="submit"
        disabled={setupMutation.isPending}
        className="w-full px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors flex items-center justify-center gap-2"
      >
        {setupMutation.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Apple className="w-4 h-4" />}
        Configure iOS Push
      </button>
    </form>
  )
}

function AndroidConfigForm({ configured }: { configured: boolean }) {
  const queryClient = useQueryClient()
  const [projectId, setProjectId] = useState('')
  const [privateKeyId, setPrivateKeyId] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [clientEmail, setClientEmail] = useState('')
  const [clientId, setClientId] = useState('')

  const setupMutation = useMutation({
    mutationFn: pushNotifications.setupAndroid,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['push-configs'] })
      setProjectId('')
      setPrivateKeyId('')
      setPrivateKey('')
      setClientEmail('')
      setClientId('')
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setupMutation.mutate({
      project_id: projectId,
      private_key_id: privateKeyId || undefined,
      private_key: privateKey,
      client_email: clientEmail,
      client_id: clientId || undefined,
    })
  }

  if (configured) {
    return (
      <div className="p-3 bg-green-50 rounded-lg text-sm text-green-700">
        Android Push is configured. Use the toggle above to enable/disable.
      </div>
    )
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-3">
      <p className="text-sm text-gray-600 mb-3">
        Configure Android push notifications with your Firebase service account.
      </p>
      <div>
        <label className="block text-xs font-medium text-gray-600 mb-1">Project ID</label>
        <input
          type="text"
          value={projectId}
          onChange={(e) => setProjectId(e.target.value)}
          placeholder="your-firebase-project"
          className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          required
        />
      </div>
      <div>
        <label className="block text-xs font-medium text-gray-600 mb-1">Client Email</label>
        <input
          type="email"
          value={clientEmail}
          onChange={(e) => setClientEmail(e.target.value)}
          placeholder="firebase-adminsdk@project.iam.gserviceaccount.com"
          className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          required
        />
      </div>
      <div>
        <label className="block text-xs font-medium text-gray-600 mb-1">Private Key</label>
        <textarea
          value={privateKey}
          onChange={(e) => setPrivateKey(e.target.value)}
          placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
          rows={4}
          className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent font-mono"
          required
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="block text-xs font-medium text-gray-600 mb-1">Private Key ID (optional)</label>
          <input
            type="text"
            value={privateKeyId}
            onChange={(e) => setPrivateKeyId(e.target.value)}
            placeholder="abc123..."
            className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
        </div>
        <div>
          <label className="block text-xs font-medium text-gray-600 mb-1">Client ID (optional)</label>
          <input
            type="text"
            value={clientId}
            onChange={(e) => setClientId(e.target.value)}
            placeholder="123456789..."
            className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
        </div>
      </div>
      {setupMutation.isError && (
        <div className="p-2 bg-red-50 text-red-700 rounded text-sm">
          Failed to configure Android Push
        </div>
      )}
      <button
        type="submit"
        disabled={setupMutation.isPending}
        className="w-full px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors flex items-center justify-center gap-2"
      >
        {setupMutation.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Smartphone className="w-4 h-4" />}
        Configure Android Push
      </button>
    </form>
  )
}

function APIExamples({ copyToClipboard, copiedCode }: { copyToClipboard: (text: string, id: string) => void; copiedCode: string | null }) {
  const [selectedLang, setSelectedLang] = useState<'curl' | 'javascript' | 'python' | 'php' | 'go'>('curl')
  
  const baseUrl = window.location.origin
  const serviceKey = 'YOUR_SERVICE_KEY'

  const examples = {
    broadcast: {
      curl: `curl -X POST "${baseUrl}/api/v1/push/send" \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${serviceKey}" \\
  -d '{
    "title": "Hello Everyone!",
    "body": "This is a broadcast notification"
  }'`,
      javascript: `fetch("${baseUrl}/api/v1/push/send", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "apikey": "${serviceKey}"
  },
  body: JSON.stringify({
    title: "Hello Everyone!",
    body: "This is a broadcast notification"
  })
})`,
      python: `import requests

response = requests.post(
    "${baseUrl}/api/v1/push/send",
    headers={
        "Content-Type": "application/json",
        "apikey": "${serviceKey}"
    },
    json={
        "title": "Hello Everyone!",
        "body": "This is a broadcast notification"
    }
)`,
      php: `<?php
$ch = curl_init("${baseUrl}/api/v1/push/send");
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    "Content-Type: application/json",
    "apikey: ${serviceKey}"
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    "title" => "Hello Everyone!",
    "body" => "This is a broadcast notification"
]));
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
$response = curl_exec($ch);`,
      go: `package main

import (
    "bytes"
    "encoding/json"
    "net/http"
)

func main() {
    payload, _ := json.Marshal(map[string]string{
        "title": "Hello Everyone!",
        "body":  "This is a broadcast notification",
    })
    
    req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/push/send", bytes.NewBuffer(payload))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("apikey", "${serviceKey}")
    
    client := &http.Client{}
    client.Do(req)
}`,
    },
    singleUser: {
      curl: `curl -X POST "${baseUrl}/api/v1/push/send" \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${serviceKey}" \\
  -d '{
    "title": "Personal Message",
    "body": "This is just for you",
    "user_id": "uuid-of-user"
  }'`,
      javascript: `fetch("${baseUrl}/api/v1/push/send", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "apikey": "${serviceKey}"
  },
  body: JSON.stringify({
    title: "Personal Message",
    body: "This is just for you",
    user_id: "uuid-of-user"
  })
})`,
      python: `import requests

response = requests.post(
    "${baseUrl}/api/v1/push/send",
    headers={
        "Content-Type": "application/json",
        "apikey": "${serviceKey}"
    },
    json={
        "title": "Personal Message",
        "body": "This is just for you",
        "user_id": "uuid-of-user"
    }
)`,
      php: `<?php
$ch = curl_init("${baseUrl}/api/v1/push/send");
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    "Content-Type: application/json",
    "apikey: ${serviceKey}"
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    "title" => "Personal Message",
    "body" => "This is just for you",
    "user_id" => "uuid-of-user"
]));
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
$response = curl_exec($ch);`,
      go: `payload, _ := json.Marshal(map[string]string{
    "title":   "Personal Message",
    "body":    "This is just for you",
    "user_id": "uuid-of-user",
})`,
    },
    filter: {
      curl: `curl -X POST "${baseUrl}/api/v1/push/send" \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${serviceKey}" \\
  -d '{
    "title": "Premium Feature",
    "body": "Exclusive content for premium users",
    "filter": {
      "role": "premium",
      "email_verified": true
    }
  }'`,
      javascript: `fetch("${baseUrl}/api/v1/push/send", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "apikey": "${serviceKey}"
  },
  body: JSON.stringify({
    title: "Premium Feature",
    body: "Exclusive content for premium users",
    filter: {
      role: "premium",
      email_verified: true
    }
  })
})`,
      python: `import requests

response = requests.post(
    "${baseUrl}/api/v1/push/send",
    headers={
        "Content-Type": "application/json",
        "apikey": "${serviceKey}"
    },
    json={
        "title": "Premium Feature",
        "body": "Exclusive content for premium users",
        "filter": {
            "role": "premium",
            "email_verified": True
        }
    }
)`,
      php: `<?php
$ch = curl_init("${baseUrl}/api/v1/push/send");
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    "Content-Type: application/json",
    "apikey: ${serviceKey}"
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    "title" => "Premium Feature",
    "body" => "Exclusive content for premium users",
    "filter" => [
        "role" => "premium",
        "email_verified" => true
    ]
]));
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
$response = curl_exec($ch);`,
      go: `payload, _ := json.Marshal(map[string]interface{}{
    "title": "Premium Feature",
    "body":  "Exclusive content for premium users",
    "filter": map[string]interface{}{
        "role":           "premium",
        "email_verified": true,
    },
})`,
    },
  }

  const CodeBlock = ({ code, id }: { code: string; id: string }) => (
    <div className="relative">
      <pre className="p-4 bg-gray-900 text-gray-100 rounded-lg text-xs overflow-x-auto">
        <code>{code}</code>
      </pre>
      <button
        onClick={() => copyToClipboard(code, id)}
        className="absolute top-2 right-2 p-2 bg-gray-700 hover:bg-gray-600 rounded text-gray-300 transition-colors"
      >
        {copiedCode === id ? <Check className="w-4 h-4 text-green-400" /> : <Copy className="w-4 h-4" />}
      </button>
    </div>
  )

  return (
    <div className="space-y-6">
      <div className="bg-white rounded-xl border border-gray-200 p-6">
        <h2 className="text-lg font-semibold text-gray-900 mb-4">API Endpoint</h2>
        <div className="p-4 bg-gray-50 rounded-lg">
          <div className="flex items-center gap-2 mb-2">
            <span className="px-2 py-1 bg-green-100 text-green-700 text-xs font-medium rounded">POST</span>
            <code className="text-sm text-gray-800">{baseUrl}/api/v1/push/send</code>
          </div>
          <p className="text-sm text-gray-600">
            Use your <strong>SERVICE_KEY</strong> in the <code className="bg-gray-200 px-1 rounded">apikey</code> header for full access.
          </p>
        </div>
      </div>

      {/* Language Selector */}
      <div className="flex gap-1 bg-gray-100 p-1 rounded-lg w-fit">
        {(['curl', 'javascript', 'python', 'php', 'go'] as const).map((lang) => (
          <button
            key={lang}
            onClick={() => setSelectedLang(lang)}
            className={`px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
              selectedLang === lang
                ? 'bg-white text-gray-900 shadow-sm'
                : 'text-gray-600 hover:text-gray-900'
            }`}
          >
            {lang === 'curl' ? 'cURL' : lang.charAt(0).toUpperCase() + lang.slice(1)}
          </button>
        ))}
      </div>

      {/* Broadcast Example */}
      <div className="bg-white rounded-xl border border-gray-200 p-6">
        <h3 className="font-semibold text-gray-900 mb-2">Broadcast to All Users</h3>
        <p className="text-sm text-gray-600 mb-4">Send a notification to all subscribed users.</p>
        <CodeBlock code={examples.broadcast[selectedLang]} id="broadcast" />
      </div>

      {/* Single User Example */}
      <div className="bg-white rounded-xl border border-gray-200 p-6">
        <h3 className="font-semibold text-gray-900 mb-2">Send to Single User</h3>
        <p className="text-sm text-gray-600 mb-4">Send a notification to a specific user by UUID.</p>
        <CodeBlock code={examples.singleUser[selectedLang]} id="singleUser" />
      </div>

      {/* Filter Example */}
      <div className="bg-white rounded-xl border border-gray-200 p-6">
        <h3 className="font-semibold text-gray-900 mb-2">Send by Filter (Segmentation)</h3>
        <p className="text-sm text-gray-600 mb-4">Send to users matching specific conditions.</p>
        <CodeBlock code={examples.filter[selectedLang]} id="filter" />
        <div className="mt-4 p-3 bg-blue-50 rounded-lg">
          <p className="text-sm text-blue-800 font-medium mb-2">Available Filter Options:</p>
          <ul className="text-sm text-blue-700 space-y-1">
            <li><code className="bg-blue-100 px-1 rounded">role</code> - Filter by user role (e.g., "premium", "admin")</li>
            <li><code className="bg-blue-100 px-1 rounded">email_verified</code> - true/false</li>
            <li><code className="bg-blue-100 px-1 rounded">created_after</code> - ISO date string</li>
            <li><code className="bg-blue-100 px-1 rounded">created_before</code> - ISO date string</li>
            <li><code className="bg-blue-100 px-1 rounded">metadata</code> - Match user_metadata JSON</li>
          </ul>
        </div>
      </div>
    </div>
  )
}
