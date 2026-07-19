# ADR 0006: Agent Run Lineage and Citation Enforcement

- Status: Accepted for Phase 8A
- Date: 2026-07-19

## Context

An insight report can look plausible while using the wrong evidence snapshot, leaking project-scoped content, exceeding cost limits, or presenting unsupported claims. Application logs alone are insufficient because they are mutable, incomplete, and cannot enforce relationships between a report and canonical citations.

Benchmark v5 human review is still open, but the Agent control plane must not depend on benchmark answers or retrieval scores.

## Decision

1. Persist every Agent request before execution with requester, immutable dataset and ingestion lineage, index checksums, prompt checksum, hard budget, request ID and trace ID.
2. Keep the workflow single-Agent and bounded. The database status machine is `queued -> running -> succeeded|failed|cancelled`, with cancellation also allowed before dispatch.
3. Bind the model to an immutable provider/model/revision record exactly once when a queued run is dispatched.
4. Store every tool attempt, claim and claim-to-citation link as structured rows.
5. Require claim citations to reference canonical `source_citations` from the same project and dataset version and to preserve the canonical quote hash.
6. Make terminal runs and their lineage immutable.
7. Treat benchmark review as a promotion and quality-claim gate, not as a dependency for schema, authorization, cancellation or budget engineering.

## Consequences

- Phase 8B can be implemented without inspecting v5 labels or tuning retrieval.
- Replays, cost accounting, unsupported-claim audits and exact citation reconstruction become queryable.
- Model and prompt changes create new version rows instead of mutating history.
- The executor must complete claims and citations before transitioning a run to `succeeded`.
- Formal Agent quality remains unclaimed until benchmark v5 is independently reviewed and frozen.
