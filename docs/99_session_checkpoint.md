# Session Checkpoint

## Planning Precedence

`最新项目规划.md` is the primary planning document.

Files whose names start with `旧版项目规划` remain historical reference material only. When planning documents differ, `最新项目规划.md` takes precedence.

## Current Progress

Phase 5A behavior simulation and the first CI quality baseline have been completed on top of the Phase 4D asynchronous event pipeline.

Completed:

- Phase 1 Go backend skeleton
- Phase 2B note-domain pivot from video/danmu to notes
- `danmus` table and danmu API removed
- `notes`, `note_media`, `note_comments`, `note_likes`, `note_collects`, `note_shares`, and `note_comment_likes`
- note/comment keyset pagination
- note like, collect, and comment like idempotency
- `sqlx` repository layer; GORM is not used
- account registration and login
- bcrypt password hashing
- JWT access tokens
- random refresh tokens stored only as hashes
- current user APIs: `/api/v1/me`
- write APIs read user identity from auth context
- author/admin edit and delete guards
- banned/deleted user write restrictions
- Prometheus `/metrics`
- note detail Redis cache: `note:{note_id}`
- comments first-page Redis cache: `note:{note_id}:comments:first_page:time`
- global hot note ZSET: `ranking:notes:daily`
- category hot note ZSET: `ranking:notes:{category}:daily`
- hot comment ZSET: `note:{note_id}:hot_comments`
- cache invalidation for note/comment writes and interactions
- `cmd/seedgen` dev profile
- generated dev bearer tokens in `backend-go/tmp/dev_tokens.csv`
- k6 script in `load-tests/k6/phase3_notes.js`
- Docker k6 runner in `scripts/run_k6_phase3.ps1`
- Docker image `grafana/k6:latest` pulled locally
- Phase 3 report in `docs/phase3_cache_ranking_report.md`
- Phase 4A migration: `backend-go/migrations/000004_phase4a_outbox_behavior.sql`
- transactional outbox table: `outbox_events`
- idempotent processing table: `processed_events`
- behavior event table: `behavior_events`
- local in-process outbox worker in `internal/worker`
- Phase 4A report in `docs/04_phase4a_outbox_behavior.md`
- Phase 4A local smoke passed: `outbox_events sent=8`, `behavior_events` recorded all six write-action types, and async Redis ranking refresh metrics were exposed.
- Phase 4B migration: `backend-go/migrations/000005_phase4b_reconcile.sql`
- Redis user-level fixed-window rate limits for content, comment, and interaction writes
- standard `429`, `Retry-After`, and `X-RateLimit-*` responses
- stale Outbox `processing` lease recovery
- hourly source-of-truth reconciliation for note/comment counts and hot scores
- atomic Redis ranking rebuild and stale note/comment cache invalidation
- one-shot repair command: `backend-go/cmd/reconcile`
- Phase 4B report in `docs/05_phase4b_rate_limit_reconcile.md`
- Phase 4B smoke passed: rate limit `200,200,200,429`; one stale Outbox event recovered; deliberately corrupted counters, hot score, rankings, and caches repaired.
- NATS Go client pinned at `github.com/nats-io/nats.go v1.52.0`
- Docker image `nats:2.12-alpine` installed
- file-backed `NOTEINSIGHT_EVENTS` stream and `NOTEINSIGHT_DLQ` stream
- durable pull consumer `noteinsight-worker-v1`
- standalone `cmd/worker`; API no longer runs publisher/reconcile loops
- publisher-side `Nats-Msg-Id` deduplication and database `processed_events` consumer idempotency
- delayed redelivery, five-attempt poison-message policy, and DLQ publishing
- worker readiness and metrics at `http://127.0.0.1:18081`
- Phase 4C report in `docs/06_phase4c_jetstream_worker.md`
- Phase 4C smoke passed: end-to-end event, publisher duplicate, consumer duplicate, poison DLQ, and NATS outage recovery.
- Phase 4C `go test ./...` and `go vet -p 1 ./...` passed.
- Final Phase 4C runtime state: Outbox `sent=10`, event stream messages `4`, DLQ messages `1`, pending `0`, and ack-pending `0`.
- API interaction transactions now write facts and Outbox events without synchronously mutating materialized counters.
- `internal/worker/repository.go` atomically claims each event, records behavior, and rebuilds exact counters from fact tables.
- `comment.deleted` now updates note/reply counters asynchronously.
- interaction responses expose `count_pending` to make eventual consistency explicit.
- worker cache invalidation prevents stale note/comment counts from surviving their Redis TTL.
- new metrics: `domain_event_lag_seconds`, `derived_data_refresh_total`, and `outbox_oldest_unsent_age_seconds`.
- repeatable Phase 2C acceptance: `scripts/smoke_phase2c_auth.ps1`.
- repeatable NATS outage test: `scripts/run_k6_phase4d_fault.ps1` and `load-tests/k6/phase4d_event_pipeline.js`.
- Phase 4D report: `docs/07_phase4d_async_counters.md`.
- project-wide progress and quality audit: `docs/00_progress_audit.md`.
- Phase 4D local smoke passed: stopped-worker eventual consistency, comment deletion, duplicate processing with a missing marker, and broker outage recovery.
- Phase 4D fault k6 passed with 120/120 writes, `0.00%` HTTP failures, P95 `32.46ms`, and a fully drained event pipeline.
- Phase 5A migration: `backend-go/migrations/000006_phase5a_behavior_simulator.sql`.
- deterministic simulator command: `backend-go/cmd/simulator`.
- eight personas, ten session states, four traffic scenarios, and fixed-seed replay.
- streaming NDJSON and transactional sqlx database sinks.
- database smoke passed with 500 sessions, 2,645 events, 50 profiles, and zero invalid session sequences.
- scale profile passed strict checks with 250,000 sessions and 1,322,565 events in about 1 minute 50 seconds.
- Phase 5A report: `docs/08_phase5a_behavior_simulator.md`.
- GitHub Actions now runs Go quality checks and a container-backed acceptance test.
- the Phase 2C smoke script now handles HTTP failures on both Windows PowerShell and PowerShell 7.
- production startup rejects the built-in development JWT secret.

