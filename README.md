# NoteInsight Agent Platform

NoteInsight is a creator-insight platform built on a Xiaohongshu-style image-text note community. The current system provides a production-minded Go data plane, deterministic Chinese corpora, retrieval evaluation cases, and a React testing console. Evidence ingestion, retrieval, and the grounded Agent are the next phases.

`最新项目规划.md` is the only authoritative plan. Files with old-version prefixes are historical references.

Private development remote: [Biubiubiu-ikun/NoteInsight-Agent-Platform](https://github.com/Biubiubiu-ikun/NoteInsight-Agent-Platform).

## Current Status

- Go + Gin + sqlx + PostgreSQL note community; GORM is not used.
- JWT access tokens, rotating hashed refresh sessions, ownership/admin/banned rules.
- Redis detail/comment caches and note/comment rankings.
- Transactional Outbox, NATS JetStream, idempotent worker, retry, DLQ and replay tooling.
- Project/dataset/visibility boundaries and versioned Evidence Source registry.
- Soft-delete propagation from notes/comments to active evidence.
- Deterministic behavior simulator, daily fact materialization and run lineage.
- Meaningful Chinese note/OCR/comment corpus plus a separate frozen six-task adversarial retrieval benchmark.
- React console for feed, search, ranking, auth, publishing, detail, comments and interactions.
- Prometheus metrics/alerts, provisioned Grafana dashboard, maintenance and recovery tools.
- OpenAPI/Gin drift checks, domain-event JSON Schema, Go/React/integration/E2E tests, Compose acceptance, CodeQL SARIF, SBOM and vulnerability gates.

Phase 6C is complete. Phase 7A canonical Evidence Store ingestion is next. The large-data 30-minute warm mixed-load gate remains open and is documented rather than hidden.

## Layout

```text
backend-go/             Go API, worker, migrations, data and ops commands
evaluation/             immutable retrieval benchmark manifests and JSONL cases
frontend/               React/Vite API testing console
deploy/                 capacity and observability configuration
docs/                   architecture, contracts, runbooks and phase evidence
load-tests/             k6 workloads and preserved local results
scripts/                build, migrate, corpus, backup and acceptance helpers
docker-compose.yml      API/worker/PostgreSQL/Redis/NATS runtime
最新项目规划.md          authoritative roadmap
旧版*.md                historical planning material
```

## Quick Start

```powershell
.\scripts\build_backend_linux.ps1
docker compose up -d --build --wait
.\scripts\migrate.ps1
.\scripts\start_frontend.ps1
```

Open:

- Frontend: `http://127.0.0.1:15173/`
- API readiness: `http://127.0.0.1:18080/ready`
- Worker readiness: `http://127.0.0.1:18081/ready`
- NATS monitor: `http://127.0.0.1:18222/`

The Vite server proxies API, worker, and NATS requests. Browser refresh tokens use an HttpOnly cookie; access tokens stay in memory.

## Observability

```powershell
docker compose -f docker-compose.yml `
  -f deploy/observability/docker-compose.observability.yml `
  up -d prometheus grafana
```

- Prometheus: `http://127.0.0.1:19090/`
- Grafana: `http://127.0.0.1:13000/`
- Local Grafana login: `admin` / `noteinsight-local` unless overridden by environment variables.

Local credentials are development-only.

## Quality Corpus

Bulk text generation is deterministic and does not call an LLM API. Image URLs may be empty, while caption and OCR text remain substantive.

```powershell
.\scripts\generate_quality_corpus.ps1 `
  -Profile quality `
  -Seed 20260715 `
  -RunId phase6c_quality_v2_20260715
```

The latest run produced 200 notes, 800 media rows, 40,000 comments and 1,619 evaluation cases across summary, procedure, controversy, audience, OCR, conflict, temporal, no-answer and cross-note tasks.

The independent retrieval baseline is `retrieval_v3_20260715`: 240 unique cases, 80 development, 160 holdout, six adversarial task families and manifest checksum `cb1494b76b38a23e0e20190614c104e1e7e22baa35bbb771cc340236335a3d35`.

```powershell
cd backend-go
go run ./cmd/evalfreeze -verify-only `
  -output-dir ../evaluation/benchmarks/retrieval_v3
```

## Fact Materialization

```powershell
docker compose run --rm --no-deps `
  --entrypoint /app/noteinsight-materialize backend `
  --days=3650 --run-id=my_fact_run
```

Facts retain `source_run_id` and can be rebuilt from behavior events.

## Verification

```powershell
cd backend-go
go test ./...
go vet ./...

cd ..

# Windows host does not need a local GCC for race detection
docker run --rm --mount "type=bind,source=$((Get-Location).Path),target=/workspace" `
  -w /workspace/backend-go golang:1.25.12-bookworm go test -race ./...

$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@127.0.0.1:15432/creatorinsight?sslmode=disable"
$env:NATS_URL = "nats://127.0.0.1:14222"
cd backend-go
go test -tags=integration -count=1 ./integration

cd ..\frontend
npm test
npm run typecheck
npm run build
npm run test:e2e

cd ..
.\scripts\migrate.ps1
.\scripts\migrate.ps1
.\scripts\smoke_phase2c_auth.ps1
```

The acceptance suite covers registration, refresh rotation/replay rejection, identity, ownership/admin/banned rules, idempotent interactions, keyset pagination, project-private reads, Evidence Source versioning/deletion and async convergence. The integration tag adds real transaction, unique-constraint, lock/lease, crash-recovery and DLQ replay coverage.

## Operations

```powershell
# Dry-run retention candidates
docker compose run --rm --no-deps `
  --entrypoint /app/noteinsight-maintenance backend

# Inspect DLQ
docker compose run --rm --no-deps `
  --entrypoint /app/noteinsight-dlqctl worker --limit=20

# Full counter/ranking repair after recovery or an integrity incident
docker compose run --rm --no-deps `
  --entrypoint /app/noteinsight-reconcile worker --full

# Backup PostgreSQL
.\scripts\backup_postgres.ps1
```

See `docs/13_recovery_runbook.md` and `docs/14_data_governance.md` before applying retention or restoring data.

## Load Testing

```powershell
.\scripts\run_k6_phase6.ps1 `
  -Profile baseline -Workload mixed -Rate 30 -Duration 45s
```

The isolated capacity environment and 4.21-million-row evidence are documented in `docs/11_phase6b_scale_soak.md`. Performance claims remain tied to the exact workload and host.

## Key Documents

- `最新项目规划.md`: current roadmap and gates.
- `docs/00_progress_audit.md`: current progress snapshot.
- `docs/12_project_excellence_review.md`: interviewer-style assessment.
- `docs/openapi.yaml`: HTTP contract.
- `docs/contracts/domain-event-v1.schema.json`: event envelope contract.
- `docs/13_recovery_runbook.md`: backup/recovery procedure.
- `docs/14_data_governance.md`: scope, deletion, retention and retrieval rules.
- `docs/15_quality_security_gates.md`: benchmark, test, contract and supply-chain gates.
