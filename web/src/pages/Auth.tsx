import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { 
  Users as UsersIcon, 
  Plus, 
  Trash2, 
  X,
  Loader2,
  Search,
  Copy,
  Check,
  Code,
  BookOpen,
  LogIn,
  LogOut,
  RefreshCw,
  UserPlus,
  Mail,
  CheckCircle
} from 'lucide-react'

const API_BASE = '/api/v1'

// Code snippets for different languages
const getCodeSnippets = (baseUrl: string, anonKey: string) => ({
  javascript: {
    name: 'JavaScript',
    signup: `// Sign up new user
const ANON_KEY = '${anonKey}';

const response = await fetch('${baseUrl}/api/v1/auth/v1/signup', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': ANON_KEY
  },
  body: JSON.stringify({
    email: 'user@example.com',
    password: 'password123',
    full_name: 'John Doe'  // optional
  })
});

const { token, refresh_token, user } = await response.json();
localStorage.setItem('token', token);`,

    signin: `// Sign in user
const ANON_KEY = '${anonKey}';

const response = await fetch('${baseUrl}/api/v1/auth/v1/signin', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': ANON_KEY
  },
  body: JSON.stringify({
    email: 'user@example.com',
    password: 'password123'
  })
});

const { token, refresh_token, user } = await response.json();
localStorage.setItem('token', token);`,
    
    authHeader: `// Make authenticated requests
// After signin, use the JWT token for authenticated requests
const response = await fetch('${baseUrl}/api/v1/your-endpoint', {
  headers: {
    'Authorization': \`Bearer \${token}\`,
    'apikey': ANON_KEY
  }
});`,

    refresh: `// Refresh token
const response = await fetch('${baseUrl}/api/v1/auth/v1/token', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': ANON_KEY
  },
  body: JSON.stringify({
    refresh_token: refreshToken
  })
});

const { token: newToken, refresh_token: newRefresh } = await response.json();`,

    signout: `// Sign out user
await fetch('${baseUrl}/api/v1/auth/v1/signout', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': ANON_KEY
  },
  body: JSON.stringify({
    refresh_token: refreshToken
  })
});

localStorage.removeItem('token');`,

    magiclink: `// Send magic link (passwordless signin)
const response = await fetch('${baseUrl}/api/v1/auth/v1/magiclink', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': ANON_KEY
  },
  body: JSON.stringify({
    email: 'user@example.com',
    redirect_url: 'https://yourapp.com/auth/callback'  // optional
  })
});

// User receives email with magic link
// When clicked, redirects to your app with tokens in URL fragment:
// https://yourapp.com/auth/callback#access_token=...&refresh_token=...`,

    verify: `// Resend verification email
const response = await fetch('${baseUrl}/api/v1/auth/v1/resend', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': ANON_KEY
  },
  body: JSON.stringify({
    email: 'user@example.com',
    redirect_url: 'https://yourapp.com'  // optional
  })
});

// User receives email with verification link
// When clicked, email_verified becomes true`,

    forgotPassword: `// Request password reset
const response = await fetch('${baseUrl}/api/v1/auth/v1/forgot-password', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': ANON_KEY
  },
  body: JSON.stringify({
    email: 'user@example.com',
    redirect_url: 'https://yourapp.com/reset-password'  // optional
  })
});

// Reset password with token
const resetResponse = await fetch('${baseUrl}/api/v1/auth/v1/reset-password', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': ANON_KEY
  },
  body: JSON.stringify({
    token: 'TOKEN_FROM_EMAIL_LINK',
    new_password: 'newSecurePassword123'
  })
});`
  },

  python: {
    name: 'Python',
    signup: `import requests

ANON_KEY = '${anonKey}'

# Sign up new user
response = requests.post(
    '${baseUrl}/api/v1/auth/v1/signup',
    headers={'apikey': ANON_KEY},
    json={
        'email': 'user@example.com',
        'password': 'password123',
        'full_name': 'John Doe'  # optional
    }
)

data = response.json()
token = data['token']
refresh_token = data['refresh_token']
user = data['user']`,

    signin: `import requests

ANON_KEY = '${anonKey}'

# Sign in user
response = requests.post(
    '${baseUrl}/api/v1/auth/v1/signin',
    headers={'apikey': ANON_KEY},
    json={
        'email': 'user@example.com',
        'password': 'password123'
    }
)

data = response.json()
token = data['token']
user = data['user']`,

    authHeader: `# Make authenticated requests
# Use JWT token after signin + apikey
headers = {
    'Authorization': f'Bearer {token}',
    'apikey': ANON_KEY
}
response = requests.get('${baseUrl}/api/v1/your-endpoint', headers=headers)`,

    refresh: `# Refresh token
response = requests.post(
    '${baseUrl}/api/v1/auth/v1/token',
    headers={'apikey': ANON_KEY},
    json={'refresh_token': refresh_token}
)
data = response.json()
new_token = data['token']`,

    signout: `# Sign out user
requests.post(
    '${baseUrl}/api/v1/auth/v1/signout',
    headers={'apikey': ANON_KEY},
    json={'refresh_token': refresh_token}
)`,

    magiclink: `# Send magic link (passwordless signin)
response = requests.post(
    '${baseUrl}/api/v1/auth/v1/magiclink',
    headers={'apikey': ANON_KEY},
    json={
        'email': 'user@example.com',
        'redirect_url': 'https://yourapp.com/auth/callback'
    }
)`,

    verify: `# Resend verification email
response = requests.post(
    '${baseUrl}/api/v1/auth/v1/resend',
    headers={'apikey': ANON_KEY},
    json={
        'email': 'user@example.com',
        'redirect_url': 'https://yourapp.com'
    }
)`,

    forgotPassword: `# Request password reset
requests.post(
    '${baseUrl}/api/v1/auth/v1/forgot-password',
    headers={'apikey': ANON_KEY},
    json={'email': 'user@example.com'}
)

# Reset password with token
requests.post(
    '${baseUrl}/api/v1/auth/v1/reset-password',
    headers={'apikey': ANON_KEY},
    json={
        'token': 'TOKEN_FROM_EMAIL',
        'new_password': 'newSecurePassword123'
    }
)`
  },

  curl: {
    name: 'cURL',
    signup: `# Sign up new user
curl -X POST ${baseUrl}/api/v1/auth/v1/signup \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -d '{"email": "user@example.com", "password": "password123", "full_name": "John Doe"}'`,

    signin: `# Sign in user
curl -X POST ${baseUrl}/api/v1/auth/v1/signin \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -d '{"email": "user@example.com", "password": "password123"}'`,

    authHeader: `# Make authenticated requests
curl ${baseUrl}/api/v1/your-endpoint \\
  -H "Authorization: Bearer YOUR_TOKEN" \\
  -H "apikey: ${anonKey}"`,

    refresh: `# Refresh token
curl -X POST ${baseUrl}/api/v1/auth/v1/token \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -d '{"refresh_token": "YOUR_REFRESH_TOKEN"}'`,

    signout: `# Sign out user
curl -X POST ${baseUrl}/api/v1/auth/v1/signout \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -d '{"refresh_token": "YOUR_REFRESH_TOKEN"}'`,

    magiclink: `# Send magic link
curl -X POST ${baseUrl}/api/v1/auth/v1/magiclink \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -d '{"email": "user@example.com", "redirect_url": "https://yourapp.com/callback"}'`,

    verify: `# Resend verification email
curl -X POST ${baseUrl}/api/v1/auth/v1/resend \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -d '{"email": "user@example.com"}'`,

    forgotPassword: `# Request password reset
curl -X POST ${baseUrl}/api/v1/auth/v1/forgot-password \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -d '{"email": "user@example.com"}'

# Reset password with token
curl -X POST ${baseUrl}/api/v1/auth/v1/reset-password \\
  -H "Content-Type: application/json" \\
  -H "apikey: ${anonKey}" \\
  -d '{"token": "TOKEN_FROM_EMAIL", "new_password": "newPassword123"}'`
  },

  go: {
    name: 'Go',
    signup: `package main

import (
    "bytes"
    "encoding/json"
    "net/http"
)

const ANON_KEY = "${anonKey}"

type AuthResponse struct {
    Token        string \`json:"token"\`
    RefreshToken string \`json:"refresh_token"\`
    User         User   \`json:"user"\`
}

// Sign up new user
func signUp(email, password, fullName string) (*AuthResponse, error) {
    body, _ := json.Marshal(map[string]string{
        "email":     email,
        "password":  password,
        "full_name": fullName,
    })
    
    req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/auth/v1/signup", bytes.NewBuffer(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("apikey", ANON_KEY)
    
    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var auth AuthResponse
    json.NewDecoder(resp.Body).Decode(&auth)
    return &auth, nil
}`,

    signin: `// Sign in user
func signIn(email, password string) (*AuthResponse, error) {
    body, _ := json.Marshal(map[string]string{
        "email":    email,
        "password": password,
    })
    
    req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/auth/v1/signin", bytes.NewBuffer(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("apikey", ANON_KEY)
    
    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var auth AuthResponse
    json.NewDecoder(resp.Body).Decode(&auth)
    return &auth, nil
}`,

    authHeader: `// Make authenticated requests
req, _ := http.NewRequest("GET", "${baseUrl}/api/v1/your-endpoint", nil)
req.Header.Set("Authorization", "Bearer "+token)
req.Header.Set("apikey", ANON_KEY)

client := &http.Client{}
resp, _ := client.Do(req)`,

    refresh: `// Refresh token
body, _ := json.Marshal(map[string]string{
    "refresh_token": refreshToken,
})

req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/auth/v1/token", bytes.NewBuffer(body))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("apikey", ANON_KEY)

client := &http.Client{}
resp, _ := client.Do(req)`,

    signout: `// Sign out user
body, _ := json.Marshal(map[string]string{
    "refresh_token": refreshToken,
})

req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/auth/v1/signout", bytes.NewBuffer(body))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("apikey", ANON_KEY)

client := &http.Client{}
client.Do(req)`,

    magiclink: `// Send magic link
body, _ := json.Marshal(map[string]string{
    "email": "user@example.com",
    "redirect_url": "https://yourapp.com/callback",
})

req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/auth/v1/magiclink", bytes.NewBuffer(body))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("apikey", ANON_KEY)

client := &http.Client{}
client.Do(req)`,

    verify: `// Resend verification email
body, _ := json.Marshal(map[string]string{
    "email": "user@example.com",
})

req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/auth/v1/resend", bytes.NewBuffer(body))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("apikey", ANON_KEY)

client := &http.Client{}
client.Do(req)`,

    forgotPassword: `// Request password reset
body, _ := json.Marshal(map[string]string{
    "email": "user@example.com",
})

req, _ := http.NewRequest("POST", "${baseUrl}/api/v1/auth/v1/forgot-password", bytes.NewBuffer(body))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("apikey", ANON_KEY)

client := &http.Client{}
client.Do(req)

// Reset password with token
resetBody, _ := json.Marshal(map[string]string{
    "token": "TOKEN_FROM_EMAIL",
    "new_password": "newPassword123",
})

req, _ = http.NewRequest("POST", "${baseUrl}/api/v1/auth/v1/reset-password", bytes.NewBuffer(resetBody))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("apikey", ANON_KEY)
client.Do(req)`
  },

  php: {
    name: 'PHP',
    signup: `<?php
$ANON_KEY = '${anonKey}';

// Sign up new user
$ch = curl_init('${baseUrl}/api/v1/auth/v1/signup');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    'email' => 'user@example.com',
    'password' => 'password123',
    'full_name' => 'John Doe'
]));

$response = curl_exec($ch);
$data = json_decode($response, true);
$token = $data['token'];
$user = $data['user'];
curl_close($ch);`,

    signin: `<?php
$ANON_KEY = '${anonKey}';

// Sign in user
$ch = curl_init('${baseUrl}/api/v1/auth/v1/signin');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    'email' => 'user@example.com',
    'password' => 'password123'
]));

$response = curl_exec($ch);
$data = json_decode($response, true);
$token = $data['token'];
curl_close($ch);`,

    authHeader: `<?php
// Make authenticated requests
$ch = curl_init('${baseUrl}/api/v1/your-endpoint');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Authorization: Bearer ' . $token,
    'apikey: ' . $ANON_KEY
]);

$response = curl_exec($ch);
curl_close($ch);`,

    refresh: `<?php
// Refresh token
$ch = curl_init('${baseUrl}/api/v1/auth/v1/token');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    'refresh_token' => $refreshToken
]));

$response = curl_exec($ch);
$newToken = json_decode($response, true)['token'];
curl_close($ch);`,

    signout: `<?php
// Sign out user
$ch = curl_init('${baseUrl}/api/v1/auth/v1/signout');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    'refresh_token' => $refreshToken
]));

curl_exec($ch);
curl_close($ch);`,

    magiclink: `<?php
// Send magic link
$ch = curl_init('${baseUrl}/api/v1/auth/v1/magiclink');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    'email' => 'user@example.com',
    'redirect_url' => 'https://yourapp.com/callback'
]));

curl_exec($ch);
curl_close($ch);`,

    verify: `<?php
// Resend verification email
$ch = curl_init('${baseUrl}/api/v1/auth/v1/resend');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    'email' => 'user@example.com'
]));

curl_exec($ch);
curl_close($ch);`,

    forgotPassword: `<?php
// Request password reset
$ch = curl_init('${baseUrl}/api/v1/auth/v1/forgot-password');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    'email' => 'user@example.com'
]));
curl_exec($ch);
curl_close($ch);

// Reset password with token
$ch = curl_init('${baseUrl}/api/v1/auth/v1/reset-password');
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Content-Type: application/json',
    'apikey: ' . $ANON_KEY
]);
curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
    'token' => 'TOKEN_FROM_EMAIL',
    'new_password' => 'newPassword123'
]));
curl_exec($ch);
curl_close($ch);`
  }
})