## Local Ports

- Backend: `http://127.0.0.1:18080`
- PostgreSQL: `localhost:15432`
- Redis: `localhost:6379`
- Worker: `http://127.0.0.1:18081`
- NATS client: `localhost:14222`
- NATS monitoring: `http://127.0.0.1:18222`

The backend and PostgreSQL ports were moved away from `8080` and `5432` because those ports were already used on this machine.

## Start Next Time

From the project root:

```powershell
.\scripts\build_backend_linux.ps1
docker compose up -d --build
.\scripts\migrate.ps1
Invoke-RestMethod -Uri http://127.0.0.1:18080/ready
Invoke-RestMethod -Uri http://127.0.0.1:18081/ready
```

Run tests:

```powershell
cd backend-go
go test -p 1 -timeout 60s ./...
```

Seed dev data:

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
go run ./cmd/seedgen --profile=dev --seed=20260706 --truncate --with-tokens
```

The latest local seed completed in about 1 minute and produced 1000 dev tokens.

Run Phase 3 k6:

```powershell
.\scripts\run_k6_phase3.ps1 -Vus 20 -Duration 1m
```

The latest local k6 run on 2026-07-08 passed with 20 VUs for 1 minute: 3409/3409 checks succeeded, HTTP failure rate 0.00%, P50 166.94ms, P95 565.5ms, P99 784.46ms.

Run Phase 2C automated acceptance:

```powershell
.\scripts\smoke_phase2c_auth.ps1
```

Run Phase 4D broker-outage verification:

```powershell
.\scripts\run_k6_phase4d_fault.ps1
```

Phase 4A smoke:

```powershell
docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -t -A -F "," -c "SELECT status, COUNT(*) FROM outbox_events GROUP BY status ORDER BY status;"
docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -t -A -F "," -c "SELECT event_type, COUNT(*) FROM behavior_events GROUP BY event_type ORDER BY event_type;"
```

Run Phase 4B reconciliation manually:

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
go run ./cmd/reconcile
```

The latest manual reconcile on 2026-07-14 completed with `stale_outbox_recovered=0`, `notes_repaired=0`, and `comments_repaired=0` after the deliberate drift test had already been repaired by the scheduled reconciler.

## Stop Runtime

```powershell
docker compose down
```

This stops and removes containers but keeps the named PostgreSQL and NATS volumes, so database data and JetStream messages remain available.

Runtime is running during the 2026-07-14 development session. PostgreSQL and Redis data still use the existing named volume/data, and generated dev tokens remain in `backend-go/tmp/dev_tokens.csv`.

The running backend uses the normal `RATE_LIMIT_INTERACTION_WRITE_LIMIT=120` default. API and worker readiness are healthy. NATS recovered from the outage smoke test and its volume retained both event and DLQ streams.

Final Phase 4D runtime snapshot on 2026-07-14:

- all five Compose services are running; PostgreSQL, Redis, and NATS are healthy;
- API readiness reports PostgreSQL and Redis `ok`;
- worker readiness reports PostgreSQL, Redis, and NATS `ok`;
- Outbox has `sent=142` and no pending, processing, retry, or failed rows;
- JetStream consumer pending and ack-pending are both `0`;
- `outbox_oldest_unsent_age_seconds` is `0`;
- full fact-table comparison reports `note_drift=0` and `comment_drift=0`;
- the latest deployed worker exposes Phase 4D lag and derived-refresh metrics.

## Next Development Step

Recommended Phase 5B and quality track:

- add deterministic user and note lifecycle evolution across multi-week simulations;
- optionally materialize selected simulated actions into fact tables without bypassing
  database uniqueness constraints;
- add Prometheus, Grafana, and alert-rule assets for the existing API/worker metrics;
- add controlled DLQ inspection/replay and event-retention commands;
- reuse simulator sessions in reproducible steady, spike, soak, and outage tests.

High-volume behavior generation remains local and does not use an LLM API. A future
LLM-backed content corpus should be smaller, versioned, reviewed, and generated offline.

The host still does not have a system-level `k6` command in PATH; use the Docker runner script above.
