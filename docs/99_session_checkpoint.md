# Session Checkpoint

Updated: 2026-07-19

## Authority

`最新项目规划.md` V7.7 is authoritative. Old-version planning files are history only.

## Current State

- Phase 1 through Phase 7C are implemented; Phase 6B keeps a long-soak performance gate open and Phase 7C retrieval quality gates remain failed baselines.
- Phase 5C fact materialization is complete.
- Local P0/P1 pre-retrieval gaps are closed. The sanitized public GitHub remote has Actions, CodeQL upload, CODEOWNERS, PR/security policy and protected `main`; the full holdout and pre-public history remain in a private archive. Independent human holdout review and production-like deployment gates remain open.
- Phase 6C is recoverable at commit `f0dee23`, annotated tag `v0.6.4`; release hardening is preserved by tag `v0.6.5`.
- Dataset version `2` freezes 113,921 source references at checksum `b91df11ca9136e000c759fd2c6de5b448816bb57d903849c478f99db8533eab5`.
- Approved retrieval benchmark v4 has 240 unique cases and checksum `851a0ae94df77291d72904185754a2bea65893826fa942d52961472b65ab1b74`; v3 is retired.
- Frontend testing console is available at `http://127.0.0.1:15173/` while the dev server is running.
- Phase 7A canonical evidence is complete on frozen dataset version `2`: 25,448 documents, 56,349 chunks and 153,348 exact citations.
- Phase 7B/7C provides authorization-filtered lexical, vector, and hybrid retrieval with exact citations, immutable index/evaluation lineage, public API, observability, and real PostgreSQL security tests.
- The full Qdrant index contains 56,349 points. Formal lexical, vector, and hybrid baselines all fail Recall/MRR gates; the sealed v4 holdout has not been used.
- Phase 7D vector recovery is complete locally: PostgreSQL lease/checkpoint resume, exact point-id/content-hash reconciliation, stale/orphan repair, immutable completion audit, snapshot retention, and an isolated 56,349-point Qdrant restore drill.
- Phase 7D local load evidence is complete: lexical v3 preserves v2 quality while halving formal P95; mixed 2 RPS passes without indexing, mixed 1 RPS passes with batch-8 indexing, and Qdrant/TEI restart recovery passes. Mixed 3 RPS and shared-index 2 RPS are retained failed capacity boundaries.
- The 30-minute warm mixed 2 RPS soak passes with 3,601 iterations, 0.6387 percent timeouts, zero dropped iterations/rate limits/invalid citations, and a successful recovery query.
- Phase 7D local distributed tracing is complete: W3C context crosses API, SQL/Redis, transactional Outbox, NATS and Worker, while hybrid retrieval includes TEI/Qdrant client spans. Collector, Tempo and the Grafana data source are provisioned; SQL/Redis content and credentials are not exported.
- Phase 7D benchmark v5 review engineering is complete: deterministic matrix/drafting, frozen Evidence Source resolution, blind reviewer assignments, loopback review UI with resumable D-drive persistence, identity conflict checks, overall/per-task agreement, adjudication queue, checksummed ledger, and fail-closed freeze output are implemented in `internal/evalreview` and `cmd/benchmarkreview`.
- Retrieval evaluation counts `out_of_domain_noise` together with `no_relevant_document` for rejection accuracy and false-positive classification while retaining a separate OOD task slice.
- The private v5 workspace now contains 288 model-assisted, unapproved authored cases, 1,728 candidate references resolving to 1,229 unique frozen sources, and separate reviewer A/B blind packages. The authored checksum is `da13ddd29c0f3cfbe2f8e7c4e133e431e09a218aff044cad78aa482c1051eedd`; every task has 32 cases and each split has 144.
- Human review count remains zero. No `submissions.jsonl`, adjudication, approved cases, or public review summary exists; v5 is not frozen and makes no retrieval quality claim.
- Next work is reviewer A in the loopback review UI, then a genuinely independent reviewer B, third-party adjudication, audit/freeze, and only then same-contract lexical/vector/hybrid comparison. Production-like multi-instance capacity, managed trace policy and deployment security evidence remain required before any public production-ready claim.

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
| Tempo optional | `http://127.0.0.1:13200` |
| OTLP gRPC optional | `127.0.0.1:14317` |
| OTLP HTTP optional | `http://127.0.0.1:14318` |
| OTel Collector health optional | `http://127.0.0.1:13133` |
| Qdrant retrieval profile | `http://127.0.0.1:16333` |
| TEI embedding profile | `http://127.0.0.1:18082` |
| Benchmark v5 review UI | `http://127.0.0.1:18083/` while `serve` is running |

## Local Storage