function getAuthToken() {
  const stored = localStorage.getItem('rapibase-auth')
  if (!stored) return null
  try {
    const parsed = JSON.parse(stored)
    return parsed?.state?.token || null
  } catch {
    return null
  }
}

async function fetchUsers() {
  const token = getAuthToken()
  if (!token) throw new Error('Not authenticated')

  const res = await fetch(`${API_BASE}/auth/users`, {
    headers: { Authorization: `Bearer ${token}` }
  })
  if (!res.ok) throw new Error('Failed to fetch users')
  return res.json()
}

async function createUser(data: { email: string; password: string; full_name?: string; email_verified: boolean }) {
  const token = getAuthToken()
  if (!token) throw new Error('Not authenticated')

  const res = await fetch(`${API_BASE}/auth/users`, {
    method: 'POST',
    headers: { 
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}` 
    },
    body: JSON.stringify(data)
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to create user')
  }
  return res.json()
}

async function deleteUser(id: string) {
  const token = getAuthToken()
  if (!token) throw new Error('Not authenticated')

  const res = await fetch(`${API_BASE}/auth/users/${id}`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${token}` }
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to delete user')
  }
  return res.json()
}

interface User {
  id: string
  email: string
  email_verified: boolean
  full_name?: string
  provider: string
  last_sign_in?: string
  created_at: string
}

