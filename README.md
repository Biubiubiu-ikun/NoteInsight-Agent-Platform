# NoteInsight Agent Platform

NoteInsight Agent Platform is an AI search and insight platform for a Xiaohongshu-style image-text note community. The project starts with a solid Go community backend, then grows into behavior simulation, Redis cache/ranking, RAG/Agent runtime, evaluation, observability, and deployment.

`最新项目规划.md` is the primary planning document. Files whose names start with `旧版项目规划` are retained as historical reference only.

## Current Status

Phase 5B completes deterministic behavior simulation and a RAG-ready Chinese text corpus on top of the Phase 4D asynchronous interaction pipeline:

- Go + Gin API service
- `/health` and `/ready`
- `/metrics` with Prometheus metrics
- Environment based configuration
- Structured logs
- PostgreSQL and Redis initialization
- Docker Compose for backend, PostgreSQL, and Redis
- Minimal unit tests
- sqlx-based PostgreSQL repository layer
- Core APIs for notes, note media, note comments, note likes, note collects, note shares, and note comment likes
- Registration, login, JWT access tokens, refresh-token sessions, `/me`
- Write APIs read `user_id` from token instead of trusting request bodies
- Author/admin edit and delete guards
- banned/deleted user write restrictions
- Redis note detail cache and comments first-page cache
- Redis hot note, category hot note, and hot comment ZSET rankings
- `cmd/seedgen` dev profile with generated `user_auth_tokens`
- k6 script for read/write note workload
- transactional `outbox_events`
- idempotent `processed_events`
- `behavior_events` for note-domain write actions
- standalone worker process that publishes PostgreSQL Outbox events
- NATS JetStream file-backed event stream and durable pull consumer
- dead-letter stream with delayed redelivery and bounded attempts
- broker and database idempotency using `Nats-Msg-Id` plus `processed_events`
- worker health, readiness, and Prometheus metrics on port `18081`
- Redis fixed-window rate limiting for content, comment, and interaction writes
- stale Outbox `processing` lease recovery
- hourly PostgreSQL counter/hot-score and Redis ranking reconciliation
- one-shot `cmd/reconcile` repair command
- worker-side exact counter and hot-score rebuilds from source-of-truth fact tables
- `comment.deleted` events for asynchronous comment/reply count repair
- event-lag, Outbox-age, and derived-refresh metrics
- repeatable Phase 2C auth acceptance and Phase 4D NATS-outage tests
- deterministic persona and Markov-session behavior simulator
- Zipf/Pareto/Poisson distributions plus viral and controversy burst scenarios
- streaming NDJSON and transactional PostgreSQL simulator sinks
- strict distribution reports and `smoke`, `dev`, and `scale` profiles
- meaningful Chinese note bodies, captions, OCR text, and note-linked comments in `seedgen`
- hidden note scenarios and a 200-note/40,000-comment Agent-quality corpus
- 1,000 ground-truth cases covering summary, procedure, controversy, audience, and OCR
- strict text uniqueness, length, semantic-alignment, and task-coverage checks
- GitHub Actions quality and container-backed integration workflows
- production startup rejection for the default development JWT secret

## Project Layout

```text
backend-go/        Go backend service
docs/              Architecture and phase documents
load-tests/        k6 scripts and future load-test assets
scripts/           Local build, migration, and k6 helper scripts
docker-compose.yml Local API/worker/PostgreSQL/Redis/NATS runtime
最新项目规划.md Primary planning document
旧版项目规划*.md Historical planning documents
```

## Quick Start

```powershell
.\scripts\build_backend_linux.ps1
docker compose up -d --build
.\scripts\migrate.ps1
curl http://127.0.0.1:18080/health
curl http://127.0.0.1:18080/ready
curl http://127.0.0.1:18081/ready
```

## Seed Dev Data

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
go run ./cmd/seedgen --profile=dev --seed=20260706 --truncate --with-tokens
```

This writes dev bearer tokens to `backend-go/tmp/dev_tokens.csv`.

## Behavior Simulator

```powershell
cd backend-go
go run ./cmd/simulator --profile=smoke --scenario=mixed --replace
go run ./cmd/simulator --profile=scale --no-event-files --strict=true
```

To generate against the current database, set `POSTGRES_DSN` and add
`--dataset=database --write-db`. Bulk behavior generation is local and deterministic;
it does not require an LLM API.

## Quality Text Corpus

```powershell
.\scripts\generate_quality_corpus.ps1 `
  -Profile quality `
  -RunId phase5b_quality_20260714 `
  -Replace
```

Generated image rows may have no real URL, but every row carries a meaningful caption,
OCR text, and semantic role. Hidden scenarios and ground-truth cases are stored for the
future Evidence Store and RAG evaluation pipeline. Bulk corpus generation does not call
an LLM API.

## k6 Load Test

```powershell
docker pull grafana/k6:latest
.\scripts\run_k6_phase3.ps1 -Vus 20 -Duration 1m
```

The helper script runs the Docker image against `http://host.docker.internal:18080`.

Run the Phase 6 capacity matrix and build its local result index:

```powershell
.\scripts\run_k6_phase6.ps1 -Profile baseline -Workload mixed -Rate 30 -Duration 45s
.\scripts\run_k6_phase6.ps1 -Profile step -Workload mixed
.\scripts\run_k6_phase6.ps1 -Profile spike -Workload comments_read `
  -AccessPattern hotspot -HotNoteCount 100 -SpikeRps 120
.\scripts\analyze_phase6_results.ps1
```

Prepare future larger data sets without an LLM API:

```powershell
.\scripts\generate_capacity_data.ps1 -Profile capacity -DryRun
# Use -Truncate only with a dedicated or backed-up PostgreSQL volume.
.\scripts\generate_capacity_data.ps1 -Profile capacity -Truncate -WithTokens
```

Run the event-pipeline outage scenario:

```powershell
.\scripts\run_k6_phase4d_fault.ps1
```

Run the full local authentication and authorization acceptance suite:

```powershell
.\scripts\smoke_phase2c_auth.ps1
```

## Content API Smoke Test

```bash
curl -X POST http://127.0.0.1:18080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"alice_demo","password":"strong_password_123","nickname":"Alice"}'

curl -X POST http://127.0.0.1:18080/api/v1/notes \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -d '{"author_id":10001,"title":"Demo Note","body":"smoke test","category":"beauty","media":[{"caption":"cover image","ocr_text":"note text","position":1}]}'
```

## Test

```bash
cd backend-go
go test ./...
```

## Manual Reconcile

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
go run ./cmd/reconcile
```

## Phase Docs

- [Phase 3 cache/ranking report](docs/phase3_cache_ranking_report.md)
- [Phase 4A outbox and behavior events](docs/04_phase4a_outbox_behavior.md)
- [Phase 4B rate limiting and reconciliation](docs/05_phase4b_rate_limit_reconcile.md)
- [Phase 4C standalone JetStream worker](docs/06_phase4c_jetstream_worker.md)
- [Phase 4D async counters and fault verification](docs/07_phase4d_async_counters.md)
- [Phase 5A behavior simulator](docs/08_phase5a_behavior_simulator.md)
- [Phase 5B quality text corpus](docs/09_phase5b_quality_corpus.md)
- [Phase 6A capacity testing](docs/10_phase6_capacity_testing.md)
- [Project progress and quality audit](docs/00_progress_audit.md)
