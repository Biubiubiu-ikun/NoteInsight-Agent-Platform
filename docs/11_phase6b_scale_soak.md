# Phase 6B Scale, Stampede, and Soak Testing

Date: 2026-07-15

## Scope

This phase keeps the note/auth domain defined by `最新项目规划.md` and adapts only the
capacity-engineering ideas from historical plans. It does not add RAG, Agent, Qdrant,
Kubernetes, or a new business domain.

Implemented:

- in-process cache-miss request coalescing for note detail and comment first pages;
- independent five-second backend-load contexts so one canceled caller cannot cancel
  work shared by other requests;
- backend-load and coalesced-request Prometheus counters;
- concurrent service tests for one-load behavior, page limits/cursors, and cancellation;
- a separate Compose project, ports, volumes, token file, and PostgreSQL tuning profile;
- migration and reconcile binaries plus migration SQL inside the scratch image;
- configurable Compose project, environment, container names, ports, token path,
  hotspot percentage, and prewarm behavior in the Phase 6 runner;
- a 4.21-million-row capacity database and large-data load tests;
- negotiated API gzip with pooled BestSpeed writers.

## Isolated Environment

| Component | Main | Capacity |
| --- | ---: | ---: |
| API | 18080 | 28080 |
| Worker | 18081 | 28081 |
| PostgreSQL | 15432 | 25432 |
| Redis | 6379 | 16379 |
| NATS client | 14222 | 24222 |
| NATS monitor | 18222 | 28222 |

The capacity project is `noteinsight-capacity`; its PostgreSQL and NATS volumes do not
share data with the main corpus. Capacity PostgreSQL uses 512 MB shared buffers, 2 GB
maximum WAL, a 15-minute checkpoint timeout, and WAL compression. These are local test
settings, not production sizing advice.

Start and migrate it:

```powershell
.\scripts\build_backend_linux.ps1
.\scripts\start_capacity_stack.ps1 -Rebuild
```

Generate data without touching the main volume:

```powershell
.\scripts\generate_capacity_data.ps1 -Profile capacity -Seed 20260714 `
  -ComposeProject noteinsight-capacity `
  -ComposeEnvFile deploy/compose/capacity.env `
  -TokenDirectory backend-go\tmp\capacity `
  -TokenFileName dev_tokens.csv -WithTokens -Truncate
```

## Dataset Evidence

Generation completed in `31m06s` without an LLM API.

| Table/domain | Rows |
| --- | ---: |
| users | 10,000 |
| notes | 50,000 |
| note_media | 50,000 |
| note_comments | 500,000 |
| note_likes | 2,000,000 |
| note_collects | 600,000 |
| note_comment_likes | 1,000,000 |
| **Core total** | **4,210,000** |

Text checks:

- empty note bodies: `0`; average body length: `674.1` characters;
- empty caption/OCR pairs: `0`; average lengths: `22.0` / `100.2`;
- empty comments: `0`; average comment length: `117.0`;
- development tokens: `10,000`; token file lines including header: `10,001`;
- database size after `ANALYZE`: `1,211 MB`.

Representative category-list, comment-list, and media queries used their intended
indexes and executed in approximately `0.3-1.4 ms` when inspected directly. Public
list/detail/comments smoke requests all returned HTTP 200.

## Stampede Evidence

The broad 100 RPS cold-hotspot comments run completed all `4,500` iterations with no
HTTP errors or dropped work, improving P95 from the earlier `267.8 ms` to `150.4 ms`.
Its 80/20 distribution still produced 790 legitimate distinct-key backend loads.

The deterministic same-key run used one note, 100% hotspot traffic, no prewarm, and
100 RPS for 30 seconds:

- `3,001` completed iterations, zero errors, zero drops;
- exactly one additional comment-page backend load;
- four requests observed a shared singleflight result.

This proves duplicate in-process loads are suppressed. It is not distributed locking:
multiple API replicas can still perform one load per replica. Cross-replica protection
or stale-while-revalidate remains a later option if production traffic requires it.

## Response Compression Finding

The first 4.21M-row notes-list run exposed a Docker Desktop transfer bottleneck. A
representative list response was 53,233 bytes, and 30 RPS produced 46 MB in 45 seconds.

