# Phase 4B Write Rate Limiting and Reconciliation

## Planning Source

`最新项目规划.md` remains the source of truth. It currently specifies the authenticated note domain through Phase 3. The older Phase 4 material is used only to choose the next infrastructure task; all video/danmu references are mapped to the current note/comment domain.

## Goal

Phase 4B closes two reliability gaps left after the Phase 4A local Outbox worker:

- protect PostgreSQL from authenticated write bursts with Redis user-level rate limits;
- repair derived counters, hot rankings, stale caches, and abandoned Outbox processing leases.

This phase keeps the worker in the API process. It does not add NATS/Kafka or move counters fully off the synchronous request path.

## Directory Changes

```text
backend-go/
  cmd/
    reconcile/
      main.go
  internal/
    platform/
      ratelimit/
        limiter.go
        limiter_test.go
    reconcile/
      repository.go
      reconciler.go
      reconciler_test.go
  migrations/
    000005_phase4b_reconcile.sql
docs/
  05_phase4b_rate_limit_reconcile.md
```

Existing API middleware, router, config, metrics, Outbox repository, worker, API bootstrap, Docker Compose, README, and tests were updated.

## Write Rate Limits

The limiter uses a Redis Lua script so `INCR`, initial expiry, count, and TTL are one atomic operation. Keys follow the historical project convention:

```text
rate:user:{user_id}:content_write
rate:user:{user_id}:comment_write
rate:user:{user_id}:interaction_write
```

Default policies:

| Policy | Routes | Default |
| --- | --- | --- |
| `content_write` | create/update/delete note | 30 per minute |
| `comment_write` | create/delete comment | 60 per minute |
| `interaction_write` | note like/collect/share and comment like | 120 per minute |

An allowed response includes `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset`. A rejected response returns HTTP `429`, `Retry-After`, and a JSON error body. Redis errors fail closed with HTTP `503` so a Redis outage cannot turn into an unbounded PostgreSQL write burst.

The middleware runs after authentication and active-user checks but before ownership checks and handlers. Identity always comes from the JWT auth context.

Prometheus metric:

```text
rate_limit_decisions_total{policy,result}
```

## Outbox Lease Recovery

`LockPending` changes an event to `processing`. Phase 4A had no recovery path if the process exited after that update. Phase 4B now periodically moves events whose processing lease is older than five minutes back to `retry`.

The worker checks once per minute by default and also checks immediately after startup. The partial index in `000005_phase4b_reconcile.sql` keeps this scan bounded:

```sql
CREATE INDEX IF NOT EXISTS idx_outbox_processing_updated
    ON outbox_events(updated_at)
    WHERE status = 'processing';
```

Prometheus metric:

```text
outbox_stale_recovered_total
```

## Derived-Data Reconciliation

The reconciler runs ten seconds after API startup and then hourly by default:

1. Recompute comment like and reply counts from `note_comment_likes` and active child comments.
2. Recompute note like, collect, active comment, and share counts from source tables.
3. Recompute `hot_score` with the current Phase 3 formula.
4. Rebuild global/category note ZSETs and per-note hot-comment ZSETs.
5. Invalidate note detail and comment first-page caches whose derived counts may be stale.

Only rows whose derived values differ are updated. Redis replacement is issued with `TxPipelined`, so readers do not observe a partially rebuilt collection of ranking keys.

Prometheus metrics:

```text
reconcile_runs_total{result}
reconcile_duration_seconds
reconcile_rows_repaired_total{entity}
```

Run the same repair manually:

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
go run ./cmd/reconcile
```

## Configuration

```text
RATE_LIMIT_ENABLED=true
RATE_LIMIT_CONTENT_WRITE_LIMIT=30
RATE_LIMIT_CONTENT_WRITE_WINDOW=1m
RATE_LIMIT_COMMENT_WRITE_LIMIT=60
RATE_LIMIT_COMMENT_WRITE_WINDOW=1m
RATE_LIMIT_INTERACTION_WRITE_LIMIT=120
RATE_LIMIT_INTERACTION_WRITE_WINDOW=1m

OUTBOX_BATCH_SIZE=50
OUTBOX_POLL_INTERVAL=500ms
OUTBOX_RECOVERY_INTERVAL=1m
OUTBOX_PROCESSING_TIMEOUT=5m

RECONCILE_ENABLED=true
RECONCILE_STARTUP_DELAY=10s
RECONCILE_INTERVAL=1h
RECONCILE_TIMEOUT=5m
RECONCILE_RANKING_LIMIT=1000
```

## Verification

Local verification on 2026-07-14:

- `go test -p 1 -timeout 60s ./...` passed.
- Linux API build passed.
- migration `000005_phase4b_reconcile.sql` applied successfully.
- `/ready` returned PostgreSQL and Redis `ok`.
- With a temporary interaction limit of 3, four duplicate note-like requests returned `200,200,200,429` and remaining quotas `2,1,0,0`.
- The duplicate requests did not change the stored like count (`25` before and after).
- One deliberately stale `processing` Outbox event was recovered and returned to `sent`; final status was `sent=8` with no pending/retry/processing/failed rows.
- Deliberately corrupted note/comment counters were repaired; all seven checked source-table invariants returned true.
- A fake Redis note score of `999999` was rebuilt to the PostgreSQL score `163`.
- Stale note detail and comment first-page cache keys were removed.
- A second one-shot reconcile repaired zero rows, confirming idempotency.

## Not In This Phase

- NATS/Kafka or a broker-backed publisher.
- Separate API and worker containers.
- Fully asynchronous note/comment counter updates.
- Agent/RAG, Qdrant, or ingestion events.
- IP-level auth endpoint throttling or distributed abuse scoring.
- Reconcile administration UI.

## Recommended Next Step

Phase 4C should split event processing into a standalone `cmd/worker` process and introduce NATS JetStream publishing/consumption while preserving PostgreSQL Outbox durability and `processed_events` idempotency. The current rate limiter and one-shot reconcile command can remain unchanged.
