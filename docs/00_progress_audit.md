# Project Progress And Quality Audit

Audit date: 2026-07-18

## Planning Rule

`最新项目规划.md` is authoritative. Files with old-version prefixes are historical references only.

## Progress Matrix

| Stage | Status | Evidence |
| --- | --- | --- |
| Phase 1-3 | Complete | Go/sqlx note community, auth, Redis cache/ranking, metrics and k6 |
| Phase 4A-4D | Complete | Outbox, JetStream, idempotent worker, DLQ, outage recovery, reconciliation |
| Phase 5A-5B | Complete | deterministic behavior data and substantive Chinese RAG corpus |
| Phase 5C | Complete | auditable note/user daily facts with materialization run lineage |
| Phase 6A | Complete | capacity matrix, cold/warm/hotspot/spike evidence |
| Phase 6B | Implemented; long-soak gate open | 4.21M-row isolated data and 10-minute mixed soak |
| Phase 6C | Complete | project/dataset boundaries, Evidence Source registry, auth/event/ops/test hardening |
| Release hardening | Complete; remote CI enabled | frozen independent benchmark, integration/E2E depth, contracts, SBOM and vulnerability gates |
| Frontend console | MVP complete | feed, search, ranking, auth, publish, detail, comments, interactions and status |
| Phase 7A-0 | Complete | immutable source history, dataset snapshots, v3 retirement, sealed v4 benchmark and retrieval ADRs |
| Phase 7A | Complete | canonical documents/chunks, exact citations, fact versions, retry/rebuild/deletion audit |
| Phase 7B | Complete | authorization-filtered PostgreSQL lexical retrieval, exact citations and guarded offline evaluation |
| Phase 7C | Engineering baseline complete; quality gate failed | pinned Qdrant/TEI/Qwen dense retrieval, RRF hybrid and same-contract formal results |
| Phase 7D | In progress | vector recovery/snapshot and local dependency/load/fault/30-minute soak evidence complete; human benchmark and deployment gates open |

## Verified Snapshot

- Twenty-one checksum-protected migrations apply idempotently.
- Main data after final Phase 7A-0 acceptance: 5,511 active notes, 6,813 media, 101,635 active comments and 113,927 active Evidence Sources.
- Every current note/media/comment has a 64-character source hash and dataset boundary.
- All 113,927 active sources and every frozen source reference resolve to immutable canonical text/payload; 114,005 current or historical payload rows have valid SHA-256 values.
- Quality corpus run `phase6c_quality_v2_20260715` produced 1,619 cases across nine task types; all strict checks passed.
- Dataset version `2` freezes 113,921 logical source references at checksum `b91df11ca9136e000c759fd2c6de5b448816bb57d903849c478f99db8533eab5`.
- Independent benchmark `retrieval_v4_20260716` contains 240 unique cases with an 80/160 development/holdout split and eight balanced task families. Random nonce commitments seal private holdout identities; manifest checksum is `851a0ae94df77291d72904185754a2bea65893826fa942d52961472b65ab1b74`.
- `retrieval_v3` is retired after proving that deterministic public inputs reconstruct all 240 commitments.
- Fact run `phase6c_final_20260715` materialized 812 note facts and 481 user facts.
- Phase 7A ingested dataset version `2` into 25,448 canonical documents, 56,349 chunks and 153,348 citations. A full rebuild reused every document and reproduced output checksum `3f372c59b8108bd95fb747e5d04aa73fe35ea6657f7219022ce047b07da3ee1a`.
- The immutable Qdrant index has 56,349 points and checksum `432221b4873b965b52444776d9e887bd79cc5ff3d1581abbf3157f88b5ae8627`; exact point-id/content-hash audit reports zero missing/orphan/mismatched points.
- A 310,594,560-byte Qdrant snapshot with SHA-256 `6400ff3cb682c872d3dc0a848f0e4795d7e9102456f91debb5da2d276c19c938` restored all 56,349 points into an isolated collection before teardown.
- Lexical v3 preserves v2 Recall/MRR/nDCG and citation/rejection metrics while reducing formal P95 from 2,831.99 ms to 1,404.09 ms; local mixed 2 RPS passes, while 3 RPS establishes the current single-instance saturation boundary.
- Qdrant and TEI restart gates recover under mixed 2 RPS. Concurrent batch-8 indexing plus mixed 1 RPS passes with durable checkpoint progress; 2 RPS remains above the strict shared-resource error budget.
- A strict 30-minute warm mixed 2 RPS soak completed 3,601 iterations with a 0.6387 percent timeout rate, zero dropped iterations/rate limits/invalid citations, and lexical/vector/hybrid P95 of 3,192.54/616.51/3,098.76 ms.
- Ingestion audits and a full registry-backed citation byte-slice comparison report zero mismatches or active deleted-source leaks.
- Auth/API/async/Evidence Source acceptance passes end to end, including private-project isolation and deletion propagation.
- A 12.8 MB PostgreSQL custom dump was parsed and restored into an isolated database; restored counts matched before teardown.
- Delayed `note.viewed` events for already deleted notes now settle as behavior facts without new DLQ entries; two historical messages were replayed successfully.

## Closed P0/P1 Gaps

- Incremental checkpointed reconcile replaces routine whole-table repair.
- PostgreSQL pool timeouts are distinct; pool utilization and wait metrics are exposed.
- JWT/refresh rotation, HttpOnly cookie storage, auth IP limits, explicit CORS/trusted proxies and audit mutation logs are present.
- Migration lock/checksum, event schema/correlation, retention command, DLQ inspect/replay, backup/restore and governance runbook are present.
- Prometheus rules and a provisioned Grafana dashboard cover the critical data path.
- OpenAPI schema/reference plus Gin route drift, Promtool, Actionlint, Gitleaks, CodeQL, pinned govulncheck, npm audit, SBOM, Trivy and Dependabot are in CI.
- Disposable-database integration tests cover refresh replay/concurrency, unique interactions, transaction rollback, Outbox lease recovery, immutable evidence/dataset versions and frozen/retired benchmark rows; live NATS covers DLQ and replay.
- Frontend coverage floors and committed Playwright desktop/mobile E2E now protect the real product workflow.
- Phase 6C is preserved by commit `f0dee23` and annotated tag `v0.6.4`; subsequent hardening is a separate change.
- A private archive preserves the pre-public history and complete benchmark. The sanitized public GitHub remote preserves `main` through `v0.7.2`; Actions exercises the Linux release chain.
- CodeQL uploads Go and JavaScript/TypeScript results to GitHub Code Scanning; local SARIF mode remains available for private mirrors.
- Product gaps such as unlike/uncollect, viewer state, author projection, deep links, server-side search and ranking N+1 were closed.

## Open Production Gates

- Full OpenTelemetry export, multi-instance tests and external load generation remain. `pg_stat_statements`, query IDs, I/O timing and slow-query logging are enabled locally.
- Managed secrets, TLS, service authentication, private networking, image signing/registry policy and PostgreSQL PITR require a deployment environment.
- Benchmark v5 still needs independent human review, multi-Gold relevance pools, task-stratified splits, authorization cases and OOD/no-answer adjudication before public quality claims.
- CODEOWNERS, a PR evidence template, security policy, protected `main`, required status checks and review rules are configured. Environment promotion still requires a deployment environment.

These items do not block continued Phase 7D engineering, but they block Phase 8 retrieval-quality and production-ready claims.
