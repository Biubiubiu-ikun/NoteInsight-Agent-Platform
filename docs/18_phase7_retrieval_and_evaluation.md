# Phase 7 Retrieval and Offline Evaluation

Updated: 2026-07-18

## Scope

Phase 7B adds an authorization-filtered PostgreSQL lexical baseline, a public retrieval API, immutable index manifests, and guarded offline evaluation. Phase 7C adds pinned dense embeddings, Qdrant vector serving, and lexical+dense hybrid retrieval. It does not add an Agent, RAG answer generation, a cross-encoder reranker, or a production cloud deployment.

## API

`POST /api/v1/retrieval/search` accepts:

```json
{
  "project_id": 1,
  "dataset_version_id": 2,
  "ingestion_run_id": "phase7a_dv2_rebuild_v2_20260718",
  "query": "敏感肌通勤防晒的主要风险是什么？",
  "mode": "hybrid",
  "limit": 10,
  "filters": {
    "document_types": ["note", "note_media"],
    "source_types": ["note", "note_media"]
  }
}
```

Anonymous callers can search public evidence. A valid JWT expands access only through active project membership. The response includes the immutable dataset/ingestion/index identity, query plan, threshold decision, ranked chunks, exact source citations, candidate count, latency, and embedding call count.

```powershell
.\scripts\smoke_phase7_retrieval.ps1 -Modes lexical
.\scripts\smoke_phase7_retrieval.ps1 -Modes lexical,vector,hybrid
.\scripts\smoke_phase7_retrieval.ps1 -Modes lexical,hybrid `
  -Query "zzzxxyyqqq-nonexistent-987654321" `
  -ExpectedDecision no_relevant_document
```

## Storage and Versions

| Migration | Contract |
| --- | --- |
| `000017_phase7b_lexical_retrieval.sql` | Immutable lexical index, global lexeme statistics, evaluation runs and per-case results |
| `000018_phase7b_visibility_scoped_lexemes.sql` | Public/member IDF statistics so private corpus frequency cannot leak into public ranking |
| `000019_phase7b_dataset_source_memberships.sql` | Many-to-many dataset/source membership used by the independent quality corpus |
| `000020_phase7c_vector_retrieval.sql` | Immutable model/revision/dimension/collection/point-count/checksum vector index control row |

Current implementation identities:

- lexical index: `postgres_ts_stat_v1`;
- lexical retriever: `postgres_fts_lexical_v2`;
- vector index: `qwen3_dense_cosine_v1`;
- vector retriever: `qdrant_qwen3_dense_v1`;
- hybrid retriever: `rrf_lexical_dense_v2`;
- reranker: `weighted_coverage_v2`;
- metrics: `retrieval_metrics_v2`.

The lexical query first materializes GIN-matched chunk ids, then joins the immutable ingestion snapshot. This avoids a PostgreSQL nested-loop plan that previously pushed representative queries above one second. Vector results are pre-filtered in Qdrant and post-authorized in PostgreSQL. Hybrid executes lexical and dense retrieval concurrently, merges candidates with reciprocal-rank fusion, and applies a development-tuned `0.60` acceptance threshold.

## Local Vector Profile

```powershell
$env:CONTAINER_HTTP_PROXY='http://host.docker.internal:7890' # only when required
$env:CONTAINER_HTTPS_PROXY=$env:CONTAINER_HTTP_PROXY
docker compose --profile retrieval up -d qdrant text-embeddings

Invoke-WebRequest http://127.0.0.1:16333/readyz
Invoke-WebRequest http://127.0.0.1:18082/health
```

The tested Windows development profile uses an RTX 2060 6 GB GPU. TEI is constrained to 2,048 batch tokens, 8 batch requests, and 32 concurrent requests. A client embedding batch of 32 completed the 15,911-point quality index in 23m26s at roughly 11 points/s. Batch 64 was rejected with TEI `429 Model is overloaded` and is not the default.

```powershell
cd backend-go
$env:POSTGRES_DSN='postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable'
$env:QDRANT_URL='http://127.0.0.1:16333'
$env:EMBEDDING_URL='http://127.0.0.1:18082'

go run ./cmd/vectorindex `
  --ingestion-run-id phase7a_dv2_rebuild_v2_20260718 `
  --timeout 6h
```

## Evaluation Controls

- Public development cases are verified from `evaluation/benchmarks/retrieval_v4`.
- A development run must use the manifest dataset unless `--allow-development-dataset-override` is explicitly set; override runs can diagnose a quality subset but cannot pass the formal dataset-contract gate.
- Holdout requires `--allow-holdout`, a private input file, an authorized project user, and a versioned `--release-id`; commitments are verified before evaluation.
- Reports persist immutable run/case rows and are atomically published as JSON artifacts.
- The report includes Recall@K, MRR, nDCG, no-relevant rejection, insufficient-evidence source recall, authorization non-leakage, citation integrity, Gold-source precision, p50/p95/p99 latency, embedding calls, and failure categories.

