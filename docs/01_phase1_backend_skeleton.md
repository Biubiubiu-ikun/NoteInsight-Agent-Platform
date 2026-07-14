# Phase 1 Backend Skeleton

## Current Goal

Phase 1 builds the smallest runnable Go backend foundation for CreatorInsight Agent Platform.

It includes:

- Go module and API entrypoint
- Gin HTTP router
- `/health` liveness endpoint
- `/ready` dependency readiness endpoint
- Environment based config loading
- Structured `slog` logs
- PostgreSQL pool initialization
- Redis client initialization
- Docker Compose for backend, PostgreSQL, and Redis
- Minimal unit tests

## Directory Design

```text
backend-go/
  cmd/api/                 API process entrypoint
  internal/api/            HTTP router, middleware, handlers
  internal/config/         Environment config loading and validation
  internal/platform/       Reusable infrastructure adapters
    cache/                 Redis client setup
    database/              PostgreSQL pool setup
    logging/               Structured logger setup
  configs/                 Local configuration examples
docs/                      Project documents and phase notes
docker-compose.yml         Local runtime dependencies and backend service
```

`internal` keeps implementation packages private to the Go module. Business modules added in Phase 2 should be placed under focused packages such as `internal/content`, `internal/comment`, and `internal/danmu` instead of being embedded directly in handlers.

## API

`GET /health`

```json
{"status":"ok"}
```

`GET /ready`

Checks PostgreSQL and Redis. Returns `200` when both are reachable and `503` when a dependency is unavailable.

## Run Locally

```bash
cd backend-go
go mod tidy
go run ./cmd/api
```

For local `go run`, PostgreSQL and Redis must already be available at the addresses configured by environment variables. The Compose PostgreSQL instance is exposed on host port `15432` to avoid colliding with an existing local PostgreSQL service.

## Run With Docker Compose

The backend image uses a locally cross-compiled static Linux binary so Docker does not need to pull a Go builder image.

```powershell
.\scripts\build_backend_linux.ps1
docker compose up -d --build
curl http://127.0.0.1:18080/health
curl http://127.0.0.1:18080/ready
```

If your Docker network can pull `golang` images reliably, this can later be changed back to a multi-stage Dockerfile.

## Tests

```bash
cd backend-go
go test ./...
```

## Local Build

```bash
cd backend-go
go build ./cmd/api
```

## Next Step

Phase 2 should add the first content interaction domain:

- videos table and repository
- comments table and repository
- danmus table and repository
- comment likes with idempotency
- create/get/list HTTP APIs
