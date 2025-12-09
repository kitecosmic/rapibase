# RapiBase

🚀 **Open-source database management platform** - Similar to Supabase/PocketBase but simpler and faster to deploy.

![RapiBase](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/license-MIT-green)

## Features

- ✅ **Visual Table Management** - Create, edit, and delete tables through a modern UI
- ✅ **SQL Editor** - Execute raw SQL queries with syntax highlighting
- ✅ **Data Import/Export** - Import from SQL or JSON, export to both formats
- ✅ **Authentication** - JWT-based auth with login, register, and password reset
- ✅ **SMTP Integration** - Email notifications for password recovery
- ✅ **Docker Ready** - Single command deployment with docker-compose
- ✅ **High Performance** - Built with Go and PostgreSQL connection pooling
- ✅ **Lightweight** - ~20MB Docker image

## Quick Start

### Using Docker Compose (Recommended)

```bash
# Clone the repository
git clone https://github.com/yourusername/rapibase.git
cd rapibase

# Start with docker-compose
docker-compose up -d

# Access at http://localhost:8080
# Default credentials: admin@rapibase.local / admin123
```

### Using Portainer

1. Create a new Stack in Portainer
2. Paste the contents of `docker-compose.yml`
3. Set your environment variables
4. Deploy!

### Manual Installation

```bash
# Prerequisites: Go 1.21+, Node.js 20+, PostgreSQL

# Clone
git clone https://github.com/yourusername/rapibase.git
cd rapibase

# Build frontend
cd web && npm install && npm run build && cd ..

# Build backend
go build -o rapibase ./cmd/rapibase

# Set environment variables (see .env.example)
export DATABASE_URL="postgres://user:pass@localhost:5432/rapibase"
export JWT_SECRET="your-secret-key"

# Run
./rapibase
```

## Configuration

All configuration is done through environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | `postgres://postgres:postgres@localhost:5432/rapibase` |
| `PORT` | Server port | `8080` |
| `APP_URL` | Public URL (for emails) | `http://localhost:8080` |
| `JWT_SECRET` | Secret for JWT tokens | `change-this-secret-in-production` |
| `ADMIN_EMAIL` | Initial admin email | `admin@rapibase.local` |
| `ADMIN_PASSWORD` | Initial admin password | `admin123` |
| `SMTP_HOST` | SMTP server host | - |
| `SMTP_PORT` | SMTP server port | `587` |
| `SMTP_USER` | SMTP username | - |
| `SMTP_PASS` | SMTP password | - |
| `SMTP_FROM` | From email address | - |

## API Endpoints

### Authentication
```
POST /api/v1/auth/login          - Login
POST /api/v1/auth/register       - Register
POST /api/v1/auth/forgot-password - Request password reset
POST /api/v1/auth/reset-password  - Reset password
POST /api/v1/auth/refresh        - Refresh token
GET  /api/v1/auth/me             - Get current user
```

### Tables
```
GET    /api/v1/tables            - List all tables
POST   /api/v1/tables            - Create table
GET    /api/v1/tables/:name      - Get table schema
DELETE /api/v1/tables/:name      - Drop table
```

### Rows
```
GET    /api/v1/tables/:name/rows     - Get rows (paginated)
POST   /api/v1/tables/:name/rows     - Insert row
PUT    /api/v1/tables/:name/rows/:id - Update row
DELETE /api/v1/tables/:name/rows/:id - Delete row
```

### Query & Import/Export
```
POST /api/v1/query               - Execute SQL query
POST /api/v1/import/sql          - Import SQL file
POST /api/v1/import/json/:table  - Import JSON to table
GET  /api/v1/export/:table       - Export table (format=json|sql)
```

## Architecture

```
rapibase/
├── cmd/rapibase/          # Application entry point
├── internal/
│   ├── api/               # HTTP handlers and routes
│   │   ├── handlers/      # Request handlers
│   │   └── middleware/    # Auth, rate limiting
│   ├── auth/              # JWT and SMTP
│   ├── config/            # Configuration
│   ├── database/          # PostgreSQL operations
│   └── models/            # Data models
├── web/                   # React frontend
├── Dockerfile
└── docker-compose.yml
```

## Security

- Passwords hashed with bcrypt (cost 12)
- JWT tokens with 24h expiration
- Refresh tokens with 7 day expiration
- Rate limiting on auth endpoints
- SQL injection prevention with prepared statements
- Internal tables protected from user access

## Development

```bash
# Backend (with hot reload)
go install github.com/cosmtrek/air@latest
air

# Frontend
cd web && npm run dev
```

## License

MIT License - feel free to use in personal and commercial projects.

## Contributing

Contributions are welcome! Please open an issue or PR.

---

Made with ❤️ for the open-source community