```powershell
go run ./cmd/benchmarkaudit `
  --benchmark-root ../evaluation/benchmarks/retrieval_v4 `
  --output ../evaluation/results/retrieval_v4/development_benchmark_audit_v1.json

go run ./cmd/retrievaleval `
  --run-id my_development_run `
  --split development --mode lexical `
  --dataset-version-id 2 `
  --ingestion-run-id phase7a_dv2_rebuild_v2_20260718 `
  --top-k 10 --strict `
  --output ../evaluation/results/retrieval_v4/my_development_run.json
```

## Evidence So Far

Formal lexical `development_phase7b_lexical_v3.json`, bound to dataset version 2:

| Metric | Value |
| --- | ---: |
| Recall@10 | 0.6812 |
| MRR@10 | 0.6585 |
| nDCG@10 | 0.6414 |
| No-relevant rejection | 1.0000 |
| Citation integrity | 1.0000 |
| Gold-source precision | 0.1787 |

The formal lexical gate fails Recall and MRR, which is the evidence-based reason for testing dense and hybrid retrieval.

The main dataset-version-2 vector index is complete and immutable:

| Field | Value |
| --- | --- |
| Points | 56,349 |
| Collection | `noteinsight_7aa574ea1bb52ae1591b4ad0d5969013` |
| Build time | 1h1m37s |
| Manifest checksum | `432221b4873b965b52444776d9e887bd79cc5ff3d1581abbf3157f88b5ae8627` |

Formal same-contract development results on dataset version 2:

| Mode | Recall@10 | MRR@10 | nDCG@10 | No-relevant | Citation integrity | p95 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| lexical | 0.6812 | 0.6585 | 0.6414 | 1.0000 | 1.0000 | 2,831.99 ms |
| vector | 0.2391 | 0.2366 | 0.2252 | 0.9091 | 1.0000 | 548.66 ms |
| hybrid | 0.5652 | 0.5598 | 0.5384 | 0.9091 | 1.0000 | 2,262.06 ms |

All three runs match the frozen dataset contract, and all three fail the Recall/MRR gate. Dense retrieval is faster but loses exact scenario discrimination in the large, near-duplicate corpus. It can also accept a random ASCII out-of-domain string at the current threshold, so dense no-answer calibration remains open. Hybrid recovers much of the lexical signal but does not beat the lexical baseline on this benchmark. These artifacts are retained as failed formal baselines; the v4 holdout remains sealed and no result is promoted as a quality pass.

Quality-corpus diagnostic results are not formal gate passes because they use dataset version 4 rather than the manifest's dataset version 2:

| Mode | Recall@10 | MRR@10 | No-relevant | Citation integrity | p50 |
| --- | ---: | ---: | ---: | ---: | ---: |
| lexical | 1.0000 | 0.8448 | 1.0000 | 1.0000 | about 125 ms |
| vector | 0.6884 | 0.3869 | 1.0000 | 1.0000 | 256.16 ms |
| hybrid v2 | 1.0000 | 0.8279 | 0.9091 | 1.0000 | 333.80 ms |

The dense-only model performs poorly on OCR-position and cross-note-comparison tasks. Hybrid recovers lexical exact-match behavior while improving semantic coverage, but its no-answer margin is narrow and requires a new independently reviewed benchmark before a production quality claim.

The deterministic benchmark audit checksum is `2e702eb90709b965467ffa79275189e95da6349ed4d4df425194fee40b616850`: 58 cases pass exact distinguishability checks, 11 no-Gold cases are not applicable, and 11 insufficient-evidence cases require review. See ADR 0005.

## Security and Reliability Evidence

- Access is resolved before index readiness, embeddings, or scoring, preventing private index metadata inference.
- Qdrant payload filters reduce unauthorized candidates; PostgreSQL remains the final membership, visibility, ingestion-snapshot, source-version, and deletion authority.
- Real PostgreSQL integration covers private member retrieval, anonymous zero-result/zero-metadata behavior, historical snapshots, soft deletion, stale Qdrant hits, exact UTF-8 citation ranges, and deterministic fake-vector ranking.
- Unit tests cover query planning, ranking, citation/source metric separation, TEI instruction and dimension validation, Qdrant API-key/error behavior, vector filters, model-call accounting, and vector metadata redaction.
- The API has a four-second request timeout, retrieval-specific rate limiting, structured dependency errors, bounded transient-overload retries, Prometheus request/duration/candidate/result metrics, and retrieval error-rate/P95 alerts.

## Remaining Before Phase 8

1. Freeze a stratified, independently reviewed benchmark v5 with multi-Gold relevance labels and public authorization cases.
2. Add resumable vector indexing, Qdrant orphan/point-count reconciliation, retention, snapshots, and restore drills.
3. Benchmark a pinned cross-encoder reranker only after benchmark v5 is frozen.
4. Run vector/hybrid load and failure tests with production-like CPU/GPU quotas; add dependency saturation and index-build dashboards/alerts.
5. Configure Qdrant API keys, TLS/private networking, secret management, and cloud capacity evidence before production deployment.