- Project runtime and tool caches live under the Git-ignored `D:\面向内容平台的创作者洞察 Agent 应用平台\.tools` tree.
- Docker Desktop's WSL disk is physically stored at `.tools\runtime\docker-desktop\wsl`; `C:\Users\23016\AppData\Local\Docker\wsl` is a directory junction to that location.
- Go build/module/temp, npm, and Hugging Face caches are physically stored under `.tools\cache` or `.tools\gopath`. Their former C-drive locations are compatibility junctions, and the corresponding user environment variables point to D.
- The D drive is external USB storage. The ignored local `.env` uses `RETRIEVAL_QUERY_TIMEOUT=25s` and `HTTP_WRITE_TIMEOUT=40s` so cold reads can finish; production/default values remain `4s` and `10s`.
- Use `scripts/start_local_stack.ps1` after a restart. It waits for the full stack and warms lexical, vector, and hybrid retrieval before reporting readiness.
- After a Docker Desktop update, verify the WSL junction with `Get-Item C:\Users\23016\AppData\Local\Docker\wsl`; its `Target` must remain the project `.tools\runtime` path.

## Resume

```powershell
.\scripts\start_local_stack.ps1 -Build -StartFrontend
Invoke-RestMethod http://127.0.0.1:18080/ready
Invoke-RestMethod http://127.0.0.1:18081/ready
```

The local runtime lives under the Git-ignored `.tools` tree. The startup script waits for dependencies and warms all three retrieval modes before reporting readiness; this is required when the Docker VHDX is hosted on slower external storage.

Benchmark v5 review workspace status:

```powershell
.\scripts\review_retrieval_benchmark.ps1 -Operation status
```

The next executable review step is reviewer A's real blind review:

```powershell
.\scripts\review_retrieval_benchmark.ps1 -Operation serve `
  -ReviewerSlot reviewer_a `
  -Listen 127.0.0.1:18083
```

Open `http://127.0.0.1:18083/`. Per-case progress is atomically saved to `evaluation/private/retrieval_v5/reviewer_a/submissions.in_progress.jsonl`; only the final confirmation creates immutable `submissions.jsonl`. Do not automate labels or call model output human evidence. Reviewer B must be a different real person, and the adjudicator must be a third identity.

Optional observability stack:

```powershell
docker compose -f docker-compose.yml `
  -f deploy/observability/docker-compose.observability.yml up -d --build

Invoke-RestMethod http://127.0.0.1:13133
Invoke-RestMethod http://127.0.0.1:13200/ready
```

## Verification

```powershell
cd backend-go
go test ./...
go vet ./...

go test ./internal/evalreview ./internal/evalbench ./cmd/benchmarkreview

cd ..
docker run --rm --mount "type=bind,source=$((Get-Location).Path),target=/workspace" `
  -w /workspace/backend-go golang:1.25.12-bookworm go test -race ./...

$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@127.0.0.1:15432/creatorinsight?sslmode=disable"
$env:NATS_URL = "nats://127.0.0.1:14222"
cd backend-go
go test -tags=integration -count=1 ./integration

$env:OTEL_TEST_ENDPOINT = "127.0.0.1:14317"
go test -run TestOTLPExporterSmoke -count=1 -v ./internal/platform/tracing

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

go run ./cmd/benchmarkaudit `
  --benchmark-root ../evaluation/benchmarks/retrieval_v4 `
  --output ../evaluation/results/retrieval_v4/development_benchmark_audit_v1.json

cd ..
.\scripts\evidence.ps1 -Operation audit `
  -RunId phase7a_dv2_rebuild_v2_20260718

.\scripts\smoke_phase7_retrieval.ps1 -Modes lexical,vector,hybrid

cd backend-go
go run ./cmd/vectorindex `
  --ingestion-run-id phase7a_dv2_rebuild_v2_20260718 `
  --audit-only

cd ..
.\scripts\qdrant_snapshot.ps1 `
  -Operation restore-drill `
  -Collection noteinsight_7aa574ea1bb52ae1591b4ad0d5969013

docker compose run --rm --no-deps `
  --entrypoint /app/noteinsight-reconcile worker --full
```

Latest verified data:

