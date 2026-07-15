# Project Progress And Quality Audit

Audit date: 2026-07-15

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
| Release hardening | Complete locally; remote record pending | frozen independent benchmark, integration/E2E depth, contracts, SBOM and vulnerability gates |
| Frontend console | MVP complete | feed, search, ranking, auth, publish, detail, comments, interactions and status |
| Phase 7A | Next | canonical evidence documents/chunks and deterministic ingestion |

## Verified Snapshot

- Eleven checksum-protected migrations apply idempotently.
- Main data after acceptance: more than 5.4K notes, 101K comments and 113K Evidence Sources.
- Every current note/media/comment has a 64-character source hash and dataset boundary.
- Quality corpus run `phase6c_quality_v2_20260715` produced 1,619 cases across nine task types; all strict checks passed.
- Independent benchmark `retrieval_v3_20260715` contains 240 unique cases with an 80/160 development/holdout split; manifest checksum is `cb1494b76b38a23e0e20190614c104e1e7e22baa35bbb771cc340236335a3d35`.
- Fact run `phase6c_final_20260715` materialized 812 note facts and 481 user facts.
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
- Disposable-database integration tests cover refresh replay/concurrency, unique interactions, transaction rollback, Outbox lease recovery and benchmark immutability; live NATS covers DLQ and replay.
- Frontend coverage floors and committed Playwright desktop/mobile E2E now protect the real product workflow.
- Phase 6C is preserved by commit `f0dee23` and annotated tag `v0.6.4`; subsequent hardening is a separate change.
- Product gaps such as unlike/uncollect, viewer state, author projection, deep links, server-side search and ranking N+1 were closed.

## Open Production Gates

- The strict 30-minute warm mixed-load SLO has not yet passed on the large data set.
- Full OpenTelemetry export, `pg_stat_statements`, multi-instance tests and external load generation remain.
- Managed secrets, TLS, service authentication, private networking, image signing/registry policy and PostgreSQL PITR require a deployment environment.
- A stratified independent human review of the holdout set is still needed before public quality claims.
- Git remote Actions evidence, protected branches and environment promotion require the code-hosting platform.

These items do not block deterministic Evidence Store work, but they do block a production-ready claim.