| Implementation | Wire bytes | Actual RPS | P95 | Drops | Result |
| --- | ---: | ---: | ---: | ---: | --- |
| no gzip | 53,233 | 26.68 | 2,850.3 ms | 129 | FAIL |
| default gzip level | 3,501 | 29.53 | 401.7 ms | 0 | FAIL |
| pooled BestSpeed gzip | 4,546 | 29.88 | 112.2 ms | 0 | PASS |

The final implementation uses only the Go standard library, honors `q=0`, compresses
only `/api/` responses for clients that advertise gzip, and leaves readiness/metrics
plain. Tests decompress and verify the original payload.

## Large-Data Results

Selected comparable results after the final compression implementation:

| Scenario | Actual RPS | P50 | P95 | P99 | Error | Drops | Writes | Pipeline | Result |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- |
| notes list, 30 RPS, 45s | 29.88 | 12.6 ms | 112.2 ms | 234.7 ms | 0% | 0 | 0 | drained | PASS |
| mixed, 30 RPS, 45s | 29.86 | 26.0 ms | 318.1 ms | 589.9 ms | 0% | 0 | 203 | drained | FAIL |
| mixed, 10 RPS, 45s | 9.93 | 20.8 ms | 117.7 ms | 192.5 ms | 0% | 0 | 66 | drained | FAIL on three endpoint tails |
| mixed soak, 10 RPS, 10m | 10.03 | 32.0 ms | 431.1 ms | 1,239.5 ms | 0% | 0 | 899 | drained | FAIL |

The soak completed 6,002 requests. Outbox active/failed, JetStream pending, and
ack-pending all returned to zero. Database deadlocks and transaction rollbacks were
zero.

The aggregate soak P95 hides a strong cold-to-warm pattern. PostgreSQL average CPU was
about 100%, 66%, and 41% in the early high-I/O minutes, then 16-25% later. API log P95
fell from 327/171/198 ms during early minutes to 21-35 ms in the later steady period.
The scheduled startup reconciliation completed once in 27.9 seconds; its full-table
grouping is itself a scale concern, but it ended before the soak began.

## Capacity Conclusion

- Read-only notes-list capacity at 30 RPS meets the strict local SLO after compression.
- The system sustains mixed 30 RPS without errors, drops, or asynchronous backlog, but
  does not meet strict endpoint tail-latency targets at this data size.
- A short mixed 10 RPS run has good aggregate latency, but the cold-to-warm 10-minute
  run shows that a warm-up period and separate cold-start SLO are required.
- This is single-host Docker Desktop evidence, not a production capacity claim.

## Open Work

1. Add OpenTelemetry traces, runtime profiles, PostgreSQL statement statistics, and DB
   pool wait metrics to attribute API wait time instead of inferring it from snapshots.
2. Replace full source-of-truth reconciliation with incremental, partitioned, or
   checkpointed repair; keep a manually triggered full audit for emergencies.
3. Define separate cold-start and warm steady-state gates, then run a 30-minute warm
   soak after the mixed-tail fixes.
4. Add stateful Locust journeys, multi-replica API tests, and an external load generator.
5. Run the 10.72M-row profile only after generation switches to COPY/staging or deferred
   index construction; the current 4.21M profile already takes 31 minutes.

## Reproduction

```powershell
.\scripts\run_k6_phase6.ps1 -Profile baseline -Workload notes_list `
  -BaseUrl http://host.docker.internal:28080 -Rate 30 -Duration 45s `
  -NoteCount 50000 -CommentCount 500000 `
  -ComposeProject noteinsight-capacity `
  -ComposeEnvFile deploy/compose/capacity.env `
  -ContainerPrefix noteinsight-capacity `
  -TokenFile backend-go/tmp/capacity/dev_tokens.csv `
  -ApiHostPort 28080 -WorkerHostPort 28081 -NatsMonitorHostPort 28222

.\scripts\analyze_phase6_results.ps1 `
  -ResultRoot load-tests/results/phase6b-capacity `
  -Output load-tests/results/phase6b-capacity/index.md
```

Raw result directories remain ignored by Git. Stop the capacity containers while
preserving their volumes with:

```powershell
docker compose --env-file deploy/compose/capacity.env -f docker-compose.yml `
  -p noteinsight-capacity down
```
