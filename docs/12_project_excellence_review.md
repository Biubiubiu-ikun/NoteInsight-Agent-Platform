# Project Excellence Review

Review date: 2026-07-18

## Interviewer Assessment

At this checkpoint, NoteInsight is a strong senior-level platform project rather than a CRUD demo. It demonstrates domain migration, trustworthy identity, transactional events, idempotent asynchronous processing, repairable derived state, deterministic data engineering, measurable performance, recovery tooling, governance boundaries and a usable frontend console.

The strongest interview signal is that failures are retained and corrected: migration drift is rejected, latency gates remain visible, late events were found through the DLQ and replayed after a semantic fix, and backup claims are backed by an isolated restore.

The project is not yet a finished Agent product. Versioned evidence ingestion now exists; measured retrieval and citation-grounded reports remain before the name is fully justified.

## What Is Already Strong

1. Identity comes from verified JWT context and refresh sessions rotate safely.
2. Business facts and Outbox events commit atomically; consumers are idempotent.
3. Project, dataset, visibility, source version and deletion boundaries exist before retrieval; immutable dataset snapshots pin experiments to exact source hashes.
4. Redis/NATS are treated as derivatives or transport, never the source of truth.
5. Data sets are deterministic and meaningful; v4 independently authors eight task families, separates abstention semantics, binds a snapshot and seals holdout identities with random nonces.
6. Operational claims have metrics, alerts, replay, maintenance, backup and restore evidence.
7. The frontend exercises real workflows instead of presenting a marketing shell.
8. CI covers backend, frontend, real PostgreSQL/NATS integration, Playwright, contracts, Prometheus, secrets, SBOM, image vulnerabilities, Compose and authenticated acceptance.
9. Canonical evidence is reproducible: immutable run inputs, deterministic document/chunk keys, exact UTF-8 citation ranges, deletion propagation and checksum-identical rebuilds are tested on the full frozen corpus.

## Remaining High-Value Work

### Before Retrieval Quality Claims

- Establish PostgreSQL FTS lexical baseline before adding embeddings.
- Report per-task Recall/MRR/nDCG, no-answer and citation metrics, not one aggregate score.
- Extend the existing consistency audit through the retrieval API and authorization principals, proving filters execute before scoring.
- Have an independent reviewer adjudicate a stratified holdout sample and record agreement before making public benchmark claims.
- Keep authorization-boundary cases sealed and execute them with both authorized and unauthorized principals once the retrieval API exists.

### Before Production Claims

- Pass a 30-minute warm large-data mixed soak and explain P95/P99 with traces and query evidence.
- Add real OpenTelemetry export and `pg_stat_statements`/slow-query diagnostics.
- Prove multi-instance behavior, external load, managed secrets, TLS, service auth and PITR.
- Split production runtime/tooling images if registry size and least-functionality policy justify the operational complexity.
- Configure protected branches, required green checks, releases, registry signing and environment promotion on the Git host.

## Next Sequence

1. Phase 7B PostgreSQL lexical retrieval and offline evaluation.
2. Add vector/hybrid retrieval only after baseline failures are classified.
3. Phase 8 single grounded Agent with structured claims and citations.
4. Phase 9 report/evidence UI and deployment hardening.

From a large-company interview perspective, the project is already credible for backend/platform/data-engineering discussion. Phase 7B and Phase 8 will supply the missing AI-system evaluation story.
