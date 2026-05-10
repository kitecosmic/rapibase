# RapiBase

🚀 **Open-source Backend as a Service** - Similar to Supabase but simpler and faster to deploy. Authentication, REST API, and Admin Dashboard in a single binary.

![RapiBase](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/license-AGPLv3-blue)

## Features

### Core
- ✅ **Visual Table Management** - Create, edit, and delete tables through a modern UI
- ✅ **SQL Editor** - Execute raw SQL queries with syntax highlighting
- ✅ **Data Import/Export** - Import from SQL or JSON, export to both formats
- ✅ **REST API** - Auto-generated CRUD endpoints for all your tables
- ✅ **File Storage** - S3-compatible storage with MinIO (buckets, upload, download, image preview)
- ✅ **MCP for AI Agents** - Built-in [Model Context Protocol](https://modelcontextprotocol.io) endpoint at `/mcp` so Claude (and other agents) can discover tables, run CRUD, raw SQL, DDL and storage ops with one URL + one API key
- ✅ **Docker Ready** - Single command deployment with docker-compose

### Authentication
- ✅ **User Auth for Your Apps** - Signup, signin, signout for third-party app users
- ✅ **Magic Links** - Passwordless authentication via email
- ✅ **Email Verification** - Verify user emails with one-click links
- ✅ **Password Reset** - Forgot password flow with email
- ✅ **JWT Tokens** - Configurable expiration times
- ✅ **Rotating Refresh Tokens** - Single-use refresh tokens for security

### Security
- ✅ **API Keys** - Anon Key (public) and Service Key (admin)
- ✅ **Rate Limiting** - Protect auth endpoints from abuse
- ✅ **SMTP Integration** - Send emails via any SMTP provider

### Storage
- ✅ **S3-Compatible** - Powered by MinIO, compatible with AWS S3 SDKs
- ✅ **Bucket Management** - Create public/private buckets via UI
- ✅ **File Operations** - Upload, download, delete, move, copy files
- ✅ **Image Preview** - View images directly in the dashboard
- ✅ **Presigned URLs** - Generate temporary access URLs for private files

## Quick Start

### Using Docker Compose (Recommended)

First, make sure you have Docker installed. If not, follow the [official installation guide](https://docs.docker.com/get-docker/) or run:
```bash
curl -fsSL https://get.docker.com | sh
```

```bash
# Clone the repository
git clone https://github.com/kitecosmic/rapibase.git
cd rapibase

# Copy environment file
cp .env.example .env

# Edit the configuration
# IMPORTANT: You must edit this file to set your own passwords and secrets! (smtp)
nano .env

# Start with docker compose
docker compose up -d

# Access at http://localhost:8080 (or http://YOUR_SERVER_IP:8080)
# Default credentials: admin@rapibase.local / admin123
```

### Enable HTTPS (Recommended)

We recommend using **Caddy** because it handles automatic SSL certificates (Let's Encrypt) and renewals for you.

Run these commands one by one on your VPS:

```bash
# 1. Install required packages
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https

# 2. Add Caddy GPG key
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg

# 3. Add Caddy repository
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list

# 4. Install Caddy
sudo apt update
sudo apt install caddy
```

### Configure Domain

1. Open the Caddy configuration file:
```bash
sudo nano /etc/caddy/Caddyfile
```

2. **Delete everything** in that file and paste this (replace `yourdomain.com` with your real domain):

```caddyfile
yourdomain.com {
    reverse_proxy localhost:8080
}
```

3. Save and exit (Press `Ctrl+X`, then `Y`, then `Enter`).

4. Reload Caddy to apply changes:
```bash
sudo systemctl reload caddy
```

Done! Access your site at `https://yourdomain.com`. Caddy will automatically keep your SSL certificate valid forever.

### Configure Storage Domain (MinIO)

If you're using the Storage feature, you'll want a separate subdomain for MinIO with SSL:

1. Open the Caddy configuration file:
```bash
sudo nano /etc/caddy/Caddyfile
```

2. Add the MinIO configuration (replace with your domains):

```caddyfile
yourdomain.com {
    reverse_proxy localhost:8080
}

storage.yourdomain.com {
    reverse_proxy localhost:9000
}

minio-console.yourdomain.com {
    reverse_proxy localhost:9001
}
```

3. Save and reload Caddy:
```bash
sudo systemctl reload caddy
```

4. Update your `.env` file:
```env
STORAGE_PUBLIC_URL=https://storage.yourdomain.com
STORAGE_USE_SSL=false  # Keep false, Caddy handles SSL

# MinIO public URLs (required for MinIO console to generate correct links)
MINIO_SERVER_URL=https://storage.yourdomain.com
MINIO_BROWSER_REDIRECT_URL=https://minio-console.yourdomain.com
```

5. Restart RapiBase:
```bash
cd ~/rapibase

docker compose down && docker compose up -d
```

Now your files will be accessible via `https://storage.yourdomain.com`.

> **Note:** `STORAGE_USE_SSL=false` means the connection between RapiBase and MinIO is unencrypted (they're on the same server). Caddy handles the public SSL.

### Updating to Latest Version

```bash
cd ~/rapibase
git pull
docker compose up -d --build
```

That's it! Docker will rebuild the image with the new code and restart the container.

### Manual Installation

```bash
# Prerequisites: Go 1.23+, Node.js 20+, PostgreSQL

# Clone
git clone https://github.com/kitecosmic/rapibase.git
cd rapibase

# Build frontend
cd web && npm install && npm run build && cd ..

# Copy and configure environment
cp .env.example .env
# Edit .env with your settings

# Run
go run ./cmd/rapibase
```

## Configuration

All configuration is done through environment variables. Copy `.env.example` to `.env`:

```env
# Database
DATABASE_URL=postgres://rapibase:rapibase@localhost:5432/rapibase?sslmode=disable

# Server
PORT=8080
APP_URL=http://localhost:8080
CORS_ORIGINS=*

# Auth (Admin Dashboard)
JWT_SECRET=change-this-secret-in-production
ADMIN_EMAIL=admin@rapibase.local
ADMIN_PASSWORD=admin123

# Token Expiration
JWT_EXPIRY=1h           # Access token duration (default: 1 hour)
REFRESH_EXPIRY=7d       # Refresh token duration (default: 7 days)

# API Keys
ANON_KEY=your-anon-key              # Public key for client-side apps
SERVICE_KEY=your-service-key        # Secret key for server-side/admin

# SMTP (optional - for email features)
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=your-email@example.com
SMTP_PASS=your-password
SMTP_FROM=noreply@example.com
SMTP_FROM_NAME=Your App Name

# Auth Redirect URL (for third-party apps)
AUTH_REDIRECT_URL=https://yourapp.com
```

## API Keys

RapiBase uses two types of API keys:

| Key | Use Case | JWT Required |
|-----|----------|--------------|
| **Anon Key** | Client-side apps (browsers, mobile) | ✅ Yes |
| **Service Key** | Server-side, admin scripts, dashboards | ❌ No |

```javascript
// Client-side: Anon Key + JWT
fetch('/api/v1/rest/products', {
  headers: { 
    'apikey': 'ANON_KEY',
    'Authorization': 'Bearer ' + accessToken
  }
})

// Server-side: Service Key only
fetch('/api/v1/rest/products', {
  headers: { 'apikey': 'SERVICE_KEY' }
})
```

## API Endpoints

### Admin Authentication (Dashboard)
```
POST /api/dashboard/auth/login           - Admin login
POST /api/dashboard/auth/forgot-password - Request password reset
POST /api/dashboard/auth/reset-password  - Reset password
POST /api/dashboard/auth/refresh         - Refresh token
GET  /api/dashboard/auth/me              - Get current admin
```

### User Authentication (For Your Apps)
All endpoints require `apikey` header.

```
POST /api/v1/auth/signup         - Create new user
POST /api/v1/auth/signin         - Sign in user
POST /api/v1/auth/token          - Refresh token (rotating)
POST /api/v1/auth/signout        - Sign out user

POST /api/v1/auth/magiclink      - Send magic link email
GET  /api/v1/auth/magic          - Verify magic link (from email)

POST /api/v1/auth/resend         - Resend verification email
GET  /api/v1/auth/verify         - Verify email (from email)

POST /api/v1/auth/forgot-password - Send password reset email
POST /api/v1/auth/reset-password  - Reset password with token
```

### REST API (For Your Apps)
Requires `apikey` header. Anon Key also requires `Authorization: Bearer <token>`.

```
GET    /api/v1/rest/:table           - Get rows (paginated)
POST   /api/v1/rest/:table           - Insert row
PUT    /api/v1/rest/:table/:id       - Update row
DELETE /api/v1/rest/:table/:id       - Delete row
```

Query parameters:
- `page` - Page number (default: 1)
- `page_size` - Rows per page (default: 50, max: 1000)
- `order_by` - Column to sort by
- `order` - Sort direction (asc/desc)
- `select` - Comma-separated columns to return
- `column.operator=value` - Filter rows (operators: eq, neq, gt, gte, lt, lte, like, ilike, is, in)

### Tables (Admin)
```
GET    /api/dashboard/tables            - List all tables
POST   /api/dashboard/tables            - Create table
GET    /api/dashboard/tables/:name      - Get table schema
DELETE /api/dashboard/tables/:name      - Drop table
```

### SQL & Import/Export (Admin)
```
POST /api/dashboard/query               - Execute SQL query
POST /api/dashboard/import/sql          - Import SQL file
POST /api/dashboard/import/json/:table  - Import JSON to table
GET  /api/dashboard/export/:table       - Export table (format=json|sql)
```

### Storage API (For Your Apps)
Requires `apikey` header. S3-compatible storage powered by MinIO.

```
POST   /api/v1/storage/:bucket/upload      - Upload file
GET    /api/v1/storage/:bucket/list        - List files
GET    /api/v1/storage/:bucket/*           - Download file
DELETE /api/v1/storage/:bucket/*           - Delete file
GET    /api/v1/storage/:bucket/search      - Search by metadata
GET    /api/v1/storage/:bucket/metadata/*  - Get file metadata
PATCH  /api/v1/storage/:bucket/metadata/*  - Update file metadata
GET    /api/v1/storage/owner/:user_id      - Get files by owner
```

## MCP — Talk to RapiBase from Claude and other agents

RapiBase ships a built-in **Model Context Protocol** server at `POST /mcp` (streamable-HTTP transport). The endpoint lives inside the same Go binary, so there's nothing extra to deploy: `docker compose up` and the MCP is online with the same `.env` and the same `SERVICE_KEY` you already have.

### What the agent gets

| Tool | Description |
|---|---|
| `list_tables` | Every user table with columns, types, primary key and row count. |
| `describe_table` | Full schema of a single table. |
| `query_rows` | Paginated SELECT with `eq, neq, gt, gte, lt, lte, like, ilike, is, in` filters. |
| `insert_row` / `update_row` / `delete_row` | CRUD on any table. Fires the same webhooks as the REST API. |
| `execute_sql` | Parameterised raw SQL. Internal `_rapibase_*` tables are blocked. |
| `create_table` / `drop_table` | Schema changes when the user explicitly authorises them. |
| `list_buckets` / `list_objects` / `download_object` / `upload_object` / `delete_object` | S3-compatible storage. Files exchanged as base64. |

| Resource | Description |
|---|---|
| `rapibase://tables` | Live list of tables (read for free, no tool budget). |
| `rapibase://tables/{name}` | Schema for a single table. |

### Connect from Claude Desktop

Edit `claude_desktop_config.json` (`%APPDATA%\Claude\` on Windows, `~/Library/Application Support/Claude/` on macOS):

```json
{
  "mcpServers": {
    "rapibase": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "apikey": "YOUR_SERVICE_KEY"
      }
    }
  }
}
```

Replace the URL with your public domain in production (`https://yourdomain.com/mcp`). Restart Claude Desktop and the `rapibase` server will appear with all tools and resources visible.

### Connect from the Anthropic SDK

```python
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
    messages=[
        {"role": "user", "content": "List the tables and tell me which has the most rows."}
    ],
)
```

### Quick test with curl

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H "apikey: $SERVICE_KEY" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | jq '.result.tools[].name'
```

You should see all 14 tool names listed.

### Security

- The endpoint requires the **SERVICE_KEY** (`apikey` header). Anon Key + JWT is **not** accepted on `/mcp` — agents get full access or none.
- Internal RapiBase tables (`_rapibase_*`) are blocked even from raw SQL.
- For production, expose `/mcp` only over HTTPS (Caddy handles it automatically as documented above).
- Webhooks fire on MCP-driven inserts/updates/deletes, identical to REST API behaviour.

## Authentication Flows

### Magic Link (Passwordless)

```javascript
// 1. Request magic link
await fetch('/api/v1/auth/magiclink', {
  method: 'POST',
  headers: { 'apikey': ANON_KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify({ 
    email: 'user@example.com',
    redirect_url: 'https://yourapp.com/auth/callback'
  })
})

// 2. User clicks email link → redirected to your app
// https://yourapp.com/auth/callback#access_token=...&refresh_token=...

// 3. Extract tokens in your callback page
const hash = window.location.hash.substring(1)
const params = new URLSearchParams(hash)
const accessToken = params.get('access_token')
const refreshToken = params.get('refresh_token')

// 4. Store tokens and redirect to dashboard
localStorage.setItem('access_token', accessToken)
localStorage.setItem('refresh_token', refreshToken)
window.location.href = '/dashboard'
```

### Email Verification

```javascript
// Request verification email
await fetch('/api/v1/auth/resend', {
  method: 'POST',
  headers: { 'apikey': ANON_KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify({ email: 'user@example.com' })
})

// User clicks email → redirected to:
// https://yourapp.com?verified=true&email=user@example.com
```

### Password Reset

```javascript
// 1. Request reset email
await fetch('/api/v1/auth/forgot-password', {
  method: 'POST',
  headers: { 'apikey': ANON_KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify({ email: 'user@example.com' })
})

// 2. User clicks email → your reset page with token
// https://yourapp.com/reset-password?token=abc123...

// 3. Submit new password
await fetch('/api/v1/auth/reset-password', {
  method: 'POST',
  headers: { 'apikey': ANON_KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify({ 
    token: 'abc123...',
    new_password: 'newsecurepassword'
  })
})
```

## Architecture

```
rapibase/
├── cmd/rapibase/          # Application entry point
├── internal/
│   ├── api/               # HTTP handlers and routes
│   │   ├── handlers/      # Request handlers
│   │   └── middleware/    # Auth, API keys, rate limiting
│   ├── auth/              # JWT and SMTP
│   ├── config/            # Configuration
│   ├── database/          # PostgreSQL operations
│   └── models/            # Data models
├── web/                   # React frontend (Admin Dashboard)
│   └── src/pages/
│       ├── Auth.tsx       # User management
│       ├── Docs.tsx       # API documentation
│       ├── Tables.tsx     # Table management
│       └── ...
├── Dockerfile
└── docker-compose.yml
```

## Security

- Passwords hashed with bcrypt (cost 12)
- Configurable JWT token expiration
- Rotating refresh tokens (single-use)
- API key authentication (Anon + Service keys)
- Rate limiting on auth endpoints
- SQL injection prevention with prepared statements
- Internal tables hidden from user access

## Development

```bash
# Backend (with hot reload)
go install github.com/cosmtrek/air@latest
air

# Frontend
cd web && npm run dev
```

## License

AGPLv3 License.

RapiBase is open source software. You are free to use it for personal and commercial projects.
However, if you modify the code and offer it as a service to others (SaaS), you must make your modifications open source under the same license.

## Contributing

Contributions are welcome! Please open an issue or PR.

---

Made with ❤️ by [kitecosmic](https://github.com/kitecosmic)
