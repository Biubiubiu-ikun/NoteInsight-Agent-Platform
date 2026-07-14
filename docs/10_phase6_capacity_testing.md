# Phase 6A Capacity Testing

Date: 2026-07-14

## Scope

This phase adds reproducible capacity evidence for the current note/auth domain. It
adapts the useful performance ideas in the historical first plan without restoring
video or danmu concepts. `最新项目规划.md` remains authoritative.

Implemented in this slice:

- constant-arrival-rate baseline tests;
- 25 -> 50 -> 75 RPS step tests;
- 20 -> 120 -> 20 RPS hotspot spike tests;
- uniform and 80/20 hotspot access distributions;
- per-endpoint P50/P95/P99, error, rate-limit, write, and dropped-iteration metrics;
- API, worker, PostgreSQL, Redis, NATS, Docker, and data-count snapshots per run;
- Docker resource time-series sampling;
- an aggregate result-index generator;
- bounded-memory seed generation profiles for later larger-data reruns.

This is a local development capacity envelope, not a production capacity claim.

## Environment

```text
Deployment: Docker Desktop / Docker Compose on one Windows workstation
Docker CPUs: 8
Docker memory: 8,203,669,504 bytes (7.64 GiB visible to containers)
PostgreSQL: 16-alpine
Redis: 7-alpine
NATS: 2.12-alpine with JetStream
k6: grafana/k6:latest, Docker runner
API instances: 1
Worker instances: 1
Cache: enabled
Authentication: pre-generated development bearer token pool
```

Database state after the test series:

| Table/domain | Rows |
| --- | ---: |
| users | 1,022 |
| notes | 5,251 |
| note_media | 5,891 |
| note_comments | 61,019 |
| note_likes | 100,549 |
| note_collects | 30,249 |
| note_shares | 222 |
| note_comment_likes | 50,502 |
| behavior_events | 3,697 |
| content_eval_cases | 1,100 |

The load test sampled the stable original ranges `notes 1..5000` and
`comments 1..20000`. All generated notes retain substantive Chinese bodies, captions,
and OCR text; comments remain semantically linked to their note scenarios.

## Test Thresholds

| Endpoint/metric | Development threshold |
| --- | ---: |
| notes list P95 | < 250 ms |
| note detail P95 | < 150 ms |
| comments read P95 | < 100 ms |
| rankings read P95 | < 80 ms |
| note/comment like P95 | < 150 ms |
| collect/share/comment create P95 | < 200 ms |
| HTTP/operation error rate | < 1% |
| dropped iterations | 0 |

These intentionally strict local targets are more useful than the older single global
`P95 < 800 ms` threshold.

## Results

| Scenario | Actual RPS | P50 | P95 | P99 | Error | Dropped | Cache hit | Result |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| comments read, uniform, target 40 | 40.10 | 7.0 ms | 74.2 ms | 195.4 ms | 0% | 0 | 1.4% | PASS |
| mixed 85/15, uniform, target 30 | 30.01 | 8.1 ms | 41.7 ms | 135.8 ms | 0% | 0 | 23.2% | PASS |
| mixed step 25/50/75, uniform | 45.66 avg | 36.3 ms | 387.3 ms | 651.6 ms | 0% | 0 | 14.8% | FAIL |
| mixed, uniform, target 50 | 49.94 | 33.9 ms | 306.1 ms | 518.6 ms | 0% | 0 | 44.3% | FAIL |
| mixed, uniform, target 75 | 73.54 | 215.2 ms | 779.9 ms | 1166.7 ms | 0% | 11 | 37.7% | FAIL |
| comments hotspot 100, cold keys | 96.61 | 17.6 ms | 267.8 ms | 1790.8 ms | 0% | 100 | 77.2% | FAIL |
| comments hotspot 100, prewarmed | 100.05 | 8.7 ms | 80.8 ms | 148.7 ms | 0% | 0 | 80.4% | PASS |
| comments hotspot spike 20/120/20, prewarmed | 51.75 avg | 24.5 ms | 142.6 ms | 244.9 ms | 0% | 0 | 79.8% | FAIL |

The spike's segmented P95 was `133.7 ms` before the peak, `153.0 ms` at the peak,
and `96.5 ms` after recovery. The system recovered without errors, dropped work,
Outbox backlog, or JetStream backlog.

One uniform mixed 40 RPS repetition produced a `570.3 ms` P95, worse than the 50 RPS
run, while sampled CPU was lower. It is retained in raw results as a local-host
variability sample but is not used as a monotonic capacity boundary.

