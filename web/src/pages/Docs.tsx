import { useState } from 'react'
import { 
  Book, 
  Copy, 
  Check, 
  Key, 
  Users, 
  Database, 
  Mail,
  Shield,
  Zap,
  Code,
  FileText,
  ChevronRight
} from 'lucide-react'

type DocSection = 'overview' | 'quickstart' | 'auth-flow' | 'api-keys' | 'rest-api' | 'email-flows' | 'examples'

export default function Docs() {
  const [copiedSection, setCopiedSection] = useState<string | null>(null)
  const [activeSection, setActiveSection] = useState<DocSection>('overview')

  const copyToClipboard = (text: string, section: string) => {
    navigator.clipboard.writeText(text)
    setCopiedSection(section)
    setTimeout(() => setCopiedSection(null), 2000)
  }

  const sections: { id: DocSection; title: string; icon: React.ReactNode }[] = [
    { id: 'overview', title: 'Overview', icon: <Book className="w-4 h-4" /> },
    { id: 'quickstart', title: 'Quick Start', icon: <Zap className="w-4 h-4" /> },
    { id: 'api-keys', title: 'API Keys', icon: <Key className="w-4 h-4" /> },
    { id: 'auth-flow', title: 'Authentication Flow', icon: <Users className="w-4 h-4" /> },
    { id: 'email-flows', title: 'Email Flows', icon: <Mail className="w-4 h-4" /> },
    { id: 'rest-api', title: 'REST API', icon: <Database className="w-4 h-4" /> },
    { id: 'examples', title: 'Full Examples', icon: <Code className="w-4 h-4" /> },
  ]

  const fullDocumentation = `# RapiBase Documentation

## Overview

RapiBase is a backend-as-a-service that provides:
- **Authentication**: User signup, signin, magic links, email verification, password reset
- **REST API**: CRUD operations on your database tables
- **API Keys**: Anon key (public) and Service key (admin)

## API Keys

### Anon Key (Public)
- Safe to use in client-side code (browsers, mobile apps)
- Requires JWT token for data access
- Use for: User-facing applications

### Service Key (Secret)
- NEVER expose in client-side code
- Full access without JWT
- Use for: Backend scripts, admin dashboards, internal tools

## Authentication Endpoints

Base URL: \`/api/v1/auth\`

### Sign Up
\`\`\`
POST /api/v1/auth/signup
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { "email": "user@example.com", "password": "securepass", "full_name": "John Doe" }

Response: {
  "token": "eyJhbG...",
  "refresh_token": "abc123...",
  "expires_in": 3600,
  "user": { "id": "uuid", "email": "user@example.com", ... }
}
\`\`\`

### Sign In
\`\`\`
POST /api/v1/auth/signin
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { "email": "user@example.com", "password": "securepass" }

Response: {
  "token": "eyJhbG...",
  "refresh_token": "abc123...",
  "expires_in": 3600,
  "user": { "id": "uuid", "email": "user@example.com", ... }
}
\`\`\`

### Refresh Token
\`\`\`
POST /api/v1/auth/token
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { "refresh_token": "abc123..." }

Response: {
  "token": "eyJhbG...",
  "refresh_token": "newtoken...",  // Rotated!
  "expires_in": 3600
}
\`\`\`

### Sign Out
\`\`\`
POST /api/v1/auth/signout
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { "refresh_token": "abc123..." }
\`\`\`

## Email Flows

### Magic Link (Passwordless Sign In)

**Step 1: Request magic link**
\`\`\`
POST /api/v1/auth/magiclink
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { 
  "email": "user@example.com",
  "redirect_url": "https://yourapp.com/auth/callback"  // optional
}
\`\`\`

**Step 2: User clicks email link**
RapiBase validates the token and redirects to your app:
\`\`\`
https://yourapp.com/auth/callback#access_token=eyJhbG...&refresh_token=abc123...&expires_in=3600&type=magiclink
\`\`\`

**Step 3: Your app extracts tokens from URL fragment**
\`\`\`javascript
// In your /auth/callback page
function handleCallback() {
  const hash = window.location.hash.substring(1);
  const params = new URLSearchParams(hash);
  
  const accessToken = params.get('access_token');
  const refreshToken = params.get('refresh_token');
  const error = params.get('error');
  
  if (error) {
    // Handle error: 'invalid_token', 'missing_token', etc.
    return;
  }
  
  if (accessToken) {
    // Save tokens
    localStorage.setItem('access_token', accessToken);
    localStorage.setItem('refresh_token', refreshToken);
    
    // Redirect to dashboard
    window.location.href = '/dashboard';
  }
}
handleCallback();
\`\`\`

### Email Verification

**Step 1: Request verification email**
\`\`\`
POST /api/v1/auth/resend
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { 
  "email": "user@example.com",
  "redirect_url": "https://yourapp.com/verified"  // optional
}
\`\`\`

**Step 2: User clicks email link**
RapiBase verifies and redirects:
\`\`\`
https://yourapp.com/verified?verified=true&email=user@example.com
\`\`\`

### Password Reset

**Step 1: Request reset email**
\`\`\`
POST /api/v1/auth/forgot-password
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { 
  "email": "user@example.com",
  "redirect_url": "https://yourapp.com/reset-password"  // optional
}
\`\`\`

**Step 2: User clicks email link**
Redirects to your reset page with token:
\`\`\`
https://yourapp.com/reset-password?token=abc123...
\`\`\`

**Step 3: Submit new password**
\`\`\`
POST /api/v1/auth/reset-password
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { 
  "token": "abc123...",
  "new_password": "newsecurepass"
}
\`\`\`

## REST API

Base URL: \`/api/v1/rest\`

### With Anon Key (requires JWT)
\`\`\`
GET /api/v1/rest/products
Headers: { 
  "apikey": "ANON_KEY",
  "Authorization": "Bearer eyJhbG..."
}
\`\`\`

### With Service Key (no JWT needed)
\`\`\`
GET /api/v1/rest/products
Headers: { "apikey": "SERVICE_KEY" }
\`\`\`

### CRUD Operations

**SELECT (GET)**
\`\`\`
GET /api/v1/rest/{table}?page=1&page_size=50&order_by=created_at&order_dir=desc
GET /api/v1/rest/{table}?filter=status:eq:active
\`\`\`

**INSERT (POST)**
\`\`\`
POST /api/v1/rest/{table}
Body: { "name": "Product", "price": 99.99 }
\`\`\`

**UPDATE (PUT)**
\`\`\`
PUT /api/v1/rest/{table}/{id}
Body: { "price": 149.99 }
\`\`\`

**DELETE (DELETE)**
\`\`\`
DELETE /api/v1/rest/{table}/{id}
\`\`\`

## Full Integration Example

### React App with Magic Link

\`\`\`javascript
// config.js
export const RAPIBASE_URL = 'https://your-rapibase.com';
export const ANON_KEY = 'your-anon-key';

// auth.js
export async function sendMagicLink(email) {
  const response = await fetch(\`\${RAPIBASE_URL}/api/v1/auth/magiclink\`, {
    method: 'POST',
    headers: { 
      'Content-Type': 'application/json',
      'apikey': ANON_KEY
    },
    body: JSON.stringify({
      email,
      redirect_url: window.location.origin + '/auth/callback'
    })
  });
  return response.json();
}

export function getAccessToken() {
  return localStorage.getItem('access_token');
}

export function isAuthenticated() {
  return !!getAccessToken();
}

export async function fetchWithAuth(url, options = {}) {
  const token = getAccessToken();
  return fetch(url, {
    ...options,
    headers: {
      ...options.headers,
      'apikey': ANON_KEY,
      'Authorization': \`Bearer \${token}\`
    }
  });
}

// pages/Login.jsx
function Login() {
  const [email, setEmail] = useState('');
  const [sent, setSent] = useState(false);
  
  const handleSubmit = async (e) => {
    e.preventDefault();
    await sendMagicLink(email);
    setSent(true);
  };
  
  if (sent) {
    return <p>Check your email for the magic link!</p>;
  }
  
  return (
    <form onSubmit={handleSubmit}>
      <input 
        type="email" 
        value={email} 
        onChange={(e) => setEmail(e.target.value)}
        placeholder="Enter your email"
      />
      <button type="submit">Send Magic Link</button>
    </form>
  );
}

// pages/AuthCallback.jsx
function AuthCallback() {
  useEffect(() => {
    const hash = window.location.hash.substring(1);
    const params = new URLSearchParams(hash);
    
    const accessToken = params.get('access_token');
    const refreshToken = params.get('refresh_token');
    
    if (accessToken) {
      localStorage.setItem('access_token', accessToken);
      localStorage.setItem('refresh_token', refreshToken);
      window.location.href = '/dashboard';
    }
  }, []);
  
  return <p>Signing you in...</p>;
}

// pages/Dashboard.jsx
function Dashboard() {
  const [products, setProducts] = useState([]);
  
  useEffect(() => {
    fetchWithAuth(\`\${RAPIBASE_URL}/api/v1/rest/products\`)
      .then(res => res.json())
      .then(data => setProducts(data.data));
  }, []);
  
  return (
    <ul>
      {products.map(p => <li key={p.id}>{p.name}</li>)}
    </ul>
  );
}
\`\`\`

## Environment Variables

\`\`\`env
# RapiBase Server
DATABASE_URL=postgres://user:pass@localhost:5432/rapibase
PORT=8080
APP_URL=http://localhost:8080

# Auth
JWT_SECRET=your-secret-key
JWT_EXPIRY=1h
REFRESH_EXPIRY=7d

# API Keys
ANON_KEY=your-anon-key
SERVICE_KEY=your-service-key

# SMTP (for emails)
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=your-email
SMTP_PASS=your-password
SMTP_FROM=noreply@example.com
SMTP_FROM_NAME=Your App

# Redirect URL for email callbacks
AUTH_REDIRECT_URL=https://yourapp.com
\`\`\`
`

  const renderContent = () => {
    switch (activeSection) {
      case 'overview':
        return (
          <div className="space-y-6">
            <div>
              <h2 className="text-2xl font-bold text-gray-900 mb-4">Overview</h2>
              <p className="text-gray-600 mb-4">
                RapiBase is a backend-as-a-service that provides authentication, database access, and API management for your applications.
              </p>
            </div>
            
            <div className="grid md:grid-cols-3 gap-4">
              <div className="bg-blue-50 rounded-lg p-4 border border-blue-200">
                <Users className="w-8 h-8 text-blue-600 mb-2" />
                <h3 className="font-semibold text-gray-900">Authentication</h3>
                <p className="text-sm text-gray-600">Signup, signin, magic links, email verification, password reset</p>
              </div>
              <div className="bg-green-50 rounded-lg p-4 border border-green-200">
                <Database className="w-8 h-8 text-green-600 mb-2" />
                <h3 className="font-semibold text-gray-900">REST API</h3>
                <p className="text-sm text-gray-600">CRUD operations on your database tables</p>
              </div>
              <div className="bg-purple-50 rounded-lg p-4 border border-purple-200">
                <Shield className="w-8 h-8 text-purple-600 mb-2" />
                <h3 className="font-semibold text-gray-900">Security</h3>
                <p className="text-sm text-gray-600">JWT tokens, API keys, role-based access</p>
              </div>
            </div>
          </div>
        )

      case 'quickstart':
        return (
          <div className="space-y-6">
            <h2 className="text-2xl font-bold text-gray-900">Quick Start</h2>
            
            <div className="space-y-4">
              <div className="flex items-start gap-4">
                <div className="w-8 h-8 bg-blue-600 text-white rounded-full flex items-center justify-center font-bold">1</div>
                <div>
                  <h3 className="font-semibold text-gray-900">Get your API keys</h3>
                  <p className="text-gray-600">Go to Settings → API Keys in the dashboard</p>
                </div>
              </div>
              
              <div className="flex items-start gap-4">
                <div className="w-8 h-8 bg-blue-600 text-white rounded-full flex items-center justify-center font-bold">2</div>
                <div>
                  <h3 className="font-semibold text-gray-900">Create a user</h3>
                  <CodeBlock code={`fetch('/api/v1/auth/signup', {
  method: 'POST',
  headers: { 
    'Content-Type': 'application/json',
    'apikey': 'YOUR_ANON_KEY'
  },
  body: JSON.stringify({
    email: 'user@example.com',
    password: 'securepassword'
  })
})`} />
                </div>
              </div>
              
              <div className="flex items-start gap-4">
                <div className="w-8 h-8 bg-blue-600 text-white rounded-full flex items-center justify-center font-bold">3</div>
                <div>
                  <h3 className="font-semibold text-gray-900">Access your data</h3>
                  <CodeBlock code={`fetch('/api/v1/rest/your_table', {
  headers: { 
    'apikey': 'YOUR_ANON_KEY',
    'Authorization': 'Bearer ' + accessToken
  }
})`} />
                </div>
              </div>
            </div>
          </div>
        )

      case 'api-keys':
        return (
          <div className="space-y-6">
            <h2 className="text-2xl font-bold text-gray-900">API Keys</h2>
            
            <div className="grid md:grid-cols-2 gap-6">
              <div className="bg-blue-50 rounded-xl p-6 border border-blue-200">
                <h3 className="text-lg font-semibold text-blue-900 mb-3">Anon Key (Public)</h3>
                <ul className="space-y-2 text-sm text-blue-800">
                  <li className="flex items-center gap-2">
                    <Check className="w-4 h-4" /> Safe for client-side code
                  </li>
                  <li className="flex items-center gap-2">
                    <Check className="w-4 h-4" /> Requires JWT for data access
                  </li>
                  <li className="flex items-center gap-2">
                    <Check className="w-4 h-4" /> Use in browsers, mobile apps
                  </li>
                </ul>
              </div>
              
              <div className="bg-purple-50 rounded-xl p-6 border border-purple-200">
                <h3 className="text-lg font-semibold text-purple-900 mb-3">Service Key (Secret)</h3>
                <ul className="space-y-2 text-sm text-purple-800">
                  <li className="flex items-center gap-2">
                    <Shield className="w-4 h-4" /> NEVER expose in client code
                  </li>
                  <li className="flex items-center gap-2">
                    <Check className="w-4 h-4" /> Full access without JWT
                  </li>
                  <li className="flex items-center gap-2">
                    <Check className="w-4 h-4" /> Use in backend, scripts, admin tools
                  </li>
                </ul>
              </div>
            </div>

            <div className="bg-yellow-50 border border-yellow-200 rounded-lg p-4">
              <p className="text-sm text-yellow-800">
                <strong>⚠️ Security:</strong> Never commit your Service Key to version control or expose it in frontend code.
              </p>
            </div>
          </div>
        )

      case 'auth-flow':
        return (
          <div className="space-y-6">
            <h2 className="text-2xl font-bold text-gray-900">Authentication Flow</h2>
            
            <div className="space-y-6">
              <div>
                <h3 className="text-lg font-semibold text-gray-900 mb-3">Sign Up</h3>
                <CodeBlock code={`POST /api/v1/auth/signup
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { 
  "email": "user@example.com", 
  "password": "securepass",
  "full_name": "John Doe"  // optional
}

Response: {
  "token": "eyJhbG...",
  "refresh_token": "abc123...",
  "expires_in": 3600,
  "user": { "id": "uuid", "email": "...", "email_verified": false }
}`} />
              </div>

              <div>
                <h3 className="text-lg font-semibold text-gray-900 mb-3">Sign In</h3>
                <CodeBlock code={`POST /api/v1/auth/signin
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { "email": "user@example.com", "password": "securepass" }

Response: {
  "token": "eyJhbG...",
  "refresh_token": "abc123...",
  "expires_in": 3600,
  "user": { ... }
}`} />
              </div>

              <div>
                <h3 className="text-lg font-semibold text-gray-900 mb-3">Refresh Token</h3>
                <p className="text-gray-600 mb-2">Tokens are rotated on each refresh for security.</p>
                <CodeBlock code={`POST /api/v1/auth/token
Headers: { "apikey": "ANON_KEY", "Content-Type": "application/json" }
Body: { "refresh_token": "abc123..." }

Response: {
  "token": "eyJhbG...",
  "refresh_token": "newtoken...",  // New token! Old one is invalidated
  "expires_in": 3600
}`} />
              </div>
            </div>
          </div>
        )

      case 'email-flows':
        return (
          <div className="space-y-8">
            <h2 className="text-2xl font-bold text-gray-900">Email Flows</h2>
            
            {/* Magic Link */}
            <div className="bg-purple-50 rounded-xl p-6 border border-purple-200">
              <h3 className="text-lg font-semibold text-purple-900 mb-4">🔮 Magic Link (Passwordless)</h3>
              
              <div className="space-y-4">
                <div>
                  <p className="text-sm font-medium text-purple-800 mb-2">Step 1: Request magic link</p>
                  <CodeBlock code={`POST /api/v1/auth/magiclink
Headers: { "apikey": "ANON_KEY" }
Body: { 
  "email": "user@example.com",
  "redirect_url": "https://yourapp.com/auth/callback"
}`} />
                </div>

                <div>
                  <p className="text-sm font-medium text-purple-800 mb-2">Step 2: User clicks email → redirected to your app</p>
                  <code className="block bg-purple-100 p-2 rounded text-sm">
                    https://yourapp.com/auth/callback#access_token=...&refresh_token=...&expires_in=3600
                  </code>
                </div>

                <div>
                  <p className="text-sm font-medium text-purple-800 mb-2">Step 3: Extract tokens in your callback page</p>
                  <CodeBlock code={`// /auth/callback page
const hash = window.location.hash.substring(1);
const params = new URLSearchParams(hash);

const accessToken = params.get('access_token');
const refreshToken = params.get('refresh_token');

if (accessToken) {
  localStorage.setItem('access_token', accessToken);
  localStorage.setItem('refresh_token', refreshToken);
  window.location.href = '/dashboard';
}`} />
                </div>
              </div>
            </div>

            {/* Email Verification */}
            <div className="bg-green-50 rounded-xl p-6 border border-green-200">
              <h3 className="text-lg font-semibold text-green-900 mb-4">✉️ Email Verification</h3>
              
              <div className="space-y-4">
                <div>
                  <p className="text-sm font-medium text-green-800 mb-2">Request verification email</p>
                  <CodeBlock code={`POST /api/v1/auth/resend
Headers: { "apikey": "ANON_KEY" }
Body: { "email": "user@example.com" }`} />
                </div>

                <div>
                  <p className="text-sm font-medium text-green-800 mb-2">User clicks email → redirected</p>
                  <code className="block bg-green-100 p-2 rounded text-sm">
                    https://yourapp.com?verified=true&email=user@example.com
                  </code>
                </div>
              </div>
            </div>

            {/* Password Reset */}
            <div className="bg-orange-50 rounded-xl p-6 border border-orange-200">
              <h3 className="text-lg font-semibold text-orange-900 mb-4">🔑 Password Reset</h3>
              
              <div className="space-y-4">
                <div>
                  <p className="text-sm font-medium text-orange-800 mb-2">Step 1: Request reset email</p>
                  <CodeBlock code={`POST /api/v1/auth/forgot-password
Headers: { "apikey": "ANON_KEY" }
Body: { "email": "user@example.com" }`} />
                </div>

                <div>
                  <p className="text-sm font-medium text-orange-800 mb-2">Step 2: User clicks email → your reset page with token</p>
                  <code className="block bg-orange-100 p-2 rounded text-sm">
                    https://yourapp.com/reset-password?token=abc123...
                  </code>
                </div>

                <div>
                  <p className="text-sm font-medium text-orange-800 mb-2">Step 3: Submit new password</p>
                  <CodeBlock code={`POST /api/v1/auth/reset-password
Headers: { "apikey": "ANON_KEY" }
Body: { 
  "token": "abc123...",
  "new_password": "newsecurepass"
}`} />
                </div>
              </div>
            </div>
          </div>
        )

      case 'rest-api':
        return (
          <div className="space-y-6">
            <h2 className="text-2xl font-bold text-gray-900">REST API</h2>
            
            <div className="bg-gray-50 rounded-lg p-4 border">
              <p className="text-sm text-gray-600">
                Base URL: <code className="bg-gray-200 px-2 py-1 rounded">/api/v1/rest</code>
              </p>
            </div>

            <div className="space-y-4">
              <div>
                <h3 className="font-semibold text-gray-900 mb-2">SELECT (GET)</h3>
                <CodeBlock code={`GET /api/v1/rest/{table}
GET /api/v1/rest/{table}?page=1&page_size=50
GET /api/v1/rest/{table}?order_by=created_at&order_dir=desc
GET /api/v1/rest/{table}?filter=status:eq:active

Headers (Anon Key): { "apikey": "ANON_KEY", "Authorization": "Bearer TOKEN" }
Headers (Service Key): { "apikey": "SERVICE_KEY" }`} />
              </div>

              <div>
                <h3 className="font-semibold text-gray-900 mb-2">INSERT (POST)</h3>
                <CodeBlock code={`POST /api/v1/rest/{table}
Body: { "name": "Product", "price": 99.99 }

Response: { "id": 1, "name": "Product", "price": 99.99, ... }`} />
              </div>

              <div>
                <h3 className="font-semibold text-gray-900 mb-2">UPDATE (PUT)</h3>
                <CodeBlock code={`PUT /api/v1/rest/{table}/{id}
Body: { "price": 149.99 }

Response: { "id": 1, "name": "Product", "price": 149.99, ... }`} />
              </div>

              <div>
                <h3 className="font-semibold text-gray-900 mb-2">DELETE (DELETE)</h3>
                <CodeBlock code={`DELETE /api/v1/rest/{table}/{id}

Response: { "message": "Row deleted successfully" }`} />
              </div>
            </div>
          </div>
        )

      case 'examples':
        return (
          <div className="space-y-6">
            <div className="flex items-center justify-between">
              <h2 className="text-2xl font-bold text-gray-900">Full Documentation</h2>
              <button
                onClick={() => copyToClipboard(fullDocumentation, 'full')}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
              >
                {copiedSection === 'full' ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                Copy for LLM
              </button>
            </div>

            <div className="bg-yellow-50 border border-yellow-200 rounded-lg p-4">
              <p className="text-sm text-yellow-800">
                <strong>💡 Tip:</strong> Click "Copy for LLM" to copy the complete documentation in markdown format. 
                Paste it into ChatGPT, Claude, or any LLM to get help implementing RapiBase in your app.
              </p>
            </div>

            <div className="bg-gray-900 rounded-xl p-6 overflow-auto max-h-[600px]">
              <pre className="text-gray-100 text-sm whitespace-pre-wrap">{fullDocumentation}</pre>
            </div>
          </div>
        )

      default:
        return null
    }
  }

  return (
    <div className="min-h-screen bg-gray-50">
      <div className="max-w-7xl mx-auto px-4 py-8">
        {/* Header */}
        <div className="mb-8">
          <div className="flex items-center gap-3 mb-2">
            <FileText className="w-8 h-8 text-blue-600" />
            <h1 className="text-3xl font-bold text-gray-900">Documentation</h1>
          </div>
          <p className="text-gray-600">Complete guide to integrating RapiBase in your application</p>
        </div>

        <div className="flex gap-8">
          {/* Sidebar */}
          <div className="w-64 flex-shrink-0">
            <nav className="bg-white rounded-xl border border-gray-200 p-4 sticky top-8">
              <ul className="space-y-1">
                {sections.map((section) => (
                  <li key={section.id}>
                    <button
                      onClick={() => setActiveSection(section.id)}
                      className={`w-full flex items-center gap-3 px-3 py-2 rounded-lg text-left transition-colors ${
                        activeSection === section.id
                          ? 'bg-blue-50 text-blue-700'
                          : 'text-gray-600 hover:bg-gray-50'
                      }`}
                    >
                      {section.icon}
                      <span className="font-medium">{section.title}</span>
                      {activeSection === section.id && (
                        <ChevronRight className="w-4 h-4 ml-auto" />
                      )}
                    </button>
                  </li>
                ))}
              </ul>
            </nav>
          </div>

          {/* Content */}
          <div className="flex-1 bg-white rounded-xl border border-gray-200 p-8">
            {renderContent()}
          </div>
        </div>
      </div>
    </div>
  )
}

function CodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false)

  const copy = () => {
    navigator.clipboard.writeText(code)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="relative">
      <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg text-sm overflow-x-auto">
        <code>{code}</code>
      </pre>
      <button
        onClick={copy}
        className="absolute top-2 right-2 p-2 bg-gray-800 hover:bg-gray-700 rounded text-gray-400 hover:text-white transition-colors"
      >
        {copied ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
      </button>
    </div>
  )
}
