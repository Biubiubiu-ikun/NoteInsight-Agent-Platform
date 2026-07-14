# Project Progress and Quality Audit

Audit date: 2026-07-14.

## Planning Rule

`最新项目规划.md` is authoritative. Files beginning with `旧版项目规划` are historical
references only. The latest plan explicitly specifies the note/auth domain through
Phase 3. Later phases must preserve that domain when reusing historical engineering
ideas.

## Progress Matrix

| Stage | Status | Evidence |
| --- | --- | --- |
| Phase 1 Go skeleton | Complete | API layout, configuration, health/readiness, sqlx, migrations, Docker |
| Phase 2B note-domain pivot | Complete | note/media/comment/interaction schema and `/api/v1/notes` routes; danmu removed |
| Phase 2C auth closure | Complete | JWT, hashed refresh sessions, `/me`, ownership/admin/banned rules, automated smoke |
| Phase 3 cache/ranking/seedgen | Complete | Redis caches/ZSETs, metrics, deterministic dev seed, token-aware k6 report |
| Phase 4A Outbox/behavior | Complete | transactional Outbox, processed and behavior events |
| Phase 4B reliability | Complete | rate limits, lease recovery, source-of-truth reconcile |
| Phase 4C JetStream worker | Complete | standalone worker, durable consumer, retry, DLQ, broker recovery |
| Phase 4D async counters | Complete | transactional exact counter rebuild, event lag metrics, outage k6 |
| Phase 5A Behavior Simulator | Complete | deterministic personas, Markov sessions, time/burst distributions, streaming sinks, strict report |
| Phase 5B quality corpus | Complete | hidden scenarios, substantive note/OCR/comment text, ground truth, strict quality report |
| Phase 5C lifecycle/fact materialization | Not started | behavior sessions are not yet materialized into community fact tables |
| Phase 6 capacity testing | Partial | k6 exists; no long-duration capacity envelope or Locust session model |
| Platform/RAG/evaluation/cloud | Not started | deliberately deferred until the data and event substrate is stable |

## What Is Strong Today

- Request identity comes from a verified token, not client-provided IDs.
- Business facts and Outbox events commit atomically.
- JetStream publication uses broker acknowledgement and message-ID deduplication.
- Consumer database work is idempotent and transactionally applied.
- Derived counters rebuild from source facts and scheduled reconciliation repairs drift.
- PostgreSQL, Redis, and NATS failures have explicit readiness and recovery behavior.
- The repository includes deterministic seed data, authenticated load tests, and a
  repeatable NATS outage scenario.
- The simulator reproduces complete session datasets from a fixed seed and streams
  million-row workloads without requiring an LLM API.
- Both pressure-test and Agent-quality datasets now contain substantive Chinese text;
  hidden scenarios and gold source selectors make later retrieval measurable.
- GitHub Actions now covers formatting, race-enabled tests, vet, all command builds,
  simulator checks, Compose validation, idempotent migrations, and full API acceptance.

## Remaining Quality Gaps

### Priority 1

1. **Operational dashboards:** metrics are exposed, but Prometheus scrape config,
   Grafana dashboards, and alert rules for event lag, Outbox age, DLQ, error rates, and
   dependency readiness are absent.
2. **Phase 5C data lifecycle:** add user/note evolution, optional fact-table
   materialization, and scenario calibration while preserving run reproducibility.
3. **Version control remote:** reviewed baseline commit `c0428c4` exists locally, but a
   remote repository and branch protection still need to be configured.

### Priority 2

1. **Production security:** local credentials are intentionally simple. Production
   configuration still needs secret management, TLS, NATS authentication, private
   database/Redis networking, and explicit trusted-proxy/CORS policy.
2. **Event governance:** add event schema versions, compatibility tests, Outbox and
   processed-event retention jobs, and a controlled DLQ inspect/replay command.
3. **Test depth:** the automated PowerShell acceptance test closes the current API
   checklist, but repository and service integration coverage should move into Go tests
   that run in CI. Current statement coverage is concentrated in middleware and pure
   helpers: approximately `note=4.1%`, `auth=11.8%`, and `worker=26.7%`.
4. **Capacity evidence:** run longer steady, spike, soak, and dependency-outage tests;
   preserve machine specifications and database/Redis/NATS metrics with each report.

### Later, By Design

- OpenTelemetry traces and profiling
- Platform tool/plugin registry
- Evidence Store and ingestion pipeline
- hybrid retrieval, Qdrant, Agent runtime, and evaluation service
- deployment hardening and Kubernetes experiments

These should not jump ahead of Phase 5 behavior data and Phase 6 capacity evidence.

## Recommended Sequence

1. Phase 6: capacity, spike, soak, and failure testing using the new corpus and sessions.
2. Observability track: Prometheus/Grafana/alerts and DLQ replay/retention tooling.
3. Event governance: schema versions, compatibility tests, and retention policies.
4. Build note-domain Evidence Store and deterministic ingestion with index versioning.
5. Add lexical/vector retrieval and evaluation before beginning the Agent runtime.
