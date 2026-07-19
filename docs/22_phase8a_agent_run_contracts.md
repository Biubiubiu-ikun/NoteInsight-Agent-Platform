# Phase 8A Agent Run Contracts

Updated: 2026-07-19

## Purpose

Phase 8A establishes the durable control plane for one evidence-grounded Insight Agent. It freezes who requested a run, which immutable evidence snapshot and retrieval indexes it may use, which prompt governed it, what budget applies, and how later tool calls and factual claims must link to exact citations.

This phase does not call an LLM, execute retrieval, or produce an insight report. A newly created run remains `queued` until the Phase 8B executor is added.

## Blind Review Dependency

Benchmark v5 human review is not a dependency for the run schema, authorization boundary, budget enforcement, lineage storage, cancellation contract, or API. Those controls can be built and tested against the existing immutable Phase 7 retrieval baseline without seeing benchmark labels.

Human review still blocks:

- tuning or promoting a retrieval implementation;
- publishing a v5 retrieval-quality claim;
- tuning Agent prompts against holdout outcomes;
- publishing a formal Agent-quality claim.

Phase 8B may implement the bounded workflow against the unchanged baseline while review proceeds. The development split must be frozen before ranker or prompt quality tuning, and the holdout remains sealed until formal evaluation.

## Database Contract

Migrations `000024-000028` add:

- `agent_prompt_versions`: immutable prompt content with SHA-256 identity and one-way retirement;
- `agent_model_versions`: immutable provider/model/revision/parameter and optional artifact checksum lineage;
- `agent_runs`: requester, immutable retrieval scope, prompt, status, hard budget, usage, cancellation, request/trace identity and idempotency hash;
- `agent_tool_calls`: ordered attempts with tool version, input/output, status, error and trace lineage;
- `agent_claims`: ordered factual claims and support state;
- `agent_claim_citations`: exact links to `source_citations`, guarded to the run's project and dataset version.

The database enforces completed ingestion scope matching, frozen prompt/model references at creation/dispatch, one-way definition retirement without breaking historical runs, valid state transitions, required model binding at dispatch, bounded usage counters, update/delete-resistant audit lineage, citation ingestion membership and quote-hash equality, supported/cited claims before success, and `(requested_by, idempotency_key)` uniqueness.

## API

All endpoints require JWT authentication. Creation and cancellation additionally require an active user and use the `agent_run` user rate limit.

```text
POST /api/v1/agent/runs
GET  /api/v1/agent/runs?limit=20&cursor=...
GET  /api/v1/agent/runs/{run_id}
POST /api/v1/agent/runs/{run_id}/cancel
```

Only the requester can read or cancel a run; admins may inspect or cancel any run. A reused `Idempotency-Key` returns the original run only when the complete normalized request hash matches, otherwise it returns `409`.

## Smoke Flow

After login, use a completed Phase 7 dataset/ingestion pair:

```powershell
$headers = @{
  Authorization = "Bearer $accessToken"
  "Idempotency-Key" = "agent-smoke-001"
}
$body = @{
  project_id = 1
  dataset_version_id = 2
  ingestion_run_id = "phase7a_dv2_rebuild_v2_20260718"
  query = "分析近期创作者反馈中的主要机会和风险"
  mode = "lexical"
  budget = @{
    max_steps = 8
    max_retrieval_calls = 4
    max_model_calls = 4
    max_input_tokens = 32000
    max_output_tokens = 4096
    max_duration_ms = 120000
    max_cost_micros = 50000
  }
} | ConvertTo-Json -Depth 4

$created = Invoke-RestMethod -Method Post `
  -Uri http://127.0.0.1:18080/api/v1/agent/runs `
  -Headers $headers -ContentType application/json -Body $body

Invoke-RestMethod -Headers @{ Authorization = "Bearer $accessToken" } `
  -Uri "http://127.0.0.1:18080/api/v1/agent/runs/$($created.run.id)"
```

The expected Phase 8A result is `status=queued`, zero usage, a frozen prompt checksum, and exact dataset/ingestion/index checksums. No model token or cost is consumed.

## Verification

```powershell
cd backend-go
go test ./...
go test -tags=integration -count=1 ./integration
go vet ./...
```

The targeted real PostgreSQL test is `TestPhase8AAgentRunLineageIdempotencyAndIsolation`.

## Phase 8B Boundary

Phase 8B will add a bounded executor for intent parsing, retrieval planning, evidence collection, structured analysis, claim-level citation validation, and report completion. It must use the persisted budget and cancellation state, bind a frozen model revision once at dispatch, write tool attempts transactionally, and fail the run if a factual claim has no valid citation.
