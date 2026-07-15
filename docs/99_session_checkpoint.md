# Session Checkpoint

Updated: 2026-07-15

## Authority

`最新项目规划.md` V6.4 is authoritative. Old-version planning files are history only.

## Current State

- Phase 1 through Phase 6C are implemented; Phase 6B keeps a long-soak performance gate open.
- Phase 5C fact materialization is complete.
- P0/P1 pre-retrieval gaps are closed; only the explicitly recorded production and long-soak gates remain open.
- Frontend testing console is available at `http://127.0.0.1:15173/` while the dev server is running.
- Next planned work is Phase 7A Evidence Store and deterministic ingestion.

## Runtime Ports

| Service | Address |
| --- | --- |
| Frontend | `http://127.0.0.1:15173/` |
| API | `http://127.0.0.1:18080` |
| Worker | `http://127.0.0.1:18081` |
| PostgreSQL | `127.0.0.1:15432` |
| Redis | `127.0.0.1:6379` |
| NATS | `127.0.0.1:14222` |
| NATS monitor | `http://127.0.0.1:18222` |
| Prometheus optional | `http://127.0.0.1:19090` |
| Grafana optional | `http://127.0.0.1:13000` |

## Resume

```powershell
.\scripts\build_backend_linux.ps1
docker compose up -d --build --wait
.\scripts\migrate.ps1
.\scripts\start_frontend.ps1
Invoke-RestMethod http://127.0.0.1:18080/ready
Invoke-RestMethod http://127.0.0.1:18081/ready
```

Optional observability stack:

```powershell
docker compose -f docker-compose.yml `
  -f deploy/observability/docker-compose.observability.yml up -d prometheus grafana
```

## Verification

```powershell
cd backend-go
go test ./...
go vet ./...

docker run --rm --mount "type=bind,source=$((Get-Location).Path),target=/src" `
  -w /src golang:1.25-bookworm /usr/local/go/bin/go test -race -count=1 ./...

cd ..\frontend
npm test
npm run typecheck
npm run build

cd ..
.\scripts\smoke_phase2c_auth.ps1

docker compose run --rm --no-deps `
  --entrypoint /app/noteinsight-reconcile worker --full
```

Latest verified data:

- quality run: `phase6c_quality_v2_20260715`, 200 notes, 40,000 comments, 1,619 eval cases;
- fact run: `phase6c_final_20260715`, 812 note facts, 481 user facts;
- main database: 5,475 active notes, 6,759 media, 101,626 active comments and 113,880 Evidence Sources;
- invariants: zero missing dataset source, counter drift, duplicate dataset, active Outbox, JetStream pending or redelivery;
- isolated PostgreSQL restore drill passed; final archive is `artifacts/backups/noteinsight_20260715_065618.dump`;
- delayed deleted-note view replay passed and DLQ did not grow.
- frontend browser smoke passed for search, deep-linked detail, structured media text, comments, ranking and runtime status with no console errors.

## Stop

```powershell
docker compose -f docker-compose.yml `
  -f deploy/observability/docker-compose.observability.yml down
```

Named PostgreSQL, NATS, Prometheus and Grafana volumes are preserved unless `-v` is supplied.
