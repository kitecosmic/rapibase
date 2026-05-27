import { useState } from 'react'
import {
  Book,
  Bot,
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
  ChevronRight,
  HardDrive,
  FunctionSquare
} from 'lucide-react'

type DocSection = 'overview' | 'quickstart' | 'auth-flow' | 'api-keys' | 'rest-api' | 'rpc' | 'storage-api' | 'email-flows' | 'mcp' | 'examples'

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
    { id: 'rpc', title: 'RPC (Functions)', icon: <FunctionSquare className="w-4 h-4" /> },
    { id: 'storage-api', title: 'Storage API', icon: <HardDrive className="w-4 h-4" /> },
    { id: 'mcp', title: 'MCP for AI Agents', icon: <Bot className="w-4 h-4" /> },
    { id: 'examples', title: 'Full Examples', icon: <Code className="w-4 h-4" /> },
  ]

  const fullDocumentation = `# RapiBase Documentation

## Overview

RapiBase is a backend-as-a-service that provides:
- **Authentication**: User signup, signin, magic links, email verification, password reset
- **REST API**: CRUD operations on your database tables
- **Storage**: S3-compatible file storage with MinIO
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

**Views and materialized views** also work on \`GET /api/v1/rest/{name}\` — Postgres treats them as tables. Use materialized views for pre-aggregated reports over millions of rows.

## RPC — Call Postgres Functions

Base URL: \`/api/v1/rpc\`

When the generic REST CRUD is not enough (aggregations, joins, window functions, parametrised reports), define a Postgres function and call it by name.

### Step 1 — Define the function

\`\`\`sql
CREATE OR REPLACE FUNCTION top_products(p_limit int)
RETURNS TABLE(id int, name text, total_sold int)
LANGUAGE sql AS $$
    SELECT p.id, p.name, SUM(oi.quantity)::int
    FROM products p
    JOIN order_items oi ON oi.product_id = p.id
    GROUP BY p.id, p.name
    ORDER BY 3 DESC
    LIMIT p_limit;
$$;
\`\`\`

### Step 2 — Invoke it

JSON body keys become named arguments: \`fn(key1 := $1, key2 := $2)\`.

\`\`\`
POST /api/v1/rpc/top_products
Headers: { "apikey": "SERVICE_KEY", "Content-Type": "application/json" }
Body: { "p_limit": 10 }

Response: [
  { "id": 42, "name": "Wireless Mouse", "total_sold": 1280 },
  { "id": 17, "name": "USB-C Cable",    "total_sold": 945  }
]
\`\`\`

### No-argument functions

Send an empty body or \`{}\` to call a function with no arguments.

### JavaScript example

\`\`\`javascript
async function callRpc(name, args = {}) {
  const res = await fetch(\`\${RAPIBASE_URL}/api/v1/rpc/\${name}\`, {
    method: 'POST',
    headers: {
      'apikey': SERVICE_KEY,
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(args)
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

const rows = await callRpc('top_products', { p_limit: 10 });
\`\`\`

### When to use RPC vs REST

| Use case | Endpoint |
|---|---|
| Listing / filtering / inserting rows | \`/rest/{table}\` |
| Pre-aggregated report that changes rarely | \`/rest/{materialized_view}\` |
| Aggregation with parameters | \`/rpc/{function}\` |
| Multi-step atomic write | \`/rpc/{function}\` |
| Heavy join across several tables | \`/rpc/{function}\` |

### Security

- Functions run with the privileges of the calling Postgres role.
- Argument values are bound as \`$1, $2, …\` placeholders — no SQL injection through arguments.
- Internal \`_rapibase_*\` functions are blocked.
- For multi-tenant production, create a dedicated role and grant \`EXECUTE\` only on the functions you want exposed.

## Storage API

Base URL: \`/api/v1/storage\`

S3-compatible file storage powered by MinIO. Requires API key for all operations.

### Upload Files
\`\`\`
POST /api/v1/storage/{bucket}/upload
Headers: { "apikey": "SERVICE_KEY" }
Content-Type: multipart/form-data

Form Data:
  - file: (binary file)
  - path: "folder/" (optional prefix)

Response: {
  "key": "folder/image.png",
  "size": 102400,
  "content_type": "image/png",
  "url": "/bucket-name/folder/image.png",
  "public_url": "http://localhost:9000/bucket-name/folder/image.png"
}
\`\`\`

### List Files
\`\`\`
GET /api/v1/storage/{bucket}/list
GET /api/v1/storage/{bucket}/list?prefix=folder/
Headers: { "apikey": "SERVICE_KEY" }

Response: {
  "objects": [
    { "key": "image.png", "size": 102400, "is_dir": false },
    { "key": "subfolder/", "size": 0, "is_dir": true }
  ]
}
\`\`\`

### Download Files
\`\`\`
GET /api/v1/storage/{bucket}/{path/to/file}
Headers: { "apikey": "SERVICE_KEY" }

Response: Binary file content
\`\`\`

### Delete Files
\`\`\`
DELETE /api/v1/storage/{bucket}/{path/to/file}
Headers: { "apikey": "SERVICE_KEY" }

Response: { "message": "Object deleted successfully" }
\`\`\`

### Upload with Owner & Metadata
\`\`\`
POST /api/v1/storage/{bucket}/upload
Headers: { "apikey": "SERVICE_KEY" }
Content-Type: multipart/form-data

Form Data:
  - file: (binary file)
  - path: "products/123/" (optional prefix)
  - owner_id: "user-uuid" (optional - links file to auth_users)
  - metadata: '{"product_id": "123", "type": "thumbnail"}' (optional JSON)

Response: {
  "id": "file-uuid",
  "key": "products/123/image.png",
  "owner": "user-uuid",
  "metadata": { "product_id": "123", "type": "thumbnail" },
  "public_url": "http://localhost:9000/bucket/products/123/image.png"
}
\`\`\`

### Get Files by Owner
\`\`\`
GET /api/v1/storage/owner/{user_id}
Headers: { "apikey": "SERVICE_KEY" }

Response: {
  "objects": [
    { "id": "uuid", "name": "avatar.png", "bucket_id": "users", ... },
    { "id": "uuid", "name": "cover.jpg", "bucket_id": "users", ... }
  ],
  "owner": "user-uuid"
}
\`\`\`

### Search Files by Metadata
\`\`\`
GET /api/v1/storage/{bucket}/search?key=product_id&value=123
Headers: { "apikey": "SERVICE_KEY" }

Response: {
  "objects": [
    { "id": "uuid", "name": "image1.png", "metadata": { "product_id": "123" } },
    { "id": "uuid", "name": "image2.png", "metadata": { "product_id": "123" } }
  ]
}
\`\`\`

### Get/Update File Metadata
\`\`\`
GET /api/v1/storage/{bucket}/metadata/{path/to/file}
Headers: { "apikey": "SERVICE_KEY" }

PATCH /api/v1/storage/{bucket}/metadata/{path/to/file}
Headers: { "apikey": "SERVICE_KEY" }
Body: { "metadata": { "product_id": "456", "featured": true } }
\`\`\`

### JavaScript Storage Example
\`\`\`javascript
// Upload a file
async function uploadFile(bucket, file, path = '') {
  const formData = new FormData();
  formData.append('file', file);
  if (path) formData.append('path', path);

  const response = await fetch(\`\${RAPIBASE_URL}/api/v1/storage/\${bucket}/upload\`, {
    method: 'POST',
    headers: { 'apikey': SERVICE_KEY },
    body: formData
  });
  return response.json();
}

// List files
async function listFiles(bucket, prefix = '') {
  const url = new URL(\`\${RAPIBASE_URL}/api/v1/storage/\${bucket}/list\`);
  if (prefix) url.searchParams.set('prefix', prefix);
  const response = await fetch(url, { headers: { 'apikey': SERVICE_KEY } });
  return response.json();
}

// Delete a file
async function deleteFile(bucket, key) {
  const response = await fetch(\`\${RAPIBASE_URL}/api/v1/storage/\${bucket}/\${key}\`, {
    method: 'DELETE',
    headers: { 'apikey': SERVICE_KEY }
  });
  return response.json();
}

// Usage
const result = await uploadFile('images', myFile, 'avatars/');
console.log('Uploaded:', result.public_url);
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

## MCP for AI Agents

RapiBase ships a built-in **Model Context Protocol** server at \`POST /mcp\` (Streamable HTTP, MCP spec 2025-03-26+). Agents can discover tables and run CRUD/SQL/storage with one URL and the SERVICE_KEY.

### Authentication

Send the SERVICE_KEY in **either** header (both work):

| Header | Example |
|---|---|
| Authorization: Bearer | \`Authorization: Bearer YOUR_SERVICE_KEY\` |
| apikey | \`apikey: YOUR_SERVICE_KEY\` |

ANON_KEY + JWT is **not** accepted on \`/mcp\`. Agents get full access (SERVICE_KEY) or none.

### Tools exposed

| Tool | Description |
|---|---|
| list_tables | Tables with columns, types, primary key, row count |
| describe_table | Full schema of a single table |
| query_rows | Paginated SELECT with eq/neq/gt/gte/lt/lte/like/ilike/is/in filters |
| insert_row, update_row, delete_row | CRUD on any table (fires webhooks) |
| execute_sql | Parameterised raw SQL ($1, $2, ...) |
| create_table, drop_table | Schema changes |
| list_buckets, list_objects, download_object, upload_object, delete_object | S3-compatible storage |

Resources: \`rapibase://tables\` (live list), \`rapibase://tables/{name}\` (single schema).

### Connect from Claude Desktop

\`\`\`json
{
  "mcpServers": {
    "rapibase": {
      "type": "http",
      "url": "https://yourdomain.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_SERVICE_KEY"
      }
    }
  }
}
\`\`\`

### Connect from the Anthropic SDK

\`\`\`python
from anthropic import Anthropic
client = Anthropic()
response = client.messages.create(
    model="claude-opus-4-7",
    max_tokens=2048,
    mcp_servers=[{
        "type": "url",
        "url": "https://yourdomain.com/mcp",
        "name": "rapibase",
        "authorization_token": "YOUR_SERVICE_KEY",
    }],
    messages=[{"role": "user", "content": "List my tables"}],
)
\`\`\`

### Connect from other platforms (n8n, OpenAI, Cursor, Cline, ...)

1. Server URL: \`https://yourdomain.com/mcp\`
2. Bearer token / secret: your \`SERVICE_KEY\`
3. Transport: **Streamable HTTP** (NOT SSE legacy — \`/sse\` + \`/messages\` is not supported).

### Quick test with curl

\`\`\`bash
curl -s -X POST https://yourdomain.com/mcp \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H "Authorization: Bearer $SERVICE_KEY" \\
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
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
            
            <div className="grid md:grid-cols-2 lg:grid-cols-4 gap-4">
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
              <div className="bg-orange-50 rounded-lg p-4 border border-orange-200">
                <Bot className="w-8 h-8 text-orange-600 mb-2" />
                <h3 className="font-semibold text-gray-900">MCP for Agents</h3>
                <p className="text-sm text-gray-600">Built-in endpoint so Claude and other agents can use your tables</p>
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

              <div className="bg-blue-50 border border-blue-200 rounded-lg p-4">
                <p className="text-sm text-blue-900">
                  <strong>💡 Views and materialized views</strong> also work on <code>GET /api/v1/rest/&#123;name&#125;</code> — Postgres treats them as tables. Use materialized views for pre-aggregated reports over millions of rows so the per-request <code>COUNT(*)</code> becomes free.
                </p>
              </div>
            </div>
          </div>
        )

      case 'rpc':
        return (
          <div className="space-y-6">
            <div>
              <h2 className="text-2xl font-bold text-gray-900 mb-2 flex items-center gap-2">
                <FunctionSquare className="w-7 h-7 text-indigo-600" /> RPC — Call Postgres Functions
              </h2>
              <p className="text-gray-600">
                The REST API gives you generic CRUD on tables. When you need aggregations, joins, window functions or any logic beyond filter/order/paginate, define a Postgres function once and call it by name at <code className="px-1.5 py-0.5 bg-gray-100 rounded text-sm">POST /api/v1/rpc/&#123;name&#125;</code>.
              </p>
            </div>

            <div className="bg-gray-50 rounded-lg p-4 border">
              <p className="text-sm text-gray-600">
                Base URL: <code className="bg-gray-200 px-2 py-1 rounded">/api/v1/rpc</code>
              </p>
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">Step 1 — Define the function in Postgres</h3>
              <p className="text-gray-600 mb-3">
                From the SQL Editor (or the dashboard query endpoint), create a function. You can use any Postgres feature: aggregations, CTEs, joins, window functions, etc.
              </p>
              <CodeBlock code={`CREATE OR REPLACE FUNCTION top_products(p_limit int)
