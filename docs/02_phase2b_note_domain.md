# Phase 2B Note Domain Refactor

## Current Goal

Phase 2B refactors the early video/danmu content API into a Xiaohongshu-style image-text note domain.

This phase only changes the domain model and synchronous CRUD-style interaction APIs. It does not add Redis cache, RAG/Agent, outbox events, ranking, or async workers.

## Planning Rule

`NoteInsight_Agent_Platform_V4_Spec_Codex.md` is the primary planning document. The old `项目规划.md` is retained as reference material, but V4 wins when the two differ.

## Directory Changes

```text
backend-go/internal/content/                 removed
backend-go/internal/note/                    new note domain package
backend-go/internal/api/handlers/content.go  removed
backend-go/internal/api/handlers/note.go     new notes HTTP handler
backend-go/migrations/000002_phase2b_notes_domain.sql
```

## Database Model

New tables:

- `users`
- `user_auth_tokens`
- `notes`
- `note_media`
- `note_comments`
- `note_likes`
- `note_collects`
- `note_shares`
- `note_comment_likes`

Deprecated Phase 2 video tables are dropped by migration `000002_phase2b_notes_domain.sql`:

- `videos`
- `comments`
- `comment_likes`
- `danmus`

The repository layer uses `sqlx`; GORM is not used.

## API

```http
POST /api/v1/notes
GET  /api/v1/notes/{note_id}
GET  /api/v1/notes?category=beauty&cursor=xxx&limit=20
POST /api/v1/notes/{note_id}/comments
GET  /api/v1/notes/{note_id}/comments?limit=20
POST /api/v1/notes/{note_id}/like
POST /api/v1/notes/{note_id}/collect
POST /api/v1/notes/{note_id}/share
POST /api/v1/comments/{comment_id}/like
```

## Run Migration

```powershell
.\scripts\migrate.ps1
```

The script connects to the Compose PostgreSQL instance through `localhost:15432`.

## Curl Smoke Tests

Create note:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/notes \
  -H "Content-Type: application/json" \
  -d '{"author_id":10001,"title":"油皮夏天底妆记录","body":"通勤八小时后观察控油和暗沉情况。","category":"beauty","topics":["base_makeup"],"tags":["oil_skin","budget"],"media":[{"media_type":"image","url":"https://example.com/note-1.jpg","caption":"上妆后自然光效果","ocr_text":"持妆 粉底 控油","position":1}]}'
```

List notes:

```bash
curl "http://127.0.0.1:18080/api/v1/notes?category=beauty&limit=20"
```

Create comment:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/notes/{note_id}/comments \
  -H "Content-Type: application/json" \
  -d '{"user_id":10002,"content":"混油皮可以参考吗？","intent":"ask_suitable"}'
```

List comments:

```bash
curl "http://127.0.0.1:18080/api/v1/notes/{note_id}/comments?limit=20"
```

Like note:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/notes/{note_id}/like \
  -H "Content-Type: application/json" \
  -d '{"user_id":10002}'
```

Collect note:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/notes/{note_id}/collect \
  -H "Content-Type: application/json" \
  -d '{"user_id":10002,"collection_name":"makeup"}'
```

Share note:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/notes/{note_id}/share \
  -H "Content-Type: application/json" \
  -d '{"user_id":10002,"channel":"wechat"}'
```

Like comment:

```bash
curl -X POST http://127.0.0.1:18080/api/v1/comments/{comment_id}/like \
  -H "Content-Type: application/json" \
  -d '{"user_id":10003}'
```

## Tests

```powershell
cd backend-go
go test ./...
```

## Not In This Phase

- Redis cache
- Hot ranking
- RAG/Agent
- Qdrant/vector index
- Outbox/NATS/async events
- K8s
- Complex recommendation system

## Next Step

Phase 3 should add Redis note detail cache, comment first-page cache, hot notes/hot comments ZSET ranking, cache metrics, `/metrics`, seedgen dev profile, and k6 smoke pressure tests.
