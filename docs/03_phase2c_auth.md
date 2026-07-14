# Phase 2C Auth And Publishing Guardrails

## Current Goal

Phase 2C upgrades the note APIs from development mode, where `user_id` was trusted from request bodies, to login-state mode:

- Account registration and login
- Password hashing
- JWT access token
- Random refresh token stored only as a hash
- Current user APIs
- Auth middleware and write-route guards
- Note/comment author permission checks
- Banned/deleted user write restrictions

This phase still does not add Redis cache, hot rankings, RAG/Agent, Qdrant, Outbox/NATS, K8s, real SMS/email/OAuth, or real image upload.

## Planning Rule

`NoteInsight_Agent_Platform_V4_1_Phase2C_Auth_Spec_Codex.md` is the primary planning document. Older planning documents are historical references only.

## Directory Changes

```text
backend-go/internal/auth/                  auth domain, password hash, JWT, sessions
backend-go/internal/api/ctxauth/           Gin auth context helpers
backend-go/internal/api/handlers/auth.go   auth HTTP handler
backend-go/internal/api/middleware.go      AuthMiddleware, RequireAuth, RequireActiveUser, RequireOwnerOrAdmin
backend-go/migrations/000003_phase2c_auth.sql
docs/03_phase2c_auth.md
```

## Migration SQL

Migration `000003_phase2c_auth.sql`:

- Adds missing `users` fields such as `bio`.
- Adds a `users_id_seq` default for formal registration.
- Adds unique `users.username`.
- Creates `user_credentials`.
- Creates `user_sessions`.
- Keeps `user_auth_tokens` for seedgen/k6/dev tokens.

## Auth Implementation

- Repository: [internal/auth/repository.go](<../backend-go/internal/auth/repository.go>)
- Service: [internal/auth/service.go](<../backend-go/internal/auth/service.go>)
- JWT/refresh token helpers: [internal/auth/token.go](<../backend-go/internal/auth/token.go>)
- Handler: [internal/api/handlers/auth.go](<../backend-go/internal/api/handlers/auth.go>)
- Middleware: [internal/api/middleware.go](<../backend-go/internal/api/middleware.go>)

Password hashes use bcrypt. Access tokens use JWT. Refresh tokens are random strings and only `sha256` hashes are stored.

## API Changes

New auth APIs:

```http
POST /api/v1/auth/register
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout
GET  /api/v1/me
PATCH /api/v1/me
```

Public read APIs remain anonymous:

```http
GET /api/v1/notes/{note_id}
GET /api/v1/notes?category=beauty&cursor=xxx&limit=20
GET /api/v1/notes/{note_id}/comments?cursor=xxx&limit=20
```

Write APIs now require `Authorization: Bearer <access_token>`:

```http
POST   /api/v1/notes
PATCH  /api/v1/notes/{note_id}
DELETE /api/v1/notes/{note_id}
POST   /api/v1/notes/{note_id}/comments
DELETE /api/v1/comments/{comment_id}
POST   /api/v1/notes/{note_id}/like
POST   /api/v1/notes/{note_id}/collect
POST   /api/v1/notes/{note_id}/share
POST   /api/v1/comments/{comment_id}/like
```

`author_id` and `user_id` in request bodies are no longer trusted. Handlers read the current user from auth context and pass it into the service layer.

## Curl Smoke Tests

Register:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"alice_demo","password":"strong_password_123","nickname":"Alice"}'
```

Login:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice_demo","password":"strong_password_123"}'
```

Create note:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/notes \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -d '{"author_id":999999,"title":"通勤底妆记录","body":"请求体 author_id 会被忽略，真实作者来自 token。","category":"beauty","media":[{"caption":"自然光","ocr_text":"控油 持妆","position":1}]}'
```

Comment:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/notes/${NOTE_ID}/comments \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -d '{"user_id":999999,"content":"评论 user_id 也会被忽略。","intent":"experience_share"}'
```

Like and collect:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/notes/${NOTE_ID}/like \
  -H "Authorization: Bearer ${ACCESS_TOKEN}"

curl -X POST http://127.0.0.1:18080/api/v1/notes/${NOTE_ID}/collect \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -d '{"collection_name":"makeup"}'
```

## Tests

```powershell
cd backend-go
go test ./...
```

## Not In This Phase

- Redis cache
- Hot notes/hot comments ZSET
- RAG/Agent
- Qdrant
- Outbox/NATS
- K8s
- Complex recommendation
- Real SMS/email/OAuth
- Real image upload

## Updated Phase 3 Plan

Phase 3 should build on real login-state and seed/dev tokens:

- note detail cache
- comments first-page cache
- hot notes Redis ZSET
- category hot notes Redis ZSET
- hot comments Redis ZSET
- `/metrics`
- cache hit/miss metrics
- seedgen dev profile
- generated `user_auth_tokens` for k6/dev token pool
- k6 scripts with `Authorization: Bearer <token>` for write APIs
- `docs/phase3_cache_ranking_report.md`
