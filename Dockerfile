# Build frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Build backend
FROM golang:1.23-alpine AS backend-builder
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o rapibase ./cmd/rapibase

# Final image
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend-builder /app/rapibase .
COPY --from=backend-builder /app/web/dist ./web/dist
# Realtime docs (markdown) are served at /api/realtime/docs/:slug and
# rendered by the dashboard's Realtime tab. Single source of truth —
# editing a .md here updates both the in-app docs and rapibase.com.
COPY --from=backend-builder /app/docs ./docs

EXPOSE 8080

ENV PORT=8080
ENV HOST=0.0.0.0

# Force IPv4 — Alpine's musl resolves `localhost` to ::1 first, but the
# Go binary only listens on IPv4 (0.0.0.0:8080). Without this the
# healthcheck always reports unhealthy even when the service is fine.
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/api/v1/health || exit 1

CMD ["./rapibase"]
