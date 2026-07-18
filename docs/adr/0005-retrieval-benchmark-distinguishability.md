# ADR 0005: Retrieval Benchmark Gold Distinguishability

Status: Accepted

Date: 2026-07-18

## Context

The frozen `retrieval_v4` benchmark protects development/holdout separation and binds every case to `dataset_version_id=2`. Its corpus intentionally contains multiple notes about the same subject. An exact single-note Gold label is valid only when the query contains evidence that distinguishes that note from equivalent-topic alternatives.

The public development split revealed two limitations:

- all 69 Gold-bearing development cases belong to repeated-topic cohorts of 7 to 13 notes;
- 11 `insufficient_evidence` cases contain no exact scenario anchor unique to the selected Gold note, while all 58 other Gold-bearing cases do;
- the 80-case development split contains no `authorization_boundary` case, so retrieval authorization quality cannot be tuned from this split.

Changing the frozen benchmark after observing retrieval results would invalidate its commitments and encourage benchmark overfitting.

## Decision

1. `retrieval_v4` remains immutable. Its manifest and nonce commitments are not rewritten.
2. `benchmarkaudit` reads public development artifacts only and reports Gold membership, topic-cohort size, exact scenario anchors, review reasons, and a deterministic report checksum.
3. Ambiguous cases remain visible in aggregate metrics; reports must also disclose per-task metrics and the audit limitation. We do not tune a ranker to reproduce an arbitrary note id among equivalent evidence.
4. Citation integrity and Gold-source relevance are separate metrics. `citation_precision` checks quote/hash/UTF-8 range integrity; `source_precision_at_k` measures relevance to the declared Gold sources.
5. Sealed holdout contents are not inspected during tuning. A holdout run requires an explicit versioned release id and verification against public commitments.
6. The next benchmark version must use a stratified task split, multi-Gold or relevance-pool labels for equivalent evidence, independently reviewed no-answer cases, and recorded inter-annotator agreement.

## Consequences

- Phase 7 can make honest pipeline and development comparisons without claiming that every v4 single-Gold rank is semantically unique.
- Security behavior is primarily supported by real PostgreSQL integration tests until a stratified public development benchmark is frozen.
- A new benchmark version requires new commitments and a fresh sealed holdout; it must not be derived by editing v4 after seeing its results.
