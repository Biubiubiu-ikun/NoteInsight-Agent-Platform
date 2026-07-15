# Phase 7A-0 Retrieval Preflight

Updated: 2026-07-16

## Purpose

Phase 7A-0 closes the reproducibility and evaluation-governance gaps that must be resolved before canonical chunking or retrieval scoring begins. It does not implement retrieval, embeddings, RAG, or an Agent.

## Immutable Source Versions

- Notes, media, and comments carry monotonic `content_version` values.
- Meaningful content updates create a new `evidence_sources.source_version`; the previous source row is retained but marked deleted.
- `evidence_source_payloads` stores immutable canonical text, the complete version payload and its SHA-256 for every source version.
- Manual attempts to skip or overwrite content versions are normalized by database triggers.
- Media caption/OCR and comment text now have the same version history guarantees as note bodies.

## Frozen Dataset Snapshots

Migrations `000012` through `000015` add source/dataset versioning, benchmark retirement, immutable source payloads and content-only trigger scope. `noteinsight-datasetfreeze` serializes freezes per dataset, briefly stabilizes the Evidence Source registry, requires a payload for every selected source, computes a logical SHA-256 manifest, and publishes an immutable snapshot.

The manifest excludes the internal `evidence_sources.id`, so equivalent logical registries produce the same checksum. Repeating a freeze with unchanged active sources reuses the latest version.

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@127.0.0.1:15432/creatorinsight?sslmode=disable"
go run ./cmd/datasetfreeze --dataset-id=2
```

Current retrieval snapshot:

- dataset version: `2`
- active source references: `113,921`
- manifest scheme: `evidence_source_snapshot_v1`
- manifest checksum: `b91df11ca9136e000c759fd2c6de5b448816bb57d903849c478f99db8533eab5`

## Retrieval Benchmark V4

`retrieval_v3_20260715` is retired. Its deterministic public inputs can reconstruct all case checksums, so it remains historical audit evidence and must not support retrieval quality claims.

`retrieval_v4_20260716` is authored independently from the corpus Gold Case generator and bound to dataset version `2`. It contains 240 cases: 80 public development cases and 160 sealed holdout cases. Eight task families each contain 30 cases:

- semantic paraphrase;
- Chinese typo robustness;
- temporal conflict;
- cross-note comparison;
- no relevant document;
- relevant document with insufficient evidence;
- OCR source precision;
- authorization boundary and cross-project leakage.

Each private case receives a cryptographically random nonce. Public commitments contain only `SHA-256(nonce || unit-separator || case-checksum)`. Development nonces are public because development content is public; holdout nonces, questions, answers, and sources remain under `evaluation/private/` and are excluded from Git.

The freeze CLI writes and verifies public/private artifacts in same-filesystem staging directories before committing the database benchmark. It then atomically renames the private and public directories. If a database commit or publish result is ambiguous, verified staging paths are preserved in structured logs for recovery rather than discarding the nonces.

```powershell
cd backend-go
go run ./cmd/evalfreeze --verify-only `
  --output-dir ../evaluation/benchmarks/retrieval_v4
```

Manifest checksum: `851a0ae94df77291d72904185754a2bea65893826fa942d52961472b65ab1b74`.

All 240 cases are model-assisted and machine-validated. A stratified independent human review remains mandatory before publishing quality claims.

## Database Tests

The integration suite applies every migration to a disposable PostgreSQL database and verifies:

- note/media/comment evidence history;
- reconstructible immutable payload history and payload tamper rejection;
- resistance to manual version manipulation;
- concurrent freeze publication and unchanged-snapshot reuse;
- changed evidence producing a new snapshot checksum;
- immutable frozen snapshot rows and membership;
- immutable frozen and retired benchmark rows.

## Explicit Non-Goals

- canonical Evidence Documents or Chunks;
- Chinese tokenization or lexical ranking;
- embeddings, Qdrant, vector/hybrid retrieval, or reranking;
- online retrieval APIs;
- RAG or Agent orchestration;
- public holdout inspection or quality-score claims.

Phase 7A starts from dataset version `2` and the decisions in `docs/adr/`.
