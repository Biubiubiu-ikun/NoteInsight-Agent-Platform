# Phase 4D Async Counters and Fault Verification

## Planning Source

`最新项目规划.md` remains the source of truth. It defines the current note and
authentication domain through Phase 3. Phase 4D adapts the useful asynchronous
processing ideas from the historical plans without restoring video or danmu concepts.

## Scope

Phase 4D finishes the event-driven write path started in Phase 4A-4C:

```text
API transaction
  -> write source-of-truth fact
  -> enqueue Outbox event
  -> return without synchronously changing derived counters

standalone worker
  -> publish through JetStream
  -> claim event idempotently
  -> record behavior fact
  -> rebuild affected counters from fact tables
  -> commit all PostgreSQL work in one transaction
  -> invalidate Redis caches and refresh rankings
```

This phase also adds repeatable authentication acceptance and NATS outage tests.

## Counter Consistency

The API no longer directly changes these derived values:

- `notes.like_count`
- `notes.collect_count`
- `notes.comment_count`
- `notes.share_count`
- `note_comments.like_count`
- `note_comments.reply_count`
- `notes.hot_score` after interaction writes

The request transaction still writes the authoritative rows in `note_likes`,
`note_collects`, `note_comments`, `note_shares`, and `note_comment_likes`. It enqueues
an Outbox event in the same transaction.

The worker does not apply a blind `+1`. It rebuilds the affected value from the fact
tables. This makes duplicate and out-of-order delivery converge to the same result.
The worker transaction includes:

1. insertion into `processed_events`;
2. insertion into `behavior_events`;
3. exact counter and hot-score rebuild;
4. commit.

If any database operation fails, the transaction rolls back, including the processed
event marker. Concurrent duplicate consumers are serialized by the unique constraint
on `(event_id, consumer_name)`.

## Comment Deletion

Soft deletion now emits `comment.deleted` in the same transaction that sets
`note_comments.status = 0`. The worker rebuilds:

- the note's active `comment_count`;
- the parent comment's active `reply_count`, when present;
- the deleted comment's materialized counters;
- the note ranking and affected Redis caches.

## API Response Semantics

Interaction responses retain their existing count fields and add `count_pending`.

- `count_pending=true` means the fact and Outbox event committed, while the returned
  count is the latest materialized value and may not yet include this request.
- repeated idempotent actions return `applied=false` and `count_pending=false`.

This makes eventual consistency visible instead of presenting a stale count as a
synchronous guarantee.

## Redis Failure Policy

PostgreSQL counters and behavior events are durable. Redis caches and rankings are
derived data. After a successful database transaction, a Redis refresh failure is
recorded and logged, but does not send the domain event to the DLQ. The scheduled
reconciler repairs Redis state from PostgreSQL.

The worker invalidates both note detail and comment first-page caches after applying
counter changes, preventing a read during event lag from leaving stale data cached for
the full TTL.

## Files

```text
backend-go/internal/worker/repository.go
backend-go/internal/worker/event_processor.go
backend-go/internal/worker/event_processor_test.go
backend-go/internal/note/repository.go
backend-go/internal/note/service.go
backend-go/internal/note/models.go
backend-go/internal/outbox/repository.go
backend-go/internal/platform/observability/metrics.go
load-tests/k6/phase4d_event_pipeline.js
scripts/run_k6_phase4d_fault.ps1
scripts/smoke_phase2c_auth.ps1
```

The former standalone behavior repository and the old non-transactional processed-event
methods were removed so there is only one durable event-application path.

No migration was required. Existing fact-table indexes and the Phase 4A
`processed_events` constraint support this implementation.

## Metrics

New worker metrics:

- `domain_event_lag_seconds{event_type}`
- `derived_data_refresh_total{event_type,result}`
- `outbox_oldest_unsent_age_seconds`

The event-lag histogram measures creation-to-durable-application delay. The Outbox age
gauge remains useful while the broker is unavailable and no event can complete.

## Automated Acceptance

Go checks:

```powershell
cd backend-go
go test ./...
go vet -p 1 ./...
```

Phase 2C API acceptance:

```powershell
.\scripts\smoke_phase2c_auth.ps1
```

This covers registration, duplicate username, password hashing, login, bad password,
refresh, logout, `/me`, token-owned note/comment creation, owner/admin authorization,
banned-user restrictions, idempotent interactions, async counter convergence, and
comment keyset pagination.

Phase 4D broker outage test:

```powershell
.\scripts\run_k6_phase4d_fault.ps1 `
  -Vus 5 `
  -WarmupSeconds 5 `
  -OutageSeconds 10 `
  -RecoveryTrafficSeconds 10
```

The runner generates non-idempotent share events, stops NATS during traffic, starts it
again, and fails unless the API thresholds pass and Outbox/JetStream fully drain.

## Local Verification

Verified on 2026-07-14:

- Worker stopped: note facts changed while materialized counts stayed unchanged.
- Worker restarted: note like/comment/share and comment-like counters matched facts.
- Comment soft delete stayed asynchronous and then converged correctly.
- Reprocessing an event after deleting its processed marker kept the count unchanged
  and restored exactly one processed marker and one behavior event.
- Phase 2C automated acceptance passed all checks.
- NATS outage k6: 120/120 event writes passed, HTTP failures were `0.00%`, P95 was
  `32.46ms`, and Outbox/pending/ack-pending all returned to zero.
- The worker recorded 120 `note.shared` event-lag samples.

## Not In Phase 4D

- Behavior Simulator personas and state transitions
- Prometheus server, Grafana dashboards, and alert rules
- OpenTelemetry distributed tracing
- DLQ replay administration
- production TLS and infrastructure authentication
- RAG, Agent runtime, Qdrant, or model API calls
- Kubernetes deployment

## Next Phase

Phase 5A should build a deterministic, note-domain Behavior Simulator:

- persona distributions;
- Markov session chains over feed, note, comment, like, collect, share, and exit states;
- Zipf/Pareto/Poisson and burst-based popularity models;
- reproducible profiles and distribution reports;
- direct high-volume generation without requiring an LLM API.
