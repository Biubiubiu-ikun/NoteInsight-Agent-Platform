# Project Excellence Review

Review date: 2026-07-15

## Executive Assessment

The project is now a strong community-backend and data-engineering foundation, not yet
a complete Agent platform. Identity, note-domain facts, asynchronous processing,
deterministic corpora, failure recovery, and capacity evidence are unusually solid for
this stage. The product promised by the project name still lacks its Evidence Store,
retrieval service, Agent runtime, evaluation loop, operator dashboards, and user-facing
application.

In practical maturity terms:

- **Backend/domain foundation:** strong prototype approaching pre-production quality.
- **Data and evaluation preparation:** strong deterministic foundation; lifecycle facts
  and retrieval ingestion remain incomplete.
- **Operational production readiness:** partial; metrics and recovery exist, but no
  dashboards, alerts, tracing, backup drill, or hardened secrets/networking.
- **RAG/Agent product:** not started beyond corpus and ground-truth preparation.
- **End-user product:** not started; there is no frontend or report workflow.

`最新项目规划.md` remains authoritative. The sequence below is an engineering proposal
for work beyond the phases explicitly specified there; historical plans remain reference
material only.

## Progress Inventory

| Area | Current state | Evidence quality |
| --- | --- | --- |
| Phase 1 skeleton | Complete | automated build, Compose, health/readiness, migration |
| Phase 2B notes domain | Complete | sqlx schema/repository/service/handlers and smoke |
| Phase 2C auth | Complete | JWT, refresh hash, ownership, banned rules, acceptance |
| Phase 3 cache/ranking | Complete | Redis caches/ZSETs, metrics, seed token pool, k6 |
| Phase 4 event reliability | Complete | transactional Outbox, JetStream, idempotency, DLQ, outage recovery |
| Phase 5A behavior simulator | Complete | deterministic scale generation and strict reports |
| Phase 5B quality corpus | Complete | substantive Chinese text, hidden scenarios, 1,000 gold cases |
| Phase 5C lifecycle facts | Not started | simulator runs are not materialized into evolving community facts |
| Phase 6A capacity | Complete | baseline/step/spike/hotspot matrix and resource snapshots |
| Phase 6B large scale | Implemented; gate open | 4.21M rows and 10-minute soak; mixed SLO still fails |
| Evidence Store/RAG | Not started | source text and gold selectors are ready inputs only |
| Agent/evaluation product | Not started | no tool runtime, report contract, citations, or eval service |
| Frontend/operator UI | Not started | API-only project today |

## What Already Deserves To Stay

1. **Planning precedence is explicit.** Domain drift back to video/danmu is prevented.
2. **Identity is trustworthy.** Writes derive user identity from verified JWT context.
3. **Facts are durable.** Business rows and Outbox events commit in one transaction.
4. **Async work is idempotent.** Broker message IDs and processed-event facts cover both
   publication and consumption duplicates.
5. **Derived state is repairable.** Counters and rankings have a source-of-truth path,
   and failures are visible through readiness and metrics.
6. **Generated data is useful.** Scale rows remain deterministic and bounded-memory;
   quality rows have meaningful text, hidden scenarios, and resolvable gold tasks.
7. **Performance claims retain failures.** Raw evidence includes failed thresholds,
   resource peaks, dropped work, and pipeline state instead of only a headline QPS.
8. **CI covers real behavior.** Race tests, vet, all command builds, Compose validation,
   idempotent migrations, authenticated API acceptance, and async convergence run in CI.

## Priority 0: Close Before Production Claims

### 1. Large-Data Tail Latency

The 4.21M-row read-only list path passes 30 RPS, but mixed 30 RPS and the 10-minute
10 RPS soak fail strict endpoint P95 targets. Add:

- OpenTelemetry spans across HTTP, SQL, Redis, Outbox publish, and consumer apply;
- PostgreSQL statement statistics and slow-query capture;
- database pool acquire/wait/usage metrics;
- separate cold-start and warm steady-state SLOs;
- a repeatable 30-minute warm soak after fixes.

The hourly reconciler currently performs whole-table aggregates across million-row fact
tables. Replace routine full scans with incremental/checkpointed or partitioned repair.
Keep a full audit as an explicit maintenance command, not normal startup competition.

### 2. Production Security Baseline

Local defaults are intentionally simple. Production readiness still requires:

- managed secrets and rotation for JWT, database, Redis, and NATS credentials;
- TLS at ingress and authenticated/private internal services;
- explicit CORS and trusted-proxy configuration;
- access/audit logs for admin mutations and token-session management;
- dependency and container vulnerability scanning;
- abuse controls beyond fixed-window user limits, including IP/device risk and signup
  throttling where appropriate.

### 3. Backups and Recovery

