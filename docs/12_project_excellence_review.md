# Project Excellence Review

Review date: 2026-07-18

## Interviewer Assessment

NoteInsight is now a strong senior backend/data/AI-platform project rather than a CRUD or framework-integration demo. It demonstrates trustworthy identity, transactional events, repairable derived state, deterministic corpus generation, immutable evidence, exact citation lineage, authorization-aware retrieval, reproducible experiments, failure classification, and operational recovery.

The strongest signal is engineering judgment. PostgreSQL lexical retrieval was implemented and measured before a vector stack was selected. Its formal Recall@10 of 0.6812 and MRR of 0.6585 were retained as a failed gate, not hidden. A pinned Qdrant/TEI/Qwen stack was then added behind the same evidence and authorization contracts. On the full same-contract corpus, dense-only reached Recall@10 0.2391/MRR 0.2366 and hybrid reached 0.5652/0.5598; both failures were retained. That is a credible experiment story, not a technology checklist.

The project is not yet a finished Agent product or production cloud service. Benchmark adjudication, production-like multi-instance capacity and cloud security/operations remain important boundaries.

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
11. Vector indexing now has a PostgreSQL lease, per-batch checkpoints, exact point-id/content-hash reconciliation, crash resume, stale/orphan repair, immutable completion audit, and a real isolated Qdrant snapshot restore drill.
12. Distributed tracing preserves W3C context through a durable Outbox and NATS delay, correlating API, SQL, Redis and Worker spans; retrieval traces also expose TEI and Qdrant client latency without recording content, credentials or SQL statements.
13. Local load evidence states both passes and saturation boundaries, including a 30-minute soak and concurrent indexing, instead of extrapolating a production capacity claim from a laptop.

## Phase 8 Entry Review

### P0: Complete Before Making a Retrieval Quality Claim

1. Freeze benchmark v5 before further ranker tuning. It must stratify every task across development/holdout, add multi-Gold relevance pools for equivalent-topic notes, independently adjudicate no-answer and insufficient-evidence cases, and cover gibberish and out-of-domain dense false positives.
2. Include authorization-boundary cases in public development so non-leakage is continuously measurable rather than appearing only in integration tests or sealed cases.
3. Keep the v4 holdout sealed. Do not use it to resolve the 0.60 hybrid threshold margin; use a newly authored v5 development set instead.

### P1: Complete Before Production Claims

1. Deploy TEI/Qdrant/NATS/Redis/PostgreSQL and the trace backend behind private networking, authenticated TLS endpoints and managed secrets; prove PostgreSQL PITR and managed backup restore.
2. Run the already-defined vector/hybrid workload against multiple API instances, production-like CPU/GPU quotas and an external load generator. Preserve p50/p95/p99, saturation, error-budget and recovery evidence.
3. Define production trace sampling, retention, redaction, access-control and cost policies. Local 100-percent sampling and unauthenticated Tempo are diagnostic only.
4. Establish environment promotion, signed-image verification and rollback evidence before a production-ready claim.

### P2: Product and Team Scale

1. Add a retrieval/evaluation console with evidence drawers, query mode comparison, failed-case inspection, and run/checksum lineage.
2. Add online feedback and retrieval-drift monitoring without using clicks as unreviewed relevance truth.
3. Define cloud environments, PITR, environment promotion, signed images, model artifact provenance, and cost budgets.
4. Record benchmark reviewer identity, rubric, disagreement, adjudication, and inter-annotator agreement.
5. Replace broad Redis ranking-key scans during note invalidation with an explicit bounded index or versioned-key strategy if production cardinality shows measurable scan cost; tracing currently suppresses repetitive `SCAN` spans but does not remove the operation itself.

## Recommended Sequence

1. Phase 7D-1: execute the documented two-reviewer benchmark v5 protocol and freeze it before any further ranker tuning.
2. Phase 7D-2: run lexical, dense and RRF unchanged on frozen v5; only then compare an optional pinned cross-encoder and promote a measured winner.
3. Phase 7D-3: freeze the winning retrieval contract and preserve the completed recovery, load, fault and distributed-tracing evidence as regression gates.
4. Phase 8A: define Agent run, tool-call, claim, evidence, prompt, model, budget and trace schemas.
5. Phase 8B: implement one bounded insight workflow: intent -> retrieval plan -> evidence -> analysis -> citation validation -> structured report.
6. Phase 8C: add Agent evaluation for claim support, citation coverage, abstention, tool errors, latency and cost. Do not start with multi-agent orchestration.
7. In parallel with Phase 8 engineering, prove cloud security, PITR, multi-instance capacity and environment promotion; these gates block production claims, not local Agent implementation.
8. Phase 9: productize report/evidence UI and complete cloud operations.

## Hiring Signal

For a large-company interview, this project is already credible for senior backend/platform/data-engineering discussion and increasingly credible for applied AI infrastructure. The best presentation is not “I used JWT, Redis, NATS, Qdrant, and an LLM.” It is: “I designed authorities and immutable contracts, built a reproducible baseline, measured where it failed, added the smallest justified component, preserved security through the derived index, and kept benchmark limitations visible.”

Completing Phase 7D and one citation-enforced Agent workflow would make the project unusually complete for an individual portfolio. Production readiness should still be stated narrowly until cloud security, recovery, capacity, and benchmark review are proven.
