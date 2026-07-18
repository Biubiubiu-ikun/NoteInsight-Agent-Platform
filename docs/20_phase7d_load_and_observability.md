# Phase 7D Retrieval Load and Observability

Updated: 2026-07-18

## Scope

This evidence covers the local single-instance retrieval stack on frozen dataset version `2`: Gin API, PostgreSQL, Qdrant `v1.18.2`, TEI `turing-1.9`, and the pinned Qwen3 embedding model. It does not represent a cloud, multi-instance, or external-load-generator capacity claim.

The load driver uses only the 80 public development queries. The sealed holdout remains unused. Every run records the benchmark SHA-256, application rate limit, intended RPS, dependency metrics, Docker/GPU samples, fault events, and a post-run recovery query.

## Lexical v3 Optimization

`postgres_fts_lexical_v3` keeps the v2 pairwise match and `ts_rank_cd` ordering semantics, but:

- an empty note-id hint can no longer execute the hint branch's document scan;
- a cheap `ts_rank` pre-rank bounds the expensive candidate rank to 480 rows;
- PostgreSQL stages are exposed as separate `db_query_duration_seconds` operations.

The full 80-case development regression is stored in `evaluation/results/retrieval_v4/development_phase7d_lexical_v3_prerank480.json`.

| Metric | lexical v2 | lexical v3 |
| --- | ---: | ---: |
| Recall@10 | 0.6812 | 0.6812 |
| MRR@10 | 0.6585 | 0.6585 |
| nDCG@10 | 0.6414 | 0.6414 |
| Gold-source precision | 0.1787 | 0.1787 |
| No-relevant rejection | 1.0000 | 1.0000 |
| Citation precision | 1.0000 | 1.0000 |
| P50 | 1,117.98 ms | 858.73 ms |
| P95 | 2,831.99 ms | 1,404.09 ms |
| P99 | 5,288.48 ms | 1,966.27 ms |

The quality gate still fails Recall/MRR. The optimization is a latency improvement, not a quality-pass claim.

## Local Capacity Evidence

The application rate limit was raised from the normal `120/min` to an explicitly recorded `6000/min` only for capacity isolation. A preflight now rejects accidental capacity runs whose application limit is lower than 120 percent of the planned request rate.

| Scenario | Result | Key evidence |
| --- | --- | --- |
| mixed 2 RPS, 15 s | PASS | 30 retrievals, zero errors/timeouts/citation failures; lexical/vector/hybrid P95 `2011/650/1934 ms` |
| warm mixed 2 RPS, 30 min | PASS | 3,601 retrieval iterations, 23 timeouts (`0.6387%`), zero 429/citation failures/dropped iterations; lexical/vector/hybrid P95 `3192.54/616.51/3098.76 ms`; recovery query passed |
| mixed 3 RPS, 30 s | FAIL | 13 percent 504; lexical/hybrid P95 exceeded 4 s |
| mixed 2-4-6 RPS step | FAIL | zero 429, but 37 percent 504; PostgreSQL CPU peaked at about 10 cores while Qdrant/TEI remained below that bottleneck |
| Qdrant restart, mixed 2 RPS, 45 s | PASS | bounded dependency errors during restart, no citation failures, readiness and recovery query passed |
| TEI restart, mixed 2 RPS, 60 s | PASS | bounded dependency errors during restart, no citation failures, readiness and recovery query passed |
| concurrent index batch 8 + mixed 2 RPS | FAIL | 3 percent timeout; vector latency passed after throttling, but the strict online error gate did not |
| concurrent index batch 8 + mixed 1 RPS | PASS | 45 retrievals, zero errors/timeouts/citation failures; index resumed from 1,472 to 2,064 points at about 10.6 points/s |

The defensible local envelope is therefore 2 mixed RPS without indexing, now sustained for 30 minutes, or 1 mixed RPS while a batch-8 vector build shares the same TEI/PostgreSQL/Qdrant resources. The warm soak used the pinned k6 image, the frozen 80-case development set at SHA-256 `981050afd9f0bd9880a23e46d4bf146cad30c14e787b0b980ce333a1e95cdbaf`, and an explicitly recorded test-only `6000/min` application limit. Scaling claims above that require separate indexing capacity or production-like resource quotas.

## Backpressure and Recovery

Online query embeddings retain three short retries. Offline document batches use up to eight exponential-backoff attempts capped at five seconds and honor context cancellation. This prevents a transient TEI 429 from immediately discarding durable index progress while keeping online request latency bounded.

The concurrent test deliberately stopped the index process. PostgreSQL checkpoint recovery resumed across attempts, and the dedicated partial Qdrant collection/control row was removed after evidence capture. The immutable production collection remained unchanged at 56,349 exact ID/hash-matched points.

## Metrics and Alerts

The API now exports:

- TEI/Qdrant request count, duration, in-flight calls and retry reason;
- embedding batch size by online/offline operation;
- PostgreSQL retrieval stage duration;
- vector-index build count, maximum checkpoint lag and oldest update age by bounded status;
- vector-index metrics scrape success.

Prometheus directly scrapes API, TEI and Qdrant. Grafana includes retrieval, dependency, TEI queue/overload, Qdrant latency/error and vector-index state/lag/age panels. Alerts cover dependency error/saturation, TEI overload/queue delay, Qdrant error/latency, vector metric failure, failed builds and stalled checkpoints.

Migration `000022` enables `pg_stat_statements`; Compose preloads it, enables query IDs and I/O timing, and logs statements slower than one second by default. Production PostgreSQL must pre-authorize the extension before application migration when the managed provider restricts extension creation.

```sql
SELECT calls, round(mean_exec_time::numeric,2) AS mean_ms,
       round(max_exec_time::numeric,2) AS max_ms, rows,
       shared_blks_hit, shared_blks_read, temp_blks_written, query
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 20;
```

## Reproduce

```powershell
# Capacity runs require an explicit high test-only limit.
$env:RATE_LIMIT_RETRIEVAL_READ_LIMIT = "6000"
docker compose up -d --no-deps --force-recreate backend

.\scripts\run_k6_phase7_retrieval.ps1 `
  -Profile baseline -Mode mixed -Rate 2 -Duration 45s

.\scripts\run_k6_phase7_retrieval.ps1 `
  -Profile baseline -Mode mixed -Fault qdrant_restart `
  -Rate 2 -Duration 45s -FaultAfterSeconds 10

.\scripts\run_k6_phase7_retrieval.ps1 `
  -Profile baseline -Mode mixed -Rate 1 -Duration 45s `
  -ConcurrentIndexIngestionRunId phase7a_dv2_v1_20260718 `
  -ConcurrentIndexBatchSize 8
```

## Open Gates

- Production-like CPU/GPU quotas, multiple API instances and an external load generator are still open.
- Local OpenTelemetry export is complete across API, SQL, Redis, Outbox, NATS, Worker, TEI and Qdrant; see `docs/21_phase7d_distributed_tracing.md`. A managed/authenticated trace backend, production sampling/retention policy, managed secrets, TLS/private networking, Qdrant authentication and PostgreSQL PITR remain deployment work.
- Benchmark v5 still requires real independent reviewers; no cross-encoder decision should precede that freeze.