There is no documented PostgreSQL backup/PITR policy, Redis loss model, NATS stream
retention policy, or restore drill. Define RPO/RTO, automate backups, and prove a restore
into an isolated environment before treating the data plane as production-ready.

### 4. Integration-Test Depth

The PowerShell acceptance suite is valuable, but repository and service integration
coverage remains thin. Add container-backed Go tests for:

- keyset pagination under concurrent inserts/deletes;
- transaction rollback and uniqueness races;
- JWT refresh/logout/session revocation;
- Outbox lease recovery, publish retry, consumer redelivery, and DLQ replay;
- reconciliation/incremental repair on large fact sets;
- gzip negotiation and cache behavior through the real router.

Use risk-based coverage gates rather than chasing one global percentage.

## Priority 1: Make It Operable and Governed

1. **Observability assets:** add Prometheus scrape config, Grafana dashboards, recording
   rules, and alerts for HTTP SLOs, DB pool waits, Outbox age, event lag, JetStream
   pending/redelivery, DLQ growth, reconcile duration, and dependency readiness.
2. **Event contracts:** version envelopes, publish JSON schemas, add compatibility tests,
   define retention, and provide controlled inspect/replay tooling for the DLQ.
3. **API contracts:** publish OpenAPI, request/response examples, error codes, pagination
   semantics, and generated client smoke tests. Decide whether list APIs return full
   bodies or explicit summaries/excerpts.
4. **Data governance:** define deletion propagation, retention, PII boundaries, content
   moderation, provenance, consent, and how deleted/banned content is removed from future
   search indexes and generated reports.
5. **Release discipline:** configure a Git remote, protected main branch, pull-request
   checks, release tags, changelog, dependency updates, and environment promotion.

## Priority 2: Build the Actual Insight Product

### Phase 5C: Lifecycle and Fact Materialization

Materialize selected simulator sessions into users, notes, comments, and interaction
facts with reproducible lifecycle evolution. Keep simulation-run lineage so every later
retrieval or evaluation result can name its source dataset and seed.

### Phase 7A: Evidence Store and Ingestion

Create versioned evidence records for:

- note title/body sections;
- media caption/OCR blocks;
- representative comments and comment clusters;
- behavior summaries and trend windows.

Each chunk needs source IDs, offsets/selectors, content hash, dataset/run ID, parser
version, timestamps, visibility/deletion state, and index version. Ingestion must be
idempotent and re-runnable before introducing embeddings.

### Phase 7B: Retrieval and Evaluation

Start with PostgreSQL full-text/BM25-style lexical retrieval and the existing 1,000 gold
cases. Then add vector retrieval and hybrid fusion only when lexical baselines are
measured. Required metrics include Recall@K, MRR/nDCG where appropriate, citation/source
resolution, latency, and cost. Qdrant is an implementation option, not the milestone.

### Phase 8: Agent and Report Runtime

Build a small, explicit tool set over retrieval and analytics. Reports should return
structured claims, evidence citations, uncertainty, and reproducible query/session IDs.
Add prompt/model versioning, budget limits, timeout/cancellation, safety filters, and
offline evaluation before open-ended multi-agent orchestration.

### Phase 9: User and Operator Experience

Add the actual creator-insight workflow: project/dataset selection, question/report
history, evidence inspection, citation navigation, evaluation feedback, and operator
views for ingestion/event health. A polished API alone is not the finished product.

## Quality Gates for a Very Good Project

| Gate | Required evidence |
| --- | --- |
| Correctness | automated unit/integration/acceptance tests; uniqueness and auth races covered |
| Reliability | broker/DB/Redis outage recovery; no lost facts; DLQ replay proven |
| Performance | cold and warm SLOs; 30-minute soak; no drops/errors/backlog; resource headroom |
| Observability | dashboards, alerts, traces, runbooks, and useful correlation IDs |
| Security | secrets/TLS/network controls, auditability, scans, abuse and deletion policy |
| Recovery | successful isolated restore drill against declared RPO/RTO |
| Retrieval | versioned ingestion; gold-set recall and citation-resolution thresholds |
| Agent quality | grounded claims, evidence completeness, latency/cost and regression evals |
| Product | usable creator workflow plus operator controls, not only raw endpoints |

## Recommended Next Sequence

1. Phase 6C: tracing, DB/pool diagnostics, incremental reconciliation, and a passing
   30-minute warm mixed soak.
2. Operations baseline: dashboards/alerts, backup restore drill, DLQ replay/retention,
   event schemas, and OpenAPI.
3. Phase 5C lifecycle fact materialization with run lineage.
4. Phase 7A versioned Evidence Store and deterministic ingestion.
5. Phase 7B lexical retrieval baseline, hybrid retrieval, and gold-set evaluation.
6. Phase 8 grounded Agent/report runtime.
7. Phase 9 frontend/operator experience and hardened deployment.
