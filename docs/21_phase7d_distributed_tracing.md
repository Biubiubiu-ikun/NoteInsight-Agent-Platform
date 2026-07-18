# Phase 7D Distributed Tracing

Updated: 2026-07-18

## Scope

Phase 7D now exports W3C Trace Context through the complete local data path:

`Gin HTTP -> PostgreSQL/Redis -> transactional Outbox -> NATS JetStream -> Worker -> PostgreSQL/Redis`

Retrieval requests additionally include TEI and Qdrant HTTP client spans. Health, readiness and metrics endpoints are excluded. Tracing is disabled by default and enabled by the observability Compose override.

## Components

- OpenTelemetry Go SDK `v1.44.0`, `otelgin v0.69.0`, `otelhttp v0.69.0`;
- `otelsql v0.43.0`, with SQL text, parameters and row spans disabled;
- `redisotel v9.21.0`, with command values disabled and repetitive `SCAN` spans filtered;
- OpenTelemetry Collector Contrib `0.156.0` with memory limiting and batching;
- Grafana Tempo `3.0.2` in local monolithic mode and a provisioned Grafana Tempo data source.

The Tempo 3.0 configuration intentionally uses the new monolithic contract. Removed 2.x `ingester` and `compactor` blocks are not present. Local Tempo has no authentication and is for development only; a cloud deployment must put an authenticated private endpoint in front of the trace backend.

## Durable Async Context

Migration `000023_phase7d_trace_context.sql` adds nullable `traceparent` and `tracestate` columns to `outbox_events`. The API injects the current W3C context inside the same transaction as the business mutation. The publisher reconstructs that parent, creates a producer span and injects a case-safe NATS header. The consumer extracts it before decode/process/ack/DLQ handling.

The existing `trace_id` remains available for logs and lineage. `traceparent` is the causal propagation contract across the durable delay.

## Privacy And Cardinality

- SQL query text and bind values are never exported.
- Redis command statements, keys and values are never exported.
- Note text, OCR, comments, JWTs, refresh tokens, API keys and event payloads are not span attributes.
- Span names and attributes use bounded route, dependency, operation and event-type values.
- SQL spans require an existing business trace, preventing worker polling from generating unrelated root traces.
- Default sampling is parent-based 10 percent; local observability uses 100 percent for deterministic verification.

## Verified Evidence

The local acceptance mutation created note `5548` and returned trace ID `903741a95c1ed196dbcd3cbd1f00b86a`. Its Outbox row reached `sent` and stored:

`00-903741a95c1ed196dbcd3cbd1f00b86a-74ba37ebb05005af-01`

Tempo returned 37 spans from both `creatorinsight-api` and `noteinsight-worker`, including the HTTP server span, SQL transaction, Redis operations, `nats publish`, `nats consume`, worker SQL/Redis and acknowledgable processing. No Redis `SCAN` span remained.

Hybrid retrieval trace `5316cae36621eca92abd4481b4dfe69a` returned HTTP 200 and included `sql.conn.query`, `tei POST` and `qdrant query` spans.

## Run And Verify

```powershell
docker compose -f docker-compose.yml `
  -f deploy/observability/docker-compose.observability.yml up -d --build

Invoke-RestMethod http://127.0.0.1:13133
Invoke-RestMethod http://127.0.0.1:13200/ready
Invoke-RestMethod http://127.0.0.1:13000/api/health

cd backend-go
$env:OTEL_TEST_ENDPOINT = "127.0.0.1:14317"
go test -run TestOTLPExporterSmoke -count=1 -v ./internal/platform/tracing
```

Open Grafana at `http://127.0.0.1:13000/`, select Explore, choose the `Tempo` data source and search by trace ID or service name.

## Configuration Validation

CI validates Compose, Collector and Tempo configuration:

```powershell
docker run --rm `
  -v "$((Get-Location).Path)/deploy/observability/otel-collector.yml:/etc/otelcol-contrib/config.yml:ro" `
  otel/opentelemetry-collector-contrib:0.156.0 `
  validate --config=/etc/otelcol-contrib/config.yml

docker run --rm `
  -v "$((Get-Location).Path)/deploy/observability/tempo.yml:/etc/tempo.yml:ro" `
  grafana/tempo:3.0.2 `
  --config.file=/etc/tempo.yml --config.verify=true
```

## Remaining Production Gates

- Export to a managed or authenticated private trace backend over TLS.
- Establish production sampling, retention, redaction, cost and access-control policies.
- Correlate traces with deployment SLOs and production-like multi-instance capacity evidence.
- Keep local Tempo as a diagnostic environment, not a production architecture claim.
