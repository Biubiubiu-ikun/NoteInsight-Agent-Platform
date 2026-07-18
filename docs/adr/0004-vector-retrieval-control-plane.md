# ADR 0004: PostgreSQL-Authoritative Vector Retrieval

Status: Accepted

Date: 2026-07-18

## Context

Phase 7 needs semantic retrieval without weakening the immutable evidence, authorization, deletion, and citation guarantees established in Phase 7A. Qdrant is an eventually synchronized serving index and cannot become the authority for project membership, document lifecycle, or source deletion. Embedding model drift would also make an index impossible to reproduce unless the model artifact and vector contract are immutable inputs.

## Decision

1. PostgreSQL remains the control plane and source of truth. The service resolves the frozen ingestion scope and project access before any embedding or Qdrant call.
2. Qdrant applies project, visibility, document type, source type, lifecycle, note, and media-position filters before vector scoring. PostgreSQL then re-authorizes every returned chunk and rejects stale or deleted sources before the result can be returned.
3. Each ingestion run receives an isolated, deterministically named Qdrant collection. Completed vector index rows are immutable and include the ingestion run, index version, model id, exact model revision, dimension, distance metric, point count, and manifest checksum.
4. The initial dense contract is Qdrant `v1.18.2`, Hugging Face TEI `turing-1.9`, and `Qwen/Qwen3-Embedding-0.6B` revision `97b0c614be4d77ee51c0cef4e5f07c00f9eb65b3`, with 1,024-dimensional cosine vectors and a retrieval query instruction.
5. Index completion requires exact equality between frozen PostgreSQL chunk ids/content hashes and Qdrant point ids/`content_hash` payloads plus a deterministic manifest checksum. PostgreSQL owns a short build lease and a checkpoint after each acknowledged Qdrant batch. A failed build resumes from that checkpoint; missing or stale checkpoint points rewind to the earliest gap, stale points are re-embedded, and point ids absent from the frozen manifest are eligible for orphan deletion.
6. `lexical`, `vector`, and `hybrid` are explicit request modes. A dependency failure returns an error rather than silently changing retrieval semantics.
7. Qdrant and TEI are an opt-in Compose profile. Lexical retrieval remains available without GPU dependencies.

## Consequences

- A stale Qdrant payload cannot bypass current PostgreSQL deletion or authorization rules.
- Indexes are reproducible and comparable, but storage grows per immutable ingestion run. Snapshot pruning keeps at least the configured recent count; collection deletion remains an explicit lineage decision.
- Interrupted builds resume without re-embedding hash-identical durable batches. Resume and completion inspect exact point ids and content hashes, so equal counts cannot hide a missing expected point, stale content, or an unrelated orphan.
- The locally pinned GPU profile is suitable for development evidence, not a cloud capacity claim. A local collection snapshot/restore drill is recovery evidence, but production still requires managed backups, API keys, TLS/private networking, secret management, and independent load tests.