RETURNS TABLE(
    id          int,
    name        text,
    total_sold  int
) LANGUAGE sql AS $$
    SELECT p.id,
           p.name,
           SUM(oi.quantity)::int
    FROM products p
    JOIN order_items oi ON oi.product_id = p.id
    GROUP BY p.id, p.name
    ORDER BY 3 DESC
    LIMIT p_limit;
$$;`} />
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">Step 2 — Invoke it via HTTP</h3>
              <p className="text-gray-600 mb-3">
                The JSON body keys become <strong>named arguments</strong> (<code>fn(key1 := $1, key2 := $2)</code>). Order does not matter, and functions with default values can be called with a subset.
              </p>
              <CodeBlock code={`POST /api/v1/rpc/top_products
Headers: { "apikey": "SERVICE_KEY", "Content-Type": "application/json" }
Body: { "p_limit": 10 }

Response: [
  { "id": 42, "name": "Wireless Mouse", "total_sold": 1280 },
  { "id": 17, "name": "USB-C Cable",    "total_sold": 945  },
  ...
]`} />
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">No-argument functions</h3>
              <p className="text-gray-600 mb-3">
                Send an empty body or <code>&#123;&#125;</code> to call a function with no arguments.
              </p>
              <CodeBlock code={`POST /api/v1/rpc/dashboard_summary
