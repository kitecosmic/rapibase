import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Check, Copy, ExternalLink, Loader2, KeyRound } from 'lucide-react'

interface ProviderStatus {
  id: 'google' | 'google_native' | 'apple' | 'github' | 'facebook'
  name: string
  flow: 'redirect' | 'id_token'
  enabled: boolean
  env: string[]
  missing: string[]
  callback_url?: string
  start_url?: string
  token_url?: string
  client_ids?: string[]
}

const CONSOLES: Record<ProviderStatus['id'], { url: string; label: string; steps: string }> = {
  google: {
    url: 'https://console.cloud.google.com/apis/credentials',
    label: 'Google Cloud → Credentials',
    steps: 'Create an OAuth client ID of type "Web application" and add the callback URL below as an authorized redirect URI.',
  },
  google_native: {
    url: 'https://console.cloud.google.com/apis/credentials',
    label: 'Google Cloud → Credentials',
    steps:
      'Create one OAuth client ID per app (Android: package name + SHA-1; iOS: bundle ID). Put them in OAUTH_GOOGLE_CLIENT_IDS, comma-separated. The web client ID (OAUTH_GOOGLE_CLIENT_ID) is accepted too: on Android, google_sign_in issues tokens for the serverClientId.',
  },
  apple: {
    url: 'https://developer.apple.com/account/resources/identifiers/list',
    label: 'Apple Developer → Identifiers',
    steps:
      'Enable "Sign in with Apple" on your App ID. Put the bundle ID (iOS/macOS) and, for the web, the Services ID in OAUTH_APPLE_CLIENT_IDS, comma-separated. No private key is needed: Rapibase verifies Apple\'s signed id_token.',
  },
  github: {
    url: 'https://github.com/settings/developers',
    label: 'GitHub → OAuth Apps',
    steps: 'Create an OAuth App and use the callback URL below as "Authorization callback URL".',
  },
  facebook: {
    url: 'https://developers.facebook.com/apps',
    label: 'Meta for Developers → Apps',
    steps: 'Add "Facebook Login" to your app and add the callback URL below to "Valid OAuth Redirect URIs".',
  },
}

function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* clipboard blocked: the value stays selectable */
    }
  }
  return (
    <div>
      <p className="text-xs text-gray-500 mb-1">{label}</p>
      <div className="flex items-center gap-2">
        <code className="flex-1 min-w-0 truncate text-xs bg-gray-50 border border-gray-200 rounded px-2 py-1.5 select-all">{value}</code>
        <button onClick={copy} className="p-1.5 text-gray-500 hover:text-gray-900" title="Copy">
          {copied ? <Check className="w-4 h-4 text-green-600" /> : <Copy className="w-4 h-4" />}
        </button>
      </div>
    </div>
  )
}