## Findings

1. The strict mixed-workload envelope on this machine is at least 30 RPS and below a
   repeatably stable 50 RPS. The service still returns successful responses at 50 RPS,
   but tail latency no longer meets the development SLO.
2. At 75 RPS PostgreSQL reached about `510%` container CPU while the API peaked around
   `153%`; Redis, NATS, and the worker were materially lower. PostgreSQL query and
   transaction concurrency is the primary measured bottleneck.
3. Uniform random comment reads intentionally defeat the first-page cache. The warm
   hotspot test demonstrates the expected cache benefit: 100 RPS meets the 100 ms P95
   target with about 80% hit rate.
4. Cold hotspot keys show cache stampede risk. Concurrent first misses caused a large
   P99 and dropped arrivals even though steady-state CPU was not saturated. Cache-miss
   request coalescing (`singleflight`) and stale-while-revalidate are priority
   optimizations before claiming robust hot-key capacity.
5. All test writes used bearer tokens, and all asynchronous work drained. Final fact
   checks reported `note_drift=0`, `comment_drift=0`, Outbox active/failed `0`, and
   JetStream pending/ack-pending `0`.

## Scalable Data Generation

The previous `seedgen` retained every pending row, every unique interaction pair, and
every generated document in memory. Increasing numeric flags alone would therefore
have risked OOM. It now uses:

- automatic bounded batch flushing;
- an O(1)-memory deterministic affine sequence for unique interaction pairs;
- per-note streaming comment generation with 5% hot / 20% warm / 75% tail tiers;
- deterministic category reconstruction instead of retaining all note documents;
- batched Redis ranking writes and pattern-based cleanup;
- count overrides for every generated entity.

Built-in profiles:

| Profile | Users | Notes | Comments | Likes | Collects | Comment likes | Estimated rows |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| dev | 1,000 | 5,000 | 20,000 | 100,000 | 30,000 | 50,000 | 211,000 |
| capacity | 10,000 | 50,000 | 500,000 | 2,000,000 | 600,000 | 1,000,000 | 4,210,000 |
| million-comments | 20,000 | 100,000 | 1,000,000 | 5,000,000 | 1,500,000 | 3,000,000 | 10,720,000 |

No LLM API is used for bulk generation. The existing deterministic content catalog
continues to generate meaningful text suitable for future Evidence Store and RAG work.

Dry-run a profile:

```powershell
.\scripts\generate_capacity_data.ps1 -Profile capacity -DryRun
```

Generate a dedicated capacity database after rebuilding the image:

```powershell
.\scripts\build_backend_linux.ps1
docker compose up -d --build
.\scripts\generate_capacity_data.ps1 -Profile capacity -Truncate -WithTokens
```

`-Truncate` deletes the current corpus and simulation runs. Use a dedicated Compose
volume or back up the current PostgreSQL volume before running it.

## Reproduction

```powershell
.\scripts\run_k6_phase6.ps1 -Profile baseline -Workload mixed -Rate 30 -Duration 45s
.\scripts\run_k6_phase6.ps1 -Profile step -Workload mixed
.\scripts\run_k6_phase6.ps1 -Profile baseline -Workload comments_read `
  -AccessPattern hotspot -HotNoteCount 100 -Rate 100 -Duration 30s
.\scripts\run_k6_phase6.ps1 -Profile spike -Workload comments_read `
  -AccessPattern hotspot -HotNoteCount 100 -SpikeRps 120
.\scripts\analyze_phase6_results.ps1
```

Raw run artifacts are intentionally ignored by Git under `load-tests/results/`.

## Not Done Yet

- a 30-60 minute soak test;
- Locust's stateful session model;
- the 4.21 million-row and 10.72 million-row database reruns;
- multi-instance API or external load-generator tests;
- cloud production-capacity claims;
- automatic cache-stampede suppression;
- Prometheus/Grafana dashboards and alert rules.

## Phase 6B

1. Add cache-miss request coalescing and repeat the cold-hotspot test.
2. Generate the `capacity` profile in a dedicated volume, run `ANALYZE`, and repeat the
   exact baseline/step/hotspot matrix for a same-machine comparison.
3. Run a 30-minute soak at the verified SLO-safe rate and inspect memory, connection,
   Outbox, JetStream, and latency drift.
4. Add Locust stateful user journeys only after the k6 envelope is stable.
5. Then proceed to the note-domain Evidence Store and deterministic RAG ingestion.