type TabType = 'users' | 'docs'
type DocSection = 'signup' | 'signin' | 'magiclink' | 'authHeader' | 'refresh' | 'signout' | 'verify' | 'forgotPassword'

export default function Auth() {
  const queryClient = useQueryClient()
  const [activeTab, setActiveTab] = useState<TabType>('users')
  const [showCreate, setShowCreate] = useState(false)
  const [search, setSearch] = useState('')
  const [selectedLang, setSelectedLang] = useState('javascript')
  const [selectedSection, setSelectedSection] = useState<DocSection>('signup')
  const [copiedCode, setCopiedCode] = useState<string | null>(null)
  
  // Create form
  const [newEmail, setNewEmail] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newFullName, setNewFullName] = useState('')
  const [newEmailVerified, setNewEmailVerified] = useState(false)

  const baseUrl = window.location.origin

  // Fetch project info (API keys)
  const { data: projectData } = useQuery({
    queryKey: ['project'],
    queryFn: async () => {
      const token = getAuthToken()
      if (!token) throw new Error('Not authenticated')
      const res = await fetch(`${API_BASE}/project`, {
        headers: { Authorization: `Bearer ${token}` }
      })
      if (!res.ok) throw new Error('Failed to fetch project')
      return res.json()
    },
  })

  const anonKey = projectData?.anon_key || 'YOUR_ANON_KEY'
  const serviceKey = projectData?.service_key || 'YOUR_SERVICE_KEY'
  const snippets = getCodeSnippets(baseUrl, anonKey)

  const { data, isLoading } = useQuery({
    queryKey: ['auth-users'],
    queryFn: fetchUsers,
  })

  const createMutation = useMutation({
    mutationFn: createUser,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['auth-users'] })
      setShowCreate(false)
      setNewEmail('')
      setNewPassword('')
      setNewFullName('')
      setNewEmailVerified(false)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteUser,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['auth-users'] })
    },
  })

  const copyToClipboard = (code: string, id: string) => {
    navigator.clipboard.writeText(code)
    setCopiedCode(id)
    setTimeout(() => setCopiedCode(null), 2000)
  }

  const users: User[] = data?.users || []
  const filteredUsers = users.filter(u => 
    u.email.toLowerCase().includes(search.toLowerCase()) ||
    (u.full_name && u.full_name.toLowerCase().includes(search.toLowerCase()))
  )

  const docSections = [
    { id: 'signup' as const, name: 'Sign Up', icon: UserPlus },
    { id: 'signin' as const, name: 'Sign In', icon: LogIn },
    { id: 'magiclink' as const, name: 'Magic Link', icon: Mail },
    { id: 'verify' as const, name: 'Verify Email', icon: CheckCircle },
    { id: 'forgotPassword' as const, name: 'Forgot Password', icon: RefreshCw },
    { id: 'authHeader' as const, name: 'Auth Headers', icon: Code },
    { id: 'refresh' as const, name: 'Refresh Token', icon: RefreshCw },
    { id: 'signout' as const, name: 'Sign Out', icon: LogOut },
  ]

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Authentication</h1>
          <p className="text-gray-600 mt-1">Manage users and integrate authentication in your apps</p>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 mb-6 bg-gray-100 p-1 rounded-lg w-fit">
        <button
          onClick={() => setActiveTab('users')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'users' 
              ? 'bg-white text-gray-900 shadow-sm' 
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <span className="flex items-center gap-2">
            <UsersIcon className="w-4 h-4" />
            Users ({data?.total || 0})
          </span>
        </button>
        <button
          onClick={() => setActiveTab('docs')}
          className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
            activeTab === 'docs' 
              ? 'bg-white text-gray-900 shadow-sm' 
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          <span className="flex items-center gap-2">
            <BookOpen className="w-4 h-4" />
            API Docs
          </span>
        </button>
      </div>

      {/* Users Tab */}
      {activeTab === 'users' && (
        <>
          <div className="flex items-center justify-between mb-6">
            <div className="relative flex-1 max-w-md">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-5 h-5 text-gray-400" />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search users..."
                className="w-full pl-10 pr-4 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
              />
            </div>
            <button
              onClick={() => setShowCreate(true)}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors ml-4"
            >
              <Plus className="w-4 h-4" />
              Add User
            </button>
          </div>

          {/* Create User Modal */}
          {showCreate && (
            <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
              <div className="bg-white rounded-xl shadow-xl w-full max-w-md">
                <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold">Add New User</h2>
                  <button onClick={() => setShowCreate(false)} className="text-gray-500 hover:text-gray-700">
                    <X className="w-5 h-5" />
                  </button>
                </div>

                <div className="p-6 space-y-4">
                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-1">Email *</label>
                    <input
                      type="email"
                      value={newEmail}
                      onChange={(e) => setNewEmail(e.target.value)}
                      className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                      placeholder="user@example.com"
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-1">Password *</label>
                    <input
                      type="password"
                      value={newPassword}
                      onChange={(e) => setNewPassword(e.target.value)}
                      className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                      placeholder="••••••••"
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-1">Full Name</label>
                    <input
                      type="text"
                      value={newFullName}
                      onChange={(e) => setNewFullName(e.target.value)}
                      className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                      placeholder="John Doe"
                    />
                  </div>

                  <div className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      id="newEmailVerified"
                      checked={newEmailVerified}
                      onChange={(e) => setNewEmailVerified(e.target.checked)}
                      className="rounded"
                    />
                    <label htmlFor="newEmailVerified" className="text-sm text-gray-700">
                      Email verified
                    </label>
                  </div>

                  {createMutation.isError && (
                    <div className="p-3 bg-red-50 border border-red-200 rounded-lg text-red-700 text-sm">
                      {(createMutation.error as Error).message}
                    </div>
                  )}
                </div>

                <div className="flex justify-end gap-3 px-6 py-4 border-t border-gray-200">
                  <button
                    onClick={() => setShowCreate(false)}
                    className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={() => createMutation.mutate({ 
                      email: newEmail, 
                      password: newPassword, 
                      full_name: newFullName || undefined,
                      email_verified: newEmailVerified 
                    })}
                    disabled={!newEmail || !newPassword || createMutation.isPending}
                    className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                  >
                    {createMutation.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
                    Create User
                  </button>
                </div>
              </div>
            </div>
          )}

          {/* Users List */}
          <div className="bg-white rounded-xl border border-gray-200">
            {isLoading ? (
              <div className="flex items-center justify-center py-12">
                <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
              </div>
            ) : filteredUsers.length === 0 ? (
              <div className="text-center py-12">
                <UsersIcon className="w-12 h-12 text-gray-400 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-gray-900 mb-2">
                  {search ? 'No users found' : 'No users yet'}
                </h3>
                <p className="text-gray-600">
                  {search ? 'Try a different search term' : 'Users who sign up will appear here'}
                </p>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full">
                  <thead>
                    <tr className="bg-gray-50 border-b border-gray-200">
                      <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                        User
                      </th>
                      <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                        Provider
                      </th>
                      <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                        Status
                      </th>
                      <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                        Last Sign In
                      </th>
                      <th className="px-6 py-3 text-right text-xs font-medium text-gray-500 uppercase tracking-wider">
                        Actions
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-200">
                    {filteredUsers.map((user) => (
                      <tr key={user.id} className="hover:bg-gray-50">
                        <td className="px-6 py-4">
                          <div className="flex items-center gap-3">
                            <div className="w-10 h-10 rounded-full bg-blue-100 flex items-center justify-center">
                              <span className="text-blue-600 font-medium">
                                {(user.full_name || user.email).charAt(0).toUpperCase()}
                              </span>
                            </div>
                            <div>
                              <p className="font-medium text-gray-900">{user.full_name || user.email}</p>
                              <p className="text-sm text-gray-500">{user.email}</p>
                            </div>
                          </div>
                        </td>
                        <td className="px-6 py-4">
                          <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium bg-gray-100 text-gray-700">
                            <Mail className="w-3 h-3" />
                            {user.provider}
                          </span>
                        </td>
                        <td className="px-6 py-4">
                          {user.email_verified ? (
                            <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium bg-green-100 text-green-700">
                              <CheckCircle className="w-3 h-3" />
                              Verified
                            </span>
                          ) : (
                            <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium bg-yellow-100 text-yellow-700">
                              Pending
                            </span>
                          )}
                        </td>
                        <td className="px-6 py-4 text-sm text-gray-500">
                          {user.last_sign_in 
                            ? new Date(user.last_sign_in).toLocaleString()
                            : 'Never'
                          }
                        </td>
                        <td className="px-6 py-4 text-right">
                          <div className="flex items-center justify-end gap-1">
                            {!user.email_verified && (
                              <button
                                onClick={async () => {
                                  await fetch(`${API_BASE}/auth/v1/resend`, {
                                    method: 'POST',
                                    headers: { 
                                      'Content-Type': 'application/json',
                                      'apikey': anonKey
                                    },
                                    body: JSON.stringify({ email: user.email })
                                  })
                                  alert('Verification email sent!')
                                }}
                                className="p-2 text-gray-500 hover:text-green-600 hover:bg-green-50 rounded transition-colors"
                                title="Resend verification email"
                              >
                                <CheckCircle className="w-4 h-4" />
                              </button>
                            )}
                            <button
                              onClick={async () => {
                                await fetch(`${API_BASE}/auth/v1/magiclink`, {
                                  method: 'POST',
                                  headers: { 
                                    'Content-Type': 'application/json',
                                    'apikey': anonKey
                                  },
                                  body: JSON.stringify({ email: user.email })
                                })
                                alert('Magic link sent!')
                              }}
                              className="p-2 text-gray-500 hover:text-purple-600 hover:bg-purple-50 rounded transition-colors"
                              title="Send magic link"
                            >
                              <Mail className="w-4 h-4" />
                            </button>
                            <button
                              onClick={() => {
                                if (confirm(`Delete user "${user.email}"?`)) {
                                  deleteMutation.mutate(user.id)
                                }
                              }}
                              className="p-2 text-gray-500 hover:text-red-600 hover:bg-red-50 rounded transition-colors"
                              title="Delete user"
                            >
                              <Trash2 className="w-4 h-4" />
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}

      {/* Docs Tab */}
      {activeTab === 'docs' && (
        <div className="grid lg:grid-cols-4 gap-6">
          {/* Sidebar */}
          <div className="lg:col-span-1">
            <div className="bg-white rounded-xl border border-gray-200 p-4 sticky top-4">
              <h3 className="text-sm font-semibold text-gray-900 mb-3">Endpoints</h3>
              <nav className="space-y-1">
                {docSections.map((section) => (
                  <button
                    key={section.id}
                    onClick={() => setSelectedSection(section.id)}
                    className={`w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-colors ${
                      selectedSection === section.id
                        ? 'bg-blue-50 text-blue-700 font-medium'
                        : 'text-gray-700 hover:bg-gray-100'
                    }`}
                  >
                    <section.icon className="w-4 h-4" />
                    {section.name}
                  </button>
                ))}
              </nav>

              <div className="mt-6 pt-4 border-t border-gray-200">
                <h3 className="text-sm font-semibold text-gray-900 mb-3">Language</h3>
                <select
                  value={selectedLang}
                  onChange={(e) => setSelectedLang(e.target.value)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                >
                  {Object.entries(snippets).map(([key, value]) => (
                    <option key={key} value={key}>{value.name}</option>
                  ))}
                </select>
              </div>

              <div className="mt-6 pt-4 border-t border-gray-200">
                <h3 className="text-sm font-semibold text-gray-900 mb-2">Base URL</h3>
                <code className="block p-2 bg-gray-100 rounded text-xs text-gray-700 break-all">
                  {baseUrl}/api/v1/auth/v1
                </code>
              </div>

              <div className="mt-6 pt-4 border-t border-gray-200">
                <h3 className="text-sm font-semibold text-gray-900 mb-2">API Keys</h3>
                <div className="space-y-3">
                  <div>
                    <label className="text-xs text-gray-500 block mb-1">Anon Key (public)</label>
                    <div className="flex items-center gap-1">
                      <code className="flex-1 p-2 bg-gray-100 rounded text-xs text-gray-700 truncate" title={anonKey}>
                        {anonKey.slice(0, 20)}...
                      </code>
                      <button
                        onClick={() => copyToClipboard(anonKey, 'anon-key')}
                        className="p-1.5 hover:bg-gray-100 rounded text-gray-500 hover:text-gray-700"
                      >
                        {copiedCode === 'anon-key' ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                      </button>
                    </div>
                  </div>
                  <div>
                    <label className="text-xs text-gray-500 block mb-1">Service Key (secret)</label>
                    <div className="flex items-center gap-1">
                      <code className="flex-1 p-2 bg-red-50 rounded text-xs text-red-700 truncate" title="Keep this secret!">
                        {serviceKey.slice(0, 20)}...
                      </code>
                      <button
                        onClick={() => copyToClipboard(serviceKey, 'service-key')}
                        className="p-1.5 hover:bg-gray-100 rounded text-gray-500 hover:text-gray-700"
                      >
                        {copiedCode === 'service-key' ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                      </button>
                    </div>
                    <p className="text-xs text-red-600 mt-1">⚠️ Never expose in client-side code</p>
                  </div>
                </div>
              </div>
            </div>
          </div>

          {/* Content */}
          <div className="lg:col-span-3 space-y-6">
            {/* Sign Up Section */}
            {selectedSection === 'signup' && (
              <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold text-gray-900">Sign Up</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    Register a new user account. Returns JWT tokens for immediate authentication.
                  </p>
                </div>
                <div className="p-6">
                  <div className="mb-4">
                    <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-green-100 text-green-700">
                      POST
                    </span>
                    <code className="ml-2 text-sm text-gray-700">/api/v1/auth/v1/signup</code>
                  </div>
                  <div className="relative">
                    <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                      <code>{snippets[selectedLang as keyof typeof snippets].signup}</code>
                    </pre>
                    <button
                      onClick={() => copyToClipboard(snippets[selectedLang as keyof typeof snippets].signup, 'signup')}
                      className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                    >
                      {copiedCode === 'signup' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                    </button>
                  </div>
                  <div className="mt-4 p-4 bg-blue-50 rounded-lg">
                    <h4 className="text-sm font-medium text-blue-900 mb-2">Response</h4>
                    <pre className="text-xs text-blue-800">
{`{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "a1b2c3d4e5f6...",
  "user": {
    "id": "uuid-here",
    "email": "user@example.com",
    "full_name": "John Doe",
    "email_verified": false,
    "provider": "email"
  }
}`}
                    </pre>
                  </div>
                </div>
              </div>
            )}

            {/* Sign In Section */}
            {selectedSection === 'signin' && (
              <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold text-gray-900">Sign In</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    Authenticate an existing user with email and password.
                  </p>
                </div>
                <div className="p-6">
                  <div className="mb-4">
                    <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-green-100 text-green-700">
                      POST
                    </span>
                    <code className="ml-2 text-sm text-gray-700">/api/v1/auth/v1/signin</code>
                  </div>
                  <div className="relative">
                    <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                      <code>{snippets[selectedLang as keyof typeof snippets].signin}</code>
                    </pre>
                    <button
                      onClick={() => copyToClipboard(snippets[selectedLang as keyof typeof snippets].signin, 'signin')}
                      className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                    >
                      {copiedCode === 'signin' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                    </button>
                  </div>
                </div>
              </div>
            )}

            {/* Auth Header Section */}
            {selectedSection === 'authHeader' && (
              <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold text-gray-900">Authenticated Requests</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    Include the JWT token in the Authorization header for protected endpoints.
                  </p>
                </div>
                <div className="p-6">
                  <div className="mb-4 p-4 bg-yellow-50 border border-yellow-200 rounded-lg">
                    <p className="text-sm text-yellow-800">
                      <strong>Note:</strong> The token expires in 24 hours. Use the refresh token to get a new access token.
                    </p>
                  </div>
                  <div className="relative">
                    <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                      <code>{snippets[selectedLang as keyof typeof snippets].authHeader}</code>
                    </pre>
                    <button
                      onClick={() => copyToClipboard(snippets[selectedLang as keyof typeof snippets].authHeader, 'authHeader')}
                      className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                    >
                      {copiedCode === 'authHeader' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                    </button>
                  </div>
                </div>
              </div>
            )}

            {/* Refresh Token Section */}
            {selectedSection === 'refresh' && (
              <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold text-gray-900">Refresh Token</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    Get a new access token using a refresh token.
                  </p>
                </div>
                <div className="p-6">
                  <div className="mb-4">
                    <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-green-100 text-green-700">
                      POST
                    </span>
                    <code className="ml-2 text-sm text-gray-700">/api/v1/auth/v1/token</code>
                  </div>
                  <div className="relative">
                    <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                      <code>{snippets[selectedLang as keyof typeof snippets].refresh}</code>
                    </pre>
                    <button
                      onClick={() => copyToClipboard(snippets[selectedLang as keyof typeof snippets].refresh, 'refresh')}
                      className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                    >
                      {copiedCode === 'refresh' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                    </button>
                  </div>
                  <div className="mt-4 p-4 bg-gray-50 rounded-lg">
                    <h4 className="text-sm font-medium text-gray-900 mb-2">Token Expiration</h4>
                    <ul className="text-sm text-gray-600 space-y-1">
                      <li>• <strong>Access Token:</strong> 24 hours</li>
                      <li>• <strong>Refresh Token:</strong> 7 days</li>
                    </ul>
                  </div>
                </div>
              </div>
            )}

            {/* Sign Out Section */}
            {selectedSection === 'signout' && (
              <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold text-gray-900">Sign Out</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    Invalidate the refresh token to sign out the user.
                  </p>
                </div>
                <div className="p-6">
                  <div className="mb-4">
                    <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-green-100 text-green-700">
                      POST
                    </span>
                    <code className="ml-2 text-sm text-gray-700">/api/v1/auth/v1/signout</code>
                  </div>
                  <div className="relative">
                    <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                      <code>{snippets[selectedLang as keyof typeof snippets].signout}</code>
                    </pre>
                    <button
                      onClick={() => copyToClipboard(snippets[selectedLang as keyof typeof snippets].signout, 'signout')}
                      className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                    >
                      {copiedCode === 'signout' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                    </button>
                  </div>
                </div>
              </div>
            )}

            {/* Magic Link Section */}
            {selectedSection === 'magiclink' && (
              <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold text-gray-900">Magic Link</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    Passwordless authentication. Send a magic link to the user's email for instant sign in.
                  </p>
                </div>
                <div className="p-6">
                  <div className="mb-4">
                    <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-green-100 text-green-700">
                      POST
                    </span>
                    <code className="ml-2 text-sm text-gray-700">/api/v1/auth/v1/magiclink</code>
                  </div>
                  <div className="relative">
                    <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                      <code>{snippets[selectedLang as keyof typeof snippets].magiclink}</code>
                    </pre>
                    <button
                      onClick={() => copyToClipboard(snippets[selectedLang as keyof typeof snippets].magiclink, 'magiclink')}
                      className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                    >
                      {copiedCode === 'magiclink' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                    </button>
                  </div>
                  <div className="mt-4 p-4 bg-purple-50 rounded-lg">
                    <h4 className="text-sm font-medium text-purple-900 mb-2">How it works</h4>
                    <ol className="text-xs text-purple-800 list-decimal list-inside space-y-1">
                      <li>User requests magic link with their email</li>
                      <li>Email is sent with a unique link (valid for 15 minutes)</li>
                      <li>User clicks link → redirected to your app with tokens in URL fragment</li>
                      <li>Your app extracts tokens and user is signed in</li>
                    </ol>
                  </div>
                </div>
              </div>
            )}

            {/* Verify Email Section */}
            {selectedSection === 'verify' && (
              <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold text-gray-900">Verify Email</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    Resend verification email to confirm user's email address.
                  </p>
                </div>
                <div className="p-6">
                  <div className="mb-4">
                    <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-green-100 text-green-700">
                      POST
                    </span>
                    <code className="ml-2 text-sm text-gray-700">/api/v1/auth/v1/resend</code>
                  </div>
                  <div className="relative">
                    <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                      <code>{snippets[selectedLang as keyof typeof snippets].verify}</code>
                    </pre>
                    <button
                      onClick={() => copyToClipboard(snippets[selectedLang as keyof typeof snippets].verify, 'verify')}
                      className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                    >
                      {copiedCode === 'verify' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                    </button>
                  </div>
                  <div className="mt-4 p-4 bg-green-50 rounded-lg">
                    <h4 className="text-sm font-medium text-green-900 mb-2">Verification Flow</h4>
                    <p className="text-xs text-green-800">
                      When user clicks the verification link, their <code className="bg-green-100 px-1 rounded">email_verified</code> field is set to <code className="bg-green-100 px-1 rounded">true</code>.
                    </p>
                  </div>
                </div>
              </div>
            )}

            {/* Forgot Password Section */}
            {selectedSection === 'forgotPassword' && (
              <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                <div className="px-6 py-4 border-b border-gray-200">
                  <h2 className="text-lg font-semibold text-gray-900">Forgot Password</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    Send password reset email and reset user's password with token.
                  </p>
                </div>
                <div className="p-6">
                  <div className="mb-4">
                    <span className="inline-flex items-center px-2.5 py-1 rounded text-xs font-medium bg-green-100 text-green-700">
                      POST
                    </span>
                    <code className="ml-2 text-sm text-gray-700">/api/v1/auth/v1/forgot-password</code>
                    <span className="mx-2 text-gray-400">→</span>
                    <code className="text-sm text-gray-700">/api/v1/auth/v1/reset-password</code>
                  </div>
                  <div className="relative">
                    <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
                      <code>{snippets[selectedLang as keyof typeof snippets].forgotPassword}</code>
                    </pre>
                    <button
                      onClick={() => copyToClipboard(snippets[selectedLang as keyof typeof snippets].forgotPassword, 'forgotPassword')}
                      className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
                    >
                      {copiedCode === 'forgotPassword' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                    </button>
                  </div>
                  <div className="mt-4 p-4 bg-orange-50 rounded-lg">
                    <h4 className="text-sm font-medium text-orange-900 mb-2">Password Reset Flow</h4>
                    <ol className="text-xs text-orange-800 list-decimal list-inside space-y-1">
                      <li>Call <code className="bg-orange-100 px-1 rounded">forgot-password</code> with user's email</li>
                      <li>User receives email with reset link (valid for 1 hour)</li>
                      <li>Your app shows password reset form with token from URL</li>
                      <li>Call <code className="bg-orange-100 px-1 rounded">reset-password</code> with token and new password</li>
                    </ol>
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