- quality run: `phase6c_quality_v2_20260715`, 200 notes, 40,000 comments, 1,619 eval cases;
- fact run: `phase6c_final_20260715`, 812 note facts, 481 user facts;
- independent benchmark: `retrieval_v4_20260716`, 80 public development cases + 160 sealed holdout cases, with 240 random-nonce commitments and eight task families;
- frozen retrieval dataset: version `2`, 113,921 source references, checksum `b91df11ca9136e000c759fd2c6de5b448816bb57d903849c478f99db8533eab5`;
- evidence run `phase7a_dv2_v1_20260718`: 1,283 fact inputs, 25,448 documents, 56,349 chunks, 153,348 citations and output checksum `3f372c59b8108bd95fb747e5d04aa73fe35ea6657f7219022ce047b07da3ee1a`;
- deterministic rebuild `phase7a_dv2_rebuild_v2_20260718`: all 25,448 documents reused, identical output checksum, zero audit violations and about 46 seconds of database run time;
- full vector index `qwen3_dense_cosine_v1`: 56,349 points in collection `noteinsight_7aa574ea1bb52ae1591b4ad0d5969013`, built in 1h1m37s with checksum `432221b4873b965b52444776d9e887bd79cc5ff3d1581abbf3157f88b5ae8627`;
- migration `000021` adds vector build lease/attempt/checkpoint/heartbeat/reconciliation state; real PostgreSQL tests reject concurrent and stale leases while preserving the last durable checkpoint;
- migration `000023` adds validated W3C `traceparent/tracestate` to Outbox rows; real PostgreSQL integration tests prove the context commits transactionally with the event and no row remains after rollback;
- completed-index audit compares all 56,349 frozen chunk ids/content hashes with all Qdrant point ids/payload hashes and reproduces checksum `432221b4873b965b52444776d9e887bd79cc5ff3d1581abbf3157f88b5ae8627` with zero missing/orphan/mismatched points;
- Qdrant restore drill snapshot: 310,594,560 bytes, SHA-256 `6400ff3cb682c872d3dc0a848f0e4795d7e9102456f91debb5da2d276c19c938`; an isolated temporary collection restored 56,349 points and was deleted after verification;
- formal lexical baseline: Recall@10 `0.6812`, MRR `0.6585`, citation integrity `1.0`, no-relevant rejection `1.0`;
- formal vector baseline: Recall@10 `0.2391`, MRR `0.2366`, citation integrity `1.0`, no-relevant rejection `0.9091`, P95 `548.66ms`;
- formal hybrid baseline: Recall@10 `0.5652`, MRR `0.5598`, citation integrity `1.0`, no-relevant rejection `0.9091`, P95 `2262.06ms`;
- all formal retrieval runs match dataset version `2` and fail the Recall/MRR gate; quality-subset results remain diagnostic only and the v4 holdout remains sealed;
- benchmark audit checksum `2e702eb90709b965467ffa79275189e95da6349ed4d4df425194fee40b616850`: 58 distinguishable Gold cases, 11 no-Gold cases, and 11 insufficient-evidence cases requiring independent review;
- all registry-backed citation source slices were compared on the main data set with zero mismatches;
- main database after Phase 7A-0 acceptance: 5,511 active notes, 6,813 media, 101,635 active comments and 113,927 active Evidence Sources;
- invariants: zero missing dataset source, counter drift, duplicate dataset, active Outbox, JetStream pending or redelivery;
- isolated PostgreSQL restore drill passed; Phase 7A-0 archive is `artifacts/backups/noteinsight_20260716_033326.dump` with SHA-256 `3424FEF52C080F432E6F230DB579EAE51379CC90C8F1C90CDCED89745F140E92` and a parseable 316-entry TOC;
- Phase 7A completion archive is `artifacts/backups/noteinsight_20260718_062530.dump` (97,180,232 bytes), SHA-256 `45F2E285534DE9D3E94BBE1949D11318B8D40CB04DB44F608840A5F36C973CB0`, with a parseable 434-entry TOC;
- delayed deleted-note view replay passed and DLQ did not grow.
- distributed trace `903741a95c1ed196dbcd3cbd1f00b86a` contains 37 API/Worker spans across an Outbox/NATS delivery for note `5548`; the persisted W3C parent is `00-903741a95c1ed196dbcd3cbd1f00b86a-74ba37ebb05005af-01` and the Outbox row reached `sent`;
- hybrid retrieval trace `5316cae36621eca92abd4481b4dfe69a` returned HTTP 200 and contains SQL, `tei POST` and `qdrant query` spans;
- benchmark v5 private matrix `retrieval_v5_matrix_v1` contains 288 deterministic slots, 32 for each of nine task families and 144 per split; checksum `7086266255375de7137d0f9502543769c22a53d20f2d280a928469c9183f3a61`;
- benchmark v5 model-assisted draft checksum is `da13ddd29c0f3cfbe2f8e7c4e133e431e09a218aff044cad78aa482c1051eedd`; 1,728 candidate references resolve through the frozen ingestion graph to 1,229 unique sources, with zero human-reviewed cases and no freeze claim;
- frontend browser smoke passed for search, deep-linked detail, structured media text, comments, ranking and runtime status with no console errors.
- Go statement coverage is 29.72% with a 25% CI floor; frontend statement coverage is 60.84% with four metric floors.
- Govulncheck reports zero reachable vulnerabilities; Trivy reports zero fixable HIGH/CRITICAL findings for the Go 1.26.5 scratch image; SPDX SBOM generation passed.
- Public GitHub remote: `https://github.com/Biubiubiu-ikun/NoteInsight-Agent-Platform`; CodeQL uploads to GitHub Code Scanning, while the private archive uses local-SARIF mode.

## Stop

```powershell
docker compose -f docker-compose.yml `
  -f deploy/observability/docker-compose.observability.yml down
```

Named PostgreSQL, NATS, Prometheus, Grafana and Tempo volumes are preserved unless `-v` is supplied.
