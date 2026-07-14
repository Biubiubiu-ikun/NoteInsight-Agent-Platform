# Phase 3 Cache, Ranking, Seedgen, and k6 Report

## Planning Source

`最新项目规划.md` is the current source of truth. Files whose names start with `旧版项目规划` are historical references only.

## Scope

Phase 3 adds operational cache/ranking infrastructure around the Phase 2C authenticated note domain.

Implemented in this phase:

- Redis note detail cache: `note:{note_id}`, TTL 10 minutes.
- Redis comments first-page cache: `note:{note_id}:comments:first_page:time`, TTL 60 seconds.
- Redis hot note ranking ZSET: `ranking:notes:daily`.
- Redis category hot note ranking ZSET: `ranking:notes:{category}:daily`.
- Redis hot comment ranking ZSET: `note:{note_id}:hot_comments`.
- Cache invalidation on comment creation, comment deletion, note mutation, note deletion, note like/collect/share, and comment like.
- Hot note score refresh after note create/update, comment create/delete, like, collect, and share.
- Hot comment score refresh after comment like.
- Prometheus metrics endpoint: `/metrics`.
- Seed data generator: `go run ./cmd/seedgen --profile=dev --seed=20260706 --truncate --with-tokens`.
- k6 load script: `load-tests/k6/phase3_notes.js`.

## New Directory Structure

```text
backend-go/
  cmd/
    seedgen/
      main.go
  internal/
    platform/
      observability/
        metrics.go
load-tests/
  k6/
    phase3_notes.js
scripts/
  run_k6_phase3.ps1
docs/
  phase3_cache_ranking_report.md
```

## Migration SQL

No new migration is required for Phase 3. This phase uses the Phase 2B/2C schema:

- `notes.hot_score`
- `note_comments.like_count`
- `user_auth_tokens`
- idempotent primary keys on `note_likes`, `note_collects`, and `note_comment_likes`

## Cache And Ranking Behavior

Note detail:

```text
GET /api/v1/notes/{note_id}
cache key: note:{note_id}
TTL: 10 minutes
```

Comments first page:

```text
GET /api/v1/notes/{note_id}/comments?limit=20
cache key: note:{note_id}:comments:first_page:time
TTL: 60 seconds
```

Rankings:

```text
global notes:   ranking:notes:daily
category notes: ranking:notes:{category}:daily
hot comments:   note:{note_id}:hot_comments
```

Current note hot score formula:

```text
view_count + like_count * 3 + collect_count * 8 + comment_count * 6 + share_count * 5
```

Current hot comment score formula:

```text
like_count * 5
```

## Metrics

Prometheus metrics exposed at:

```text
GET /metrics
```

Added application metrics:

- `http_requests_total`
- `http_request_duration_seconds`
- `cache_hit_total`
- `cache_miss_total`
- `db_query_duration_seconds`
- `hot_ranking_update_total`

## Seedgen

Dry run:

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
go run ./cmd/seedgen --profile=dev --dry-run
```

Full dev seed:

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
go run ./cmd/seedgen --profile=dev --seed=20260706 --truncate --with-tokens
```

Dev profile volume:

- 1000 users
- 100 creators
- 5000 notes
- 20000 comments
- 100000 note likes
- 30000 note collects
- 50000 comment likes

Token output:

```text
backend-go/tmp/dev_tokens.csv
```

## Curl Smoke Tests

Register and login:

```bash
curl -s -X POST http://127.0.0.1:18080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"phase3_smoke","password":"password123","nickname":"Phase 3 Smoke"}'

curl -s -X POST http://127.0.0.1:18080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"phase3_smoke","password":"password123"}'
```

Create note, comment, like, collect:

```bash
curl -s -X POST http://127.0.0.1:18080/api/v1/notes \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Phase 3 smoke note","body":"Cache and ranking smoke note","category":"beauty","media":[{"media_type":"image","url":"https://example.com/phase3.jpg","caption":"caption","ocr_text":"ocr text","position":1}]}'

curl -s http://127.0.0.1:18080/api/v1/notes/$NOTE_ID
curl -s http://127.0.0.1:18080/api/v1/notes/$NOTE_ID

curl -s http://127.0.0.1:18080/api/v1/notes/$NOTE_ID/comments?limit=20
curl -s http://127.0.0.1:18080/api/v1/notes/$NOTE_ID/comments?limit=20

curl -s -X POST http://127.0.0.1:18080/api/v1/notes/$NOTE_ID/comments \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"content":"Phase 3 smoke comment","intent":"question"}'

curl -s -X POST http://127.0.0.1:18080/api/v1/notes/$NOTE_ID/like \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'

curl -s -X POST http://127.0.0.1:18080/api/v1/notes/$NOTE_ID/collect \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"collection_name":"phase3"}'

curl -s -X POST http://127.0.0.1:18080/api/v1/comments/$COMMENT_ID/like \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

Redis checks:

```bash
redis-cli EXISTS note:$NOTE_ID
redis-cli EXISTS note:$NOTE_ID:comments:first_page:time
redis-cli ZSCORE ranking:notes:daily $NOTE_ID
redis-cli ZSCORE ranking:notes:beauty:daily $NOTE_ID
redis-cli ZSCORE note:$NOTE_ID:hot_comments $COMMENT_ID
```

Metrics checks:

```bash
curl -s http://127.0.0.1:18080/metrics | grep cache_hit_total
curl -s http://127.0.0.1:18080/metrics | grep cache_miss_total
```

## k6

The local project uses the official Docker image `grafana/k6:latest`, so a system-level `k6` install is not required.

Install/pull the image:

```powershell
docker pull grafana/k6:latest
```

Run after seedgen with dev tokens:

```powershell
.\scripts\run_k6_phase3.ps1 -Vus 20 -Duration 1m
```

Direct Docker command:

```powershell
docker run --rm `
  -v "${PWD}:/work" `
  -w /work `
  -e BASE_URL=http://host.docker.internal:18080 `
  -e VUS=20 `
  -e DURATION=1m `
  grafana/k6:latest run load-tests/k6/phase3_notes.js
```

k6 summary includes P50/P95/P99 through `summaryTrendStats`.

## Test Commands

```powershell
cd backend-go
go test ./...
```

## Not In This Phase

- Redis hot feed materialization beyond simple ZSET rankings.
- RAG/Agent.
- Qdrant.
- Outbox/NATS.
- Kubernetes.
- Complex recommendation.
- Real image upload.
- Real SMS/email/OAuth.

## Phase 4 Preview

The next phase can build on Phase 3 with behavior-event outbox, async aggregation, and a more formal ranking/recommendation pipeline. RAG/Agent and creator insight workflows should still wait until the content/event substrate is stable.

## Verification Results

Local verification on 2026-07-06:

- `go test ./...` passed.
- `.\scripts\build_backend_linux.ps1` passed.
- `docker compose up -d --build` passed.
- `.\scripts\migrate.ps1` passed with existing migrations skipped.
- `/ready` returned PostgreSQL and Redis `ok`.
- Authenticated smoke test passed: request-body `author_id` was ignored and token user became note author.
- Note detail cache passed: after deleting `note:{note_id}`, first GET populated Redis and second GET was served with cache metrics available.
- Comments first-page cache passed: first GET created `note:{note_id}:comments:first_page:time`; creating a new comment deleted that key.
- Idempotency passed: repeated note like and collect returned `applied=false` on the second request.
- Hot notes passed: `ZSCORE ranking:notes:daily {note_id}` and `ZSCORE ranking:notes:beauty:daily {note_id}` returned score `28` in the smoke case.
- Hot comments passed: `ZSCORE note:{note_id}:hot_comments {comment_id}` returned score `5`.
- `/metrics` exposed both `cache_hit_total` and `cache_miss_total`.
- Full dev seed passed in `1m1.1167374s` with 1000 generated dev tokens.
- After seed plus one token smoke write, table counts were `users=1000`, `notes=5000`, `note_comments=20001`, `note_likes=100001`, `note_collects=30000`, `note_comment_likes=50001`, `user_auth_tokens=1000`.
- Seeded Redis ranking check passed: `ZCARD ranking:notes:daily` returned `5000`.

Local k6 verification on 2026-07-08:

- Installed/pulled Docker image: `grafana/k6:latest`.
- Docker image version: `k6 v2.1.0`.
- Added convenience runner: `.\scripts\run_k6_phase3.ps1`.
- 1 VU / 5s smoke passed with read and write checks enabled.
- 20 VU / 1m Phase 3 run passed.
- Checks: `3409/3409` succeeded.
- HTTP failure rate: `0.00%`.
- HTTP request duration: P50 `166.94ms`, P95 `565.5ms`, P99 `784.46ms`, max `1.41s`.
- Throughput: `3409` HTTP requests, `54.27 req/s`, `487` iterations.
- Thresholds passed: `http_req_failed rate<0.05`, `http_req_duration p(95)<800`.
- Post-k6 table counts: `users=1000`, `notes=5000`, `note_comments=20500`, `note_likes=100480`, `note_collects=30485`, `note_comment_likes=50500`, `user_auth_tokens=1000`.
- Post-k6 metrics included `cache_hit_total`, `cache_miss_total`, and `hot_ranking_update_total`.
