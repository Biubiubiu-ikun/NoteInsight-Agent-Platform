# Session Checkpoint

Updated: 2026-07-16

## Authority

`最新项目规划.md` V6.8 is authoritative. Old-version planning files are history only.

## Current State

- Phase 1 through Phase 6C and Phase 7A-0 are implemented; Phase 6B keeps a long-soak performance gate open.
- Phase 5C fact materialization is complete.
- Local P0/P1 pre-retrieval gaps are closed. The sanitized public GitHub remote has Actions, CodeQL upload, CODEOWNERS, PR/security policy and protected `main`; the full holdout and pre-public history remain in a private archive. Independent human holdout review and production/long-soak gates remain open.
- Phase 6C is recoverable at commit `f0dee23`, annotated tag `v0.6.4`; release hardening is preserved by tag `v0.6.5`.
- Dataset version `2` freezes 113,921 source references at checksum `b91df11ca9136e000c759fd2c6de5b448816bb57d903849c478f99db8533eab5`.
- Approved retrieval benchmark v4 has 240 unique cases and checksum `851a0ae94df77291d72904185754a2bea65893826fa942d52961472b65ab1b74`; v3 is retired.
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

cd ..
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
.\scripts\smoke_phase2c_auth.ps1

cd backend-go
go run ./cmd/evalfreeze -verify-only `
  -output-dir ../evaluation/benchmarks/retrieval_v4

cd ..
docker compose run --rm --no-deps `
  --entrypoint /app/noteinsight-reconcile worker --full
```

Latest verified data:

- quality run: `phase6c_quality_v2_20260715`, 200 notes, 40,000 comments, 1,619 eval cases;
- fact run: `phase6c_final_20260715`, 812 note facts, 481 user facts;
- independent benchmark: `retrieval_v4_20260716`, 80 public development cases + 160 sealed holdout cases, with 240 random-nonce commitments and eight task families;
- frozen retrieval dataset: version `2`, 113,921 source references, checksum `b91df11ca9136e000c759fd2c6de5b448816bb57d903849c478f99db8533eab5`;
- main database after Phase 7A-0 acceptance: 5,511 active notes, 6,813 media, 101,635 active comments and 113,927 active Evidence Sources;
- invariants: zero missing dataset source, counter drift, duplicate dataset, active Outbox, JetStream pending or redelivery;
- isolated PostgreSQL restore drill passed; Phase 7A-0 archive is `artifacts/backups/noteinsight_20260716_033326.dump` with SHA-256 `3424FEF52C080F432E6F230DB579EAE51379CC90C8F1C90CDCED89745F140E92` and a parseable 316-entry TOC;
- delayed deleted-note view replay passed and DLQ did not grow.
- frontend browser smoke passed for search, deep-linked detail, structured media text, comments, ranking and runtime status with no console errors.
- Go statement coverage is 27.72% with a 25% CI floor; frontend statement coverage is 60.84% with four metric floors.
- Govulncheck reports zero reachable vulnerabilities; Trivy reports zero fixable HIGH/CRITICAL findings for the Go 1.26.5 scratch image; SPDX SBOM generation passed.
- Public GitHub remote: `https://github.com/Biubiubiu-ikun/NoteInsight-Agent-Platform`; CodeQL uploads to GitHub Code Scanning, while the private archive uses local-SARIF mode.

## Stop

```powershell
docker compose -f docker-compose.yml `
  -f deploy/observability/docker-compose.observability.yml down
```

Named PostgreSQL, NATS, Prometheus and Grafana volumes are preserved unless `-v` is supplied.