Headers: { "apikey": "SERVICE_KEY" }
Body: {}

Response: [
  { "users_total": 1042, "orders_today": 87, "revenue_month": 124300 }
]`} />
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">JavaScript example</h3>
              <CodeBlock code={`async function callRpc(name, args = {}) {
  const res = await fetch(\`\${RAPIBASE_URL}/api/v1/rpc/\${name}\`, {
    method: 'POST',
    headers: {
      'apikey': SERVICE_KEY,
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(args)
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

// Usage
const rows = await callRpc('top_products', { p_limit: 10 });
console.table(rows);`} />
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">When to use RPC vs REST</h3>
              <div className="overflow-x-auto">
                <table className="w-full border border-gray-200 rounded-lg text-sm">
                  <thead className="bg-gray-50">
                    <tr className="text-left">
                      <th className="px-4 py-2 font-semibold text-gray-700">Use case</th>
                      <th className="px-4 py-2 font-semibold text-gray-700">Recommended endpoint</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-200">
                    <tr><td className="px-4 py-2">Listing / filtering / inserting rows of a table</td><td className="px-4 py-2 font-mono text-xs">/rest/&#123;table&#125;</td></tr>
                    <tr><td className="px-4 py-2">Pre-aggregated report that changes rarely</td><td className="px-4 py-2 font-mono text-xs">/rest/&#123;materialized_view&#125;</td></tr>
                    <tr><td className="px-4 py-2">Aggregation with parameters (date range, top-N, etc.)</td><td className="px-4 py-2 font-mono text-xs">/rpc/&#123;function&#125;</td></tr>
                    <tr><td className="px-4 py-2">Multi-step write that must be atomic</td><td className="px-4 py-2 font-mono text-xs">/rpc/&#123;function&#125;</td></tr>
                    <tr><td className="px-4 py-2">Heavy join across several tables</td><td className="px-4 py-2 font-mono text-xs">/rpc/&#123;function&#125;</td></tr>
                  </tbody>
                </table>
              </div>
            </div>

            <div className="bg-yellow-50 border border-yellow-200 rounded-lg p-4">
              <p className="text-sm text-yellow-900">
                <strong>🔒 Security:</strong> Functions run with the privileges of the calling Postgres role — the same way regular SQL does. Internal <code>_rapibase_*</code> functions are blocked. Argument values are bound as <code>$1, $2, …</code> placeholders, so SQL injection through arguments is not possible. For production multi-tenant setups, create a dedicated role and grant <code>EXECUTE</code> only on the functions you want exposed.
              </p>
            </div>
          </div>
        )

      case 'storage-api':
        return (
          <div className="space-y-8">
            <h2 className="text-2xl font-bold text-gray-900">Storage API</h2>
            
            <div className="bg-gray-50 rounded-lg p-4 border">
              <p className="text-sm text-gray-600">
                Base URL: <code className="bg-gray-200 px-2 py-1 rounded">/api/v1/storage</code>
              </p>
              <p className="text-sm text-gray-500 mt-1">
                S3-compatible file storage powered by MinIO. Requires API key for all operations.
              </p>
            </div>

            {/* Upload Files */}
            <div className="bg-blue-50 rounded-xl p-6 border border-blue-200">
              <h3 className="text-lg font-semibold text-blue-900 mb-4">📤 Upload Files</h3>
              <CodeBlock code={`POST /api/v1/storage/{bucket}/upload
Headers: { "apikey": "SERVICE_KEY" }
Content-Type: multipart/form-data

Form Data:
  - file: (binary file)
  - path: "products/123/" (optional prefix)
  - owner_id: "user-uuid" (optional - links to auth_users)
  - metadata: '{"product_id":"123"}' (optional JSON)

Response: {
  "id": "file-uuid",
  "key": "products/123/image.png",
  "owner": "user-uuid",
  "metadata": { "product_id": "123" },
  "public_url": "http://localhost:9000/bucket/..."
}`} />
            </div>

            {/* List Files */}
            <div className="bg-green-50 rounded-xl p-6 border border-green-200">
              <h3 className="text-lg font-semibold text-green-900 mb-4">📂 List Files</h3>
              <CodeBlock code={`GET /api/v1/storage/{bucket}/list
GET /api/v1/storage/{bucket}/list?prefix=folder/
Headers: { "apikey": "SERVICE_KEY" }

Response: {
  "objects": [
    {
      "key": "folder/image.png",
      "size": 102400,
      "last_modified": "2024-01-15T10:30:00Z",
      "content_type": "image/png",
      "is_dir": false
    },
    {
      "key": "folder/subfolder/",
      "size": 0,
      "is_dir": true
    }
  ],
  "prefix": "folder/"
}`} />
            </div>

            {/* Download Files */}
            <div className="bg-purple-50 rounded-xl p-6 border border-purple-200">
              <h3 className="text-lg font-semibold text-purple-900 mb-4">📥 Download / Get Files</h3>
              <CodeBlock code={`GET /api/v1/storage/{bucket}/{path/to/file}
Headers: { "apikey": "SERVICE_KEY" }

Response: Binary file content with appropriate Content-Type header

// For public buckets, files can be accessed directly:
GET http://localhost:9000/{bucket}/{path/to/file}`} />
            </div>

            {/* Delete Files */}
            <div className="bg-red-50 rounded-xl p-6 border border-red-200">
              <h3 className="text-lg font-semibold text-red-900 mb-4">🗑️ Delete Files</h3>
              <CodeBlock code={`DELETE /api/v1/storage/{bucket}/{path/to/file}
Headers: { "apikey": "SERVICE_KEY" }

Response: { "message": "Object deleted successfully" }`} />
            </div>

            {/* Get Files by Owner */}
            <div className="bg-indigo-50 rounded-xl p-6 border border-indigo-200">
              <h3 className="text-lg font-semibold text-indigo-900 mb-4">👤 Get Files by Owner</h3>
              <p className="text-sm text-indigo-700 mb-3">Get all files uploaded by a specific user (useful for user profiles, galleries, etc.)</p>
              <CodeBlock code={`GET /api/v1/storage/owner/{user_id}
Headers: { "apikey": "SERVICE_KEY" }

Response: {
  "objects": [
    { "id": "uuid", "name": "avatar.png", "bucket_id": "users" },
    { "id": "uuid", "name": "cover.jpg", "bucket_id": "users" }
  ],
  "owner": "user-uuid"
}`} />
            </div>

            {/* Search by Metadata */}
            <div className="bg-orange-50 rounded-xl p-6 border border-orange-200">
              <h3 className="text-lg font-semibold text-orange-900 mb-4">🔍 Search by Metadata</h3>
              <p className="text-sm text-orange-700 mb-3">Find files by custom metadata (e.g., all images for a product)</p>
              <CodeBlock code={`GET /api/v1/storage/{bucket}/search?key=product_id&value=123
Headers: { "apikey": "SERVICE_KEY" }

Response: {
  "objects": [
    { "id": "uuid", "name": "image1.png", "metadata": { "product_id": "123" } },
    { "id": "uuid", "name": "image2.png", "metadata": { "product_id": "123" } }
  ]
}`} />
            </div>

            {/* Update Metadata */}
            <div className="bg-teal-50 rounded-xl p-6 border border-teal-200">
              <h3 className="text-lg font-semibold text-teal-900 mb-4">✏️ Get/Update Metadata</h3>
              <CodeBlock code={`// Get file metadata
GET /api/v1/storage/{bucket}/metadata/{path/to/file}

// Update file metadata
PATCH /api/v1/storage/{bucket}/metadata/{path/to/file}
Headers: { "apikey": "SERVICE_KEY" }
Body: { "metadata": { "product_id": "456", "featured": true } }`} />
            </div>

            {/* JavaScript Example */}
            <div className="bg-gray-900 rounded-xl p-6">
              <h3 className="text-lg font-semibold text-white mb-4">💻 JavaScript Example</h3>
              <CodeBlock code={`// Upload a file
async function uploadFile(bucket, file, path = '') {
  const formData = new FormData();
  formData.append('file', file);
  if (path) formData.append('path', path);

  const response = await fetch(
    \`\${RAPIBASE_URL}/api/v1/storage/\${bucket}/upload\`,
    {
      method: 'POST',
      headers: { 'apikey': SERVICE_KEY },
      body: formData
    }
  );
  return response.json();
}

// List files in a bucket
async function listFiles(bucket, prefix = '') {
  const url = new URL(\`\${RAPIBASE_URL}/api/v1/storage/\${bucket}/list\`);
  if (prefix) url.searchParams.set('prefix', prefix);

  const response = await fetch(url, {
    headers: { 'apikey': SERVICE_KEY }
  });
  return response.json();
}

// Delete a file
async function deleteFile(bucket, key) {
  const response = await fetch(
    \`\${RAPIBASE_URL}/api/v1/storage/\${bucket}/\${key}\`,
    {
      method: 'DELETE',
      headers: { 'apikey': SERVICE_KEY }
    }
  );
  return response.json();
}

// Usage
const result = await uploadFile('images', myFile, 'avatars/');
console.log('Uploaded:', result.public_url);

const files = await listFiles('images', 'avatars/');
console.log('Files:', files.objects);`} />
            </div>

            {/* Bucket Policies */}
            <div className="bg-yellow-50 border border-yellow-200 rounded-lg p-4">
              <p className="text-sm text-yellow-800">
                <strong>💡 Public vs Private Buckets:</strong> Public buckets allow direct URL access to files. 
                Private buckets require API key authentication. You can toggle bucket visibility in the Storage dashboard.
              </p>
            </div>
          </div>
        )

      case 'mcp':
        return (
          <div className="space-y-6">
            <div>
              <h2 className="text-2xl font-bold text-gray-900 mb-2 flex items-center gap-2">
                <Bot className="w-7 h-7 text-orange-600" /> MCP for AI Agents
              </h2>
              <p className="text-gray-600">
                RapiBase ships a built-in <strong>Model Context Protocol</strong> server at <code className="px-1.5 py-0.5 bg-gray-100 rounded text-sm">POST /mcp</code>. Connect Claude, ChatGPT or any MCP-compatible agent and it will be able to discover your tables, run CRUD, raw SQL and storage operations — with one URL and one API key.
              </p>
            </div>

            <div className="bg-blue-50 border border-blue-200 rounded-lg p-4">
              <p className="text-sm text-blue-900">
                <strong>Transport:</strong> Streamable HTTP (MCP spec 2025-03-26+). The deprecated SSE-legacy transport (separate <code>/sse</code> + <code>/messages</code> endpoints) is <strong>not</strong> supported. If your client offers a choice, pick "Streamable HTTP" or "MCP HTTP".
              </p>
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">Authentication</h3>
              <p className="text-gray-600 mb-3">
                The endpoint requires your <strong>SERVICE_KEY</strong>. Send it in <em>either</em> header — both work:
              </p>
              <div className="overflow-x-auto">
                <table className="w-full border border-gray-200 rounded-lg text-sm">
                  <thead className="bg-gray-50">
                    <tr className="text-left">
                      <th className="px-4 py-2 font-semibold text-gray-700">Header</th>
                      <th className="px-4 py-2 font-semibold text-gray-700">Example</th>
                      <th className="px-4 py-2 font-semibold text-gray-700">When to use</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-200">
                    <tr>
                      <td className="px-4 py-2 font-mono text-xs">Authorization: Bearer</td>
                      <td className="px-4 py-2 font-mono text-xs">Authorization: Bearer YOUR_SERVICE_KEY</td>
                      <td className="px-4 py-2">Default for MCP platforms (Claude Desktop, OpenAI Agents, n8n, Cursor, Cline). Most UIs ask for a "bearer token" or "secret" — paste your <code>SERVICE_KEY</code>.</td>
                    </tr>
                    <tr>
                      <td className="px-4 py-2 font-mono text-xs">apikey</td>
                      <td className="px-4 py-2 font-mono text-xs">apikey: YOUR_SERVICE_KEY</td>
                      <td className="px-4 py-2">Same convention as the rest of the RapiBase REST API. Handy from curl or custom code.</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p className="text-sm text-gray-500 mt-2">
                ANON_KEY + JWT is <strong>not</strong> accepted on <code>/mcp</code> — agents get full access (SERVICE_KEY) or none.
              </p>
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">What the agent gets</h3>
              <div className="overflow-x-auto">
                <table className="w-full border border-gray-200 rounded-lg text-sm">
                  <thead className="bg-gray-50">
                    <tr className="text-left">
                      <th className="px-4 py-2 font-semibold text-gray-700">Tool</th>
                      <th className="px-4 py-2 font-semibold text-gray-700">Description</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-200">
                    <tr><td className="px-4 py-2 font-mono text-xs">list_tables</td><td className="px-4 py-2">Every user table with columns, types, primary key and row count.</td></tr>
                    <tr><td className="px-4 py-2 font-mono text-xs">describe_table</td><td className="px-4 py-2">Full schema of a single table.</td></tr>
                    <tr><td className="px-4 py-2 font-mono text-xs">query_rows</td><td className="px-4 py-2">Paginated SELECT with eq, neq, gt, gte, lt, lte, like, ilike, is, in filters.</td></tr>
                    <tr><td className="px-4 py-2 font-mono text-xs">insert_row · update_row · delete_row</td><td className="px-4 py-2">CRUD on any table. Fires the same webhooks as the REST API.</td></tr>
                    <tr><td className="px-4 py-2 font-mono text-xs">execute_sql</td><td className="px-4 py-2">Parameterised raw SQL. Internal <code>_rapibase_*</code> tables are blocked.</td></tr>
                    <tr><td className="px-4 py-2 font-mono text-xs">create_table · drop_table</td><td className="px-4 py-2">Schema changes when the user explicitly authorises them.</td></tr>
                    <tr><td className="px-4 py-2 font-mono text-xs">list_buckets · list_objects · download_object · upload_object · delete_object</td><td className="px-4 py-2">S3-compatible storage. Files exchanged as base64.</td></tr>
                  </tbody>
                </table>
              </div>
              <p className="text-sm text-gray-500 mt-3">
                Resources: <code className="font-mono">rapibase://tables</code> (live list) and <code className="font-mono">rapibase://tables/&#123;name&#125;</code> (single schema). Read for free, no tool budget.
              </p>
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">Connect from Claude Desktop</h3>
              <p className="text-gray-600 mb-3">
                Edit <code className="font-mono text-sm">claude_desktop_config.json</code> (<code>%APPDATA%\Claude\</code> on Windows, <code>~/Library/Application Support/Claude/</code> on macOS):
              </p>
              <CodeBlock code={`{
  "mcpServers": {
    "rapibase": {
      "type": "http",
      "url": "https://yourdomain.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_SERVICE_KEY"
      }
    }
  }
}`} />
              <p className="text-sm text-gray-500 mt-2">
                Restart Claude Desktop and the <code>rapibase</code> server appears with all 14 tools and 2 resources.
              </p>
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">Connect from the Anthropic SDK</h3>
              <CodeBlock code={`from anthropic import Anthropic

client = Anthropic()
response = client.messages.create(
    model="claude-opus-4-7",
    max_tokens=2048,
    mcp_servers=[{
        "type": "url",
        "url": "https://yourdomain.com/mcp",
        "name": "rapibase",
        "authorization_token": "YOUR_SERVICE_KEY",  # sent as Authorization: Bearer
    }],
    messages=[
        {"role": "user", "content": "List the tables and tell me which has the most rows."}
    ],
)`} />
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">Connect from other platforms</h3>
              <p className="text-gray-600 mb-3">
                For n8n, OpenAI Agents, Cursor, Cline and most other MCP clients, the platform asks for two things:
              </p>
              <ol className="list-decimal pl-6 space-y-1 text-gray-700 text-sm mb-3">
                <li><strong>Server URL</strong> → <code className="font-mono">https://yourdomain.com/mcp</code></li>
                <li><strong>Bearer token</strong> / <strong>Authorization</strong> / <strong>Secret</strong> → your <code className="font-mono">SERVICE_KEY</code></li>
              </ol>
              <p className="text-sm text-gray-600">
                The platform will send <code className="font-mono">Authorization: Bearer &lt;your-service-key&gt;</code> and RapiBase will accept it. If asked for a transport, choose <strong>Streamable HTTP</strong>.
              </p>
            </div>

            <div>
              <h3 className="text-lg font-semibold text-gray-900 mb-3">Quick test with curl</h3>
              <CodeBlock code={`# Authorization: Bearer (recommended)
curl -s -X POST https://yourdomain.com/mcp \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H "Authorization: Bearer $SERVICE_KEY" \\
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \\
| jq '.result.tools[].name'

# Or with the apikey header
curl -s -X POST https://yourdomain.com/mcp \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H "apikey: $SERVICE_KEY" \\
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \\
| jq '.result.tools[].name'`} />
              <p className="text-sm text-gray-500 mt-2">You should see all 14 tool names listed.</p>
            </div>

            <div className="bg-yellow-50 border border-yellow-200 rounded-lg p-4">
              <p className="text-sm text-yellow-900">
                <strong>⚠️ Security:</strong> The MCP gives the agent admin-level access to your data. Expose <code>/mcp</code> only over HTTPS and treat the <code>SERVICE_KEY</code> as a secret. Webhooks fire on agent-driven inserts/updates/deletes, identical to REST API behaviour.
              </p>
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
