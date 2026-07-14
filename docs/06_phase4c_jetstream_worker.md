# Phase 4C Standalone Worker and NATS JetStream

## Planning Source

`最新项目规划.md` remains the source of truth for the note domain, authentication, and public API. Its detailed roadmap currently stops at Phase 3, so the older Phase 4 event requirements are used only as historical guidance. Video/danmu concepts are not restored.

## Goal

Phase 4C moves asynchronous work out of the API process and adds a durable broker boundary:

```text
authenticated API write
  -> domain table + outbox_events in one PostgreSQL transaction
  -> standalone worker Outbox publisher
  -> NATS JetStream publish acknowledgement
  -> durable pull consumer
  -> behavior event + Redis ranking refresh
  -> processed_events idempotency record
  -> JetStream double acknowledgement
```

The design follows the NATS delivery model: JetStream is at least once, publisher retries can be deduplicated with `Nats-Msg-Id`, and consumer redelivery must still be handled by the application. See the official [JetStream concepts](https://docs.nats.io/nats-concepts/jetstream), [stream configuration](https://docs.nats.io/nats-concepts/jetstream/streams), and [NATS message headers](https://docs.nats.io/nats-concepts/jetstream/headers).

## Process Boundary

The API now owns only HTTP/auth/domain transactions, cache behavior, and synchronous response semantics. It no longer starts the Outbox worker or scheduled reconciler.

`cmd/worker` owns:

- PostgreSQL Outbox publishing;
- JetStream durable consumption;
- `behavior_events` recording;
- asynchronous Redis ranking refresh;
- poison-message retry and dead-letter handling;
- stale Outbox lease recovery;
- scheduled Phase 4B reconciliation;
- worker `/health`, `/ready`, and `/metrics` endpoints.

## JetStream Topology

```text
stream: NOTEINSIGHT_EVENTS
subjects: noteinsight.events.>
storage: file
retention: 7 days
duplicate window: 10 minutes

durable consumer: noteinsight-worker-v1
ack policy: explicit
ack wait: 30 seconds
application delivery limit: 5

stream: NOTEINSIGHT_DLQ
subjects: noteinsight.dlq.>
storage: file
retention: 30 days
```

The main stream and DLQ use the Compose `nats_data` volume.

## Reliability Rules

### Publisher

1. Lock `pending/retry` Outbox rows with `FOR UPDATE SKIP LOCKED`.
2. Synchronously publish the event envelope to JetStream.
3. Set `Nats-Msg-Id` to the PostgreSQL `event_id` and assert the expected stream.
4. Mark the Outbox row `sent` only after the JetStream publish acknowledgement.
5. On failure, use exponential backoff and retain the row for up to 20 attempts.
6. Recover stale `processing` leases using the Phase 4B timeout logic.

If JetStream stores a message but the publisher loses the acknowledgement, the same `event_id` is deduplicated inside the stream window. A later duplicate outside that window is still harmless because of consumer-side database idempotency.

### Consumer

1. Decode the domain event envelope.
2. Check `(event_id, consumer_name)` in `processed_events` before side effects.
3. Record `behavior_events` idempotently and refresh derived ranking data.
4. Insert the `processed_events` row.
5. Use JetStream `DoubleAck` only after the database work succeeds.
6. Use delayed NAK for transient failures.
7. After five deliveries, publish the original message and error to `NOTEINSIGHT_DLQ`, then terminate the original consumer message.

## Directory Changes

```text
backend-go/
  cmd/
    api/main.go                 # no embedded worker
    worker/main.go              # standalone worker process
  internal/
    platform/
      messaging/
        jetstream.go
        jetstream_test.go
    worker/
      event_consumer.go
      event_processor.go
      outbox_publisher.go
      status_server.go
      *_test.go
  bin/
    creatorinsight-api
    creatorinsight-worker
docker-compose.yml              # NATS + worker services and nats_data
```

No Phase 4C database migration is required. Existing `outbox_events`, `processed_events`, and `behavior_events` tables already provide the necessary durable state.

## Configuration

Important defaults:

```text
NATS_URL=nats://localhost:4222
NATS_STREAM=NOTEINSIGHT_EVENTS
NATS_SUBJECT_PREFIX=noteinsight.events
NATS_DLQ_STREAM=NOTEINSIGHT_DLQ
NATS_DLQ_SUBJECT_PREFIX=noteinsight.dlq
NATS_CONSUMER=noteinsight-worker-v1
NATS_ACK_WAIT=30s
NATS_NAK_DELAY=2s
NATS_MAX_DELIVER=5
NATS_DUPLICATE_WINDOW=10m

WORKER_HTTP_PORT=8081
WORKER_CONSUMER_BATCH_SIZE=50
WORKER_METRICS_INTERVAL=5s
OUTBOX_MAX_RETRIES=20
```

Local ports:

- API: `http://127.0.0.1:18080`
- worker status/metrics: `http://127.0.0.1:18081`
- NATS client: `nats://127.0.0.1:14222`
- NATS monitoring: `http://127.0.0.1:18222`

## Metrics

Worker metrics include:

```text
nats_connected
outbox_publish_total{result}
outbox_events{status}
jetstream_messages_consumed_total{event_type,result}
jetstream_redeliveries_total{event_type}
jetstream_dead_letters_total{event_type}
jetstream_consumer_pending_messages
jetstream_consumer_ack_pending_messages
jetstream_consumer_redelivered_messages
```

## Verification

Local verification on 2026-07-14:

- `go test ./...` passed;
- `go vet -p 1 ./...` passed;
- focused messaging/worker/config tests passed;
- API and worker Linux builds passed;
- API `/ready` returned PostgreSQL and Redis `ok`;
- worker `/ready` returned PostgreSQL, Redis, and NATS `ok`;
- JetStream created two file-backed streams and one durable pull consumer;
- an authenticated share request moved through Outbox, JetStream, `behavior_events`, and `processed_events` and ended `sent`;
- re-publishing the same Outbox event inside the duplicate window did not increase the stream message count;
- injecting a second broker message with the same business `event_id` increased stream messages but left behavior/processed row counts at one and emitted `result="duplicate"`;
- a poison event missing `user_id` failed five deliveries, was redelivered four times, entered the DLQ once, and left consumer pending/ack-pending at zero;
- while NATS was stopped, the API still accepted a write and the Outbox retained it; after NATS restarted, the worker reconnected and the event reached `sent` and processed state;
- NATS stream/DLQ data survived the broker container restart.
- final stable state: Outbox `sent=10`, main stream messages `4`, DLQ messages `1`, consumer pending `0`, and ack-pending `0`.

## Start and Inspect

```powershell
.\scripts\build_backend_linux.ps1
docker compose up -d --build
Invoke-RestMethod -Uri http://127.0.0.1:18080/ready
Invoke-RestMethod -Uri http://127.0.0.1:18081/ready
Invoke-RestMethod -Uri "http://127.0.0.1:18222/jsz?streams=true&consumers=true"
```

## Not In This Phase

- Kafka migration or a multi-node NATS cluster.
- Fully asynchronous note/comment counters.
- Replay/DLQ administration API or UI.
- RAG/Agent ingestion consumers.
- Kubernetes worker deployment.
- Schema registry or event-version migration framework.

## Recommended Next Step

Phase 4D should move interaction counters from synchronous increments to idempotent worker-side updates, add event lag and broker-outage k6 scenarios, and keep Phase 4B reconciliation as the repair path. After that, Phase 5 can expand the existing seed generator into the planned persona/Markov/burst Behavior Simulator.