function ProviderCard({ p }: { p: ProviderStatus }) {
  const help = CONSOLES[p.id]
  return (
    <div className={`border rounded-lg p-5 bg-white ${p.enabled ? 'border-gray-200' : 'border-gray-200 opacity-70'}`}>
      <div className="flex items-start justify-between gap-3">
        <div>
          <h3 className={`font-semibold ${p.enabled ? 'text-gray-900' : 'text-gray-500'}`}>{p.name}</h3>
          <p className="text-xs text-gray-500 mt-0.5">
            {p.flow === 'redirect' ? 'Browser redirect (web)' : 'ID token from the native SDK (mobile, Apple JS)'}
          </p>
        </div>
        <span
          className={`text-xs font-medium px-2 py-1 rounded-full whitespace-nowrap ${
            p.enabled ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-500'
          }`}
        >
          {p.enabled ? 'Enabled' : 'Not configured'}
        </span>
      </div>

      <p className="text-sm text-gray-600 mt-3">{help.steps}</p>

      <div className="mt-4 space-y-3">
        {p.callback_url && <CopyField label="Callback URL (register it at the provider)" value={p.callback_url} />}
        {p.enabled && p.start_url && <CopyField label="Login link (add ?redirect_url=… to come back to your app)" value={p.start_url} />}
        {p.token_url && <CopyField label="POST the id_token here" value={p.token_url} />}
        {p.client_ids && p.client_ids.length > 0 && (
          <div>
            <p className="text-xs text-gray-500 mb-1">Accepted client IDs</p>
            <div className="flex flex-wrap gap-1">
              {p.client_ids.map((id) => (
                <code key={id} className="text-xs bg-gray-50 border border-gray-200 rounded px-2 py-0.5">{id}</code>
              ))}
            </div>
          </div>
        )}
        <div>
          <p className="text-xs text-gray-500 mb-1">{p.flow === 'id_token' && p.env.length > 1 ? 'Variables (any of them)' : 'Variables'}</p>
          <div className="flex flex-wrap gap-1">
            {p.env.map((e) => (
              <code
                key={e}
                className={`text-xs rounded px-2 py-0.5 border ${
                  p.missing.includes(e) ? 'bg-amber-50 border-amber-200 text-amber-800' : 'bg-green-50 border-green-200 text-green-800'
                }`}
              >
                {e}
              </code>
            ))}
          </div>
        </div>
      </div>

      <a href={help.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm text-blue-600 hover:underline mt-4">
        {help.label} <ExternalLink className="w-3.5 h-3.5" />
      </a>
    </div>
  )
}

const nativeSnippet = (baseUrl: string) => `// After the provider's SDK signs the user in:
const res = await fetch('${baseUrl}/api/v1/auth/oauth/apple/token', { // or /google/token
  method: 'POST',
  headers: { 'Content-Type': 'application/json', apikey: ANON_KEY },
  body: JSON.stringify({
    id_token: credential.identityToken, // Google: googleAuth.idToken
    nonce: rawNonce,                    // only if you passed one to the SDK
    name: fullName,                     // Apple sends the name only the first time
  }),
})
const { token, refresh_token, user } = await res.json() // same as /auth/signin`

export default function AuthProviders({ token }: { token: string | null }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['auth-providers'],
    queryFn: async () => {
      if (!token) throw new Error('Not authenticated')
      const res = await fetch('/api/dashboard/auth/providers', { headers: { Authorization: `Bearer ${token}` } })
      if (!res.ok) throw new Error('Failed to load providers')
      return (await res.json()) as { providers: ProviderStatus[] }
    },
  })

  if (isLoading) {
    return (
      <div className="flex justify-center py-12">
        <Loader2 className="w-6 h-6 animate-spin text-gray-400" />
      </div>
    )
  }
  if (error || !data) {
    return <p className="text-sm text-red-600">{error instanceof Error ? error.message : 'Failed to load providers'}</p>
  }

  const enabled = data.providers.filter((p) => p.enabled).length
  return (
    <div className="space-y-6">
      <div className="flex items-start gap-3 bg-blue-50 border border-blue-100 rounded-lg p-4">
        <KeyRound className="w-5 h-5 text-blue-600 flex-shrink-0 mt-0.5" />
        <div className="text-sm text-blue-900">
          <p className="font-medium">
            {enabled === 0 ? 'No login providers enabled yet' : `${enabled} of ${data.providers.length} login providers enabled`}
          </p>
          <p className="mt-1 text-blue-800">
            Providers are turned on with environment variables. On Rapibase Cloud, set them in the console → your project →
            Settings → Social login; self-hosted, in your <code>.env</code>. Email + password and magic links work without any of
            them.
          </p>
        </div>
      </div>

      <div className="grid md:grid-cols-2 gap-4">
        {data.providers.map((p) => (
          <ProviderCard key={p.id} p={p} />
        ))}
      </div>

      <div className="bg-white border border-gray-200 rounded-lg p-5">
        <h3 className="font-semibold text-gray-900">Native sign-in (Google app, Sign in with Apple)</h3>
        <p className="text-sm text-gray-600 mt-1">
          Mobile apps sign in with the provider's SDK and exchange its <code>id_token</code> for a Rapibase session. Rapibase checks the
          signature, the issuer and that the token was issued for one of your client IDs. If you offer Google sign-in on iOS, the App
          Store also requires Sign in with Apple.
        </p>
        <pre className="mt-3 text-xs bg-gray-900 text-gray-100 rounded-lg p-4 overflow-x-auto">{nativeSnippet(window.location.origin)}</pre>
      </div>
    </div>
  )
}
