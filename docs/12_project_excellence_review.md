# Project Excellence Review

Review date: 2026-07-18

## Interviewer Assessment

NoteInsight is now a strong senior backend/data/AI-platform project rather than a CRUD or framework-integration demo. It demonstrates trustworthy identity, transactional events, repairable derived state, deterministic corpus generation, immutable evidence, exact citation lineage, authorization-aware retrieval, reproducible experiments, failure classification, and operational recovery.

The strongest signal is engineering judgment. PostgreSQL lexical retrieval was implemented and measured before a vector stack was selected. Its formal Recall@10 of 0.6812 and MRR of 0.6585 were retained as a failed gate, not hidden. A pinned Qdrant/TEI/Qwen stack was then added behind the same evidence and authorization contracts. On the full same-contract corpus, dense-only reached Recall@10 0.2391/MRR 0.2366 and hybrid reached 0.5652/0.5598; both failures were retained. That is a credible experiment story, not a technology checklist.

The project is not yet a finished Agent product or production cloud service. Benchmark adjudication, vector-index recovery, and production dependency operations remain important boundaries.

## What Is Already Strong

1. Identity comes from verified JWT context; refresh sessions rotate and detect token reuse.
2. Business writes and Outbox events commit atomically; JetStream consumers are idempotent and repairable.
3. Project, dataset, visibility, version, and deletion boundaries exist before retrieval. Frozen snapshots pin every experiment to exact source hashes.
4. Redis, NATS, lexical indexes, and Qdrant are derivatives or transport. PostgreSQL remains the authority.
5. Evidence ingestion is deterministic across 25,448 documents, 56,349 chunks, and 153,348 exact UTF-8 citations.
6. Retrieval resolves access before index readiness or model calls, pre-filters Qdrant, then post-authorizes every candidate in PostgreSQL.
7. Index and evaluation lineage includes ingestion checksums, lexical/vector checksums, model revision, retriever/reranker/metric versions, config checksum, and per-case failures.
8. Citation integrity and Gold-source relevance are reported separately, preventing a misleading single “citation quality” score.
9. The benchmark has a sealed holdout, nonce commitments, a public development split, and a deterministic distinguishability audit. Known labeling limitations are documented rather than tuned away.
10. CI covers Go race/coverage/vet/vulnerability checks, real PostgreSQL/NATS integration, frontend unit/E2E, OpenAPI and route drift, Prometheus, secrets, SBOM, image scanning, Compose, and authenticated acceptance.

## Phase 8 Entry Review

### P0: Complete Before Making a Retrieval Quality Claim

1. Freeze benchmark v5 before further ranker tuning. It must stratify every task across development/holdout, add multi-Gold relevance pools for equivalent-topic notes, independently adjudicate no-answer and insufficient-evidence cases, and cover gibberish and out-of-domain dense false positives.
2. Include authorization-boundary cases in public development so non-leakage is continuously measurable rather than appearing only in integration tests or sealed cases.
3. Keep the v4 holdout sealed. Do not use it to resolve the 0.60 hybrid threshold margin; use a newly authored v5 development set instead.

### P1: Complete Before Production Claims

1. Add resumable vector indexing with database checkpoints, exact collection/checkpoint reconciliation, crash tests, orphan cleanup, immutable completion, and retention policy.
2. Add Qdrant snapshots and restore drills, TEI/Qdrant readiness and saturation alerts, API-key/TLS/private-network configuration, and managed secrets.
3. Run vector/hybrid load tests with realistic concurrent queries and index builds. Record p50/p95/p99, GPU memory, queue saturation, Qdrant latency, PostgreSQL post-filter cost, and failure behavior.
4. Evaluate a pinned cross-encoder reranker only after v5 is frozen. Compare against lexical and RRF with per-task deltas and latency/cost budgets.
5. Add real OpenTelemetry export and `pg_stat_statements` evidence, then close the existing 30-minute warm mixed-soak gate.

### P2: Product and Team Scale

1. Add a retrieval/evaluation console with evidence drawers, query mode comparison, failed-case inspection, and run/checksum lineage.
2. Add online feedback and retrieval-drift monitoring without using clicks as unreviewed relevance truth.
3. Define cloud environments, PITR, environment promotion, signed images, model artifact provenance, and cost budgets.
4. Record benchmark reviewer identity, rubric, disagreement, adjudication, and inter-annotator agreement.

## Recommended Sequence

1. Phase 7D-1: freeze and independently review benchmark v5 before any further ranker tuning.
2. Phase 7D-2: add resumable/reconciled vector indexing, dependency observability, load and failure tests.
3. Phase 7D-3: compare lexical, dense, RRF and an optional pinned cross-encoder on v5; promote only a measured winner.
4. Phase 8A: define Agent run, tool-call, claim, evidence, prompt, model, budget, and trace schemas.
5. Phase 8B: implement one bounded insight workflow: intent -> retrieval plan -> evidence -> analysis -> citation validation -> structured report.
6. Phase 8C: add Agent evaluation for claim support, citation coverage, abstention, tool errors, latency, and cost. Do not start with multi-agent orchestration.
7. Phase 9: productize report/evidence UI and prove cloud operations.

## Hiring Signal

For a large-company interview, this project is already credible for senior backend/platform/data-engineering discussion and increasingly credible for applied AI infrastructure. The best presentation is not “I used JWT, Redis, NATS, Qdrant, and an LLM.” It is: “I designed authorities and immutable contracts, built a reproducible baseline, measured where it failed, added the smallest justified component, preserved security through the derived index, and kept benchmark limitations visible.”

Completing Phase 7D and one citation-enforced Agent workflow would make the project unusually complete for an individual portfolio. Production readiness should still be stated narrowly until cloud security, recovery, capacity, and benchmark review are proven.
