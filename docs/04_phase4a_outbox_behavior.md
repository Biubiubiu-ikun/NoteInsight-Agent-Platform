# Phase 4A Outbox, Behavior Events, and Local Worker

## Planning Source

`最新项目规划.md` remains the current source of truth. It details Phase 3 and points the next step toward behavior-event outbox, async aggregation, and a more formal ranking pipeline. Older planning files are used only as historical reference.

## Goal

Phase 4A adds a local PostgreSQL Outbox Pattern and behavior-event recording for the note-domain write path.

This is intentionally smaller than a full event platform:

- No NATS/Kafka yet.
- No RAG/Agent ingestion yet.
- No full async counter migration yet.
- No recommendation system yet.

## Implemented

- `outbox_events` table for transactional event enqueue.
- `processed_events` table for idempotent worker processing.
- `behavior_events` table for user behavior records.
- Local in-process outbox worker started by the API process.
- Outbox events emitted from the same transaction as:
  - note created
  - comment created
  - note liked
  - note collected
  - note shared
  - comment liked
- Worker records behavior events and refreshes Redis ranking data.
- New Prometheus metrics:
  - `outbox_events_locked_total`
  - `outbox_events_processed_total`
  - `outbox_events_retried_total`
  - `outbox_events_failed_total`
  - `behavior_events_recorded_total`

## Directory Changes

```text
backend-go/
  migrations/
    000004_phase4a_outbox_behavior.sql
  internal/
    behavior/
      models.go
      repository.go
    outbox/
      models.go
      repository.go
    worker/
      outbox_worker.go
docs/
  04_phase4a_outbox_behavior.md
```

## Migration SQL

The migration is in:

```text
backend-go/migrations/000004_phase4a_outbox_behavior.sql
```

It creates:

- `outbox_events`
- `processed_events`
- `behavior_events`

`behavior_events.source_event_id` is unique so worker retries cannot duplicate behavior rows.

## Event Flow

```text
API write request
  -> domain repository transaction
  -> write notes/comments/likes/collects/shares
  -> insert outbox_events in the same transaction
  -> commit

local outbox worker
  -> lock pending outbox_events
  -> write behavior_events idempotently
  -> refresh Redis ranking derived data
  -> insert processed_events
  -> mark outbox_events sent
```

## Event Types

Outbox event types:

- `note.created`
- `comment.created`
- `note.liked`
- `note.collected`
- `note.shared`
- `comment.liked`

Behavior event types:

- `note_created`
- `comment_created`
- `note_liked`
- `note_collected`
- `note_shared`
- `comment_liked`

## Smoke Test

Use a dev token after seedgen:

```powershell
$base = "http://127.0.0.1:18080"
$token = (Import-Csv .\backend-go\tmp\dev_tokens.csv | Select-Object -First 1).token
$headers = @{ Authorization = "Bearer $token" }

$before = docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -t -A -F "," -c "SELECT (SELECT COUNT(*) FROM outbox_events),(SELECT COUNT(*) FROM behavior_events);"

Invoke-RestMethod -Method Post -Uri "$base/api/v1/notes/1/like" -Headers $headers -ContentType "application/json" -Body "{}"
Start-Sleep -Seconds 2

docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -t -A -F "," -c "SELECT status, COUNT(*) FROM outbox_events GROUP BY status ORDER BY status;"
docker exec creatorinsight-postgres psql -U creatorinsight -d creatorinsight -t -A -F "," -c "SELECT event_type, COUNT(*) FROM behavior_events GROUP BY event_type ORDER BY event_type;"
```

Metrics:

```powershell
(Invoke-WebRequest -Uri http://127.0.0.1:18080/metrics).Content |
  Select-String -Pattern "outbox_events_processed_total|behavior_events_recorded_total"
```

## Current Consistency Policy

Phase 4A keeps existing synchronous counters and synchronous cache invalidation/ranking refresh from Phase 3. The new local worker also refreshes ranking from outbox events.

This gives us a safe bridge:

- Existing API behavior remains stable.
- Events are durable and observable.
- Later phases can move counters/rankings fully to asynchronous reconciliation.

## Not In This Phase

- NATS/Kafka publishing.
- Separate worker binary/container.
- Full async counter migration.
- Rate limiting.
- RAG/Agent ingestion.
- Qdrant.
- Recommendation serving.
- Dead-letter UI.

## Verification Results

Local verification on 2026-07-08:

- `go test ./...` passed.
- `.\scripts\build_backend_linux.ps1` passed.
- `docker compose up -d --build` passed.
- `.\scripts\migrate.ps1` applied `000004_phase4a_outbox_behavior.sql`.
- `/ready` returned PostgreSQL and Redis `ok`.
- Comment smoke passed: one `comment.created` outbox event became one `comment_created` behavior event and was marked `sent`.
- Full write-path smoke passed with note id `5001` and comment id `20502`.
- Final post-restart share smoke also passed.
- `outbox_events` status counts: `sent=8`, `pending=0`, `retry=0`, `failed=0`.
- `behavior_events` counts after smoke:
  - `comment_created=2`
  - `comment_liked=1`
  - `note_collected=1`
  - `note_created=1`
  - `note_liked=1`
  - `note_shared=2`
- Redis ranking refresh passed:
  - `ZSCORE ranking:notes:daily 5001` returned `22`.
  - `ZSCORE note:5001:hot_comments 20502` returned `5`.
- `/metrics` exposed after the final container restart:
  - `outbox_events_locked_total 1`
  - `outbox_events_processed_total 1`
  - `outbox_events_failed_total 0`
  - `behavior_events_recorded_total{...}`
  - async ranking labels such as `notes_daily_async` and `hot_comments_async`

## Next Step

Phase 4B should either:

- add write rate limiting and stronger outbox reconciliation, or
- split the local worker into a separate `cmd/worker` process and introduce NATS/Kafka publishing.
