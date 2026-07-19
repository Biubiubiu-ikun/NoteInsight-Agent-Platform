package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetPromptVersion(ctx context.Context, key string, version string) (PromptVersion, error) {
	defer observability.ObserveDB("agent_prompt_get", time.Now())
	var prompt PromptVersion
	err := r.db.QueryRowxContext(ctx, `
SELECT id, prompt_key, version, purpose, template_sha256, created_at
FROM agent_prompt_versions
WHERE prompt_key=$1 AND version=$2 AND status='frozen'`, key, version).Scan(
		&prompt.ID, &prompt.PromptKey, &prompt.Version, &prompt.Purpose,
		&prompt.TemplateSHA256, &prompt.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PromptVersion{}, ErrScopeUnavailable
	}
	if err != nil {
		return PromptVersion{}, fmt.Errorf("load agent prompt version: %w", err)
	}
	return prompt, nil
}

func (r *Repository) CreateRun(ctx context.Context, input createRecord) (Run, bool, error) {
	defer observability.ObserveDB("agent_run_create", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return Run{}, false, fmt.Errorf("begin agent run create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	retrievalPlan, err := json.Marshal(input.RetrievalPlan)
	if err != nil {
		return Run{}, false, fmt.Errorf("marshal agent retrieval plan: %w", err)
	}
	var idempotencyKey any
	if input.IdempotencyKey != "" {
		idempotencyKey = input.IdempotencyKey
	}
	var runID string
	err = tx.QueryRowxContext(ctx, `
INSERT INTO agent_runs (
    project_id, dataset_version_id, ingestion_run_id, requested_by,
    query, requested_mode, intent, retrieval_plan, prompt_version_id,
    max_steps, max_retrieval_calls, max_model_calls, max_input_tokens,
    max_output_tokens, max_duration_ms, max_cost_micros,
    idempotency_key, request_hash, request_id, trace_id
) VALUES (
    $1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,
    $10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20
)
ON CONFLICT (requested_by, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
RETURNING id::text`,
		input.ProjectID, input.DatasetVersionID, input.IngestionRunID, input.RequestedBy,
		input.Query, input.Mode, string(input.Intent), string(retrievalPlan), input.PromptVersionID,
		input.Budget.MaxSteps, input.Budget.MaxRetrievalCalls, input.Budget.MaxModelCalls,
		input.Budget.MaxInputTokens, input.Budget.MaxOutputTokens, input.Budget.MaxDurationMS,
		input.Budget.MaxCostMicros, idempotencyKey, input.RequestHash,
		nullableString(input.RequestID), nullableString(input.TraceID),
	).Scan(&runID)
	replayed := false
	if errors.Is(err, sql.ErrNoRows) {
		if input.IdempotencyKey == "" {
			return Run{}, false, fmt.Errorf("create agent run returned no row")
		}
		var existingHash string
		if lookupErr := tx.QueryRowxContext(ctx, `
SELECT id::text, request_hash
FROM agent_runs
WHERE requested_by=$1 AND idempotency_key=$2`, input.RequestedBy, input.IdempotencyKey).Scan(
			&runID, &existingHash,
		); lookupErr != nil {
			return Run{}, false, fmt.Errorf("load idempotent agent run: %w", lookupErr)
		}
		if existingHash != input.RequestHash {
			return Run{}, false, ErrIdempotencyConflict
		}
		replayed = true
	} else if err != nil {
		return Run{}, false, fmt.Errorf("insert agent run: %w", err)
	}

	run, err := scanRun(tx.QueryRowxContext(ctx, runSelectSQL()+` WHERE ar.id=$1`, runID))
	if err != nil {
		return Run{}, false, fmt.Errorf("load created agent run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Run{}, false, fmt.Errorf("commit agent run create: %w", err)
	}
	return run, replayed, nil
}

func (r *Repository) GetRun(ctx context.Context, runID string, userID int64, admin bool) (Run, error) {
	defer observability.ObserveDB("agent_run_get", time.Now())
	run, err := scanRun(r.db.QueryRowxContext(ctx, runSelectSQL()+`
WHERE ar.id=$1 AND ($3 OR ar.requested_by=$2)`, runID, userID, admin))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("load agent run: %w", err)
	}
	return run, nil
}

func (r *Repository) ListRuns(
	ctx context.Context,
	userID int64,
	admin bool,
	limit int,
	cursor *runCursor,
) ([]Run, error) {
	defer observability.ObserveDB("agent_run_list", time.Now())
	var cursorTime any
	var cursorID any
	if cursor != nil {
		cursorTime = cursor.CreatedAt
		cursorID = cursor.ID
	}
	rows, err := r.db.QueryxContext(ctx, runSelectSQL()+`
WHERE ($2 OR ar.requested_by=$1)
  AND ($4::timestamptz IS NULL OR (ar.created_at, ar.id) < ($4::timestamptz, $5::uuid))
ORDER BY ar.created_at DESC, ar.id DESC
LIMIT $3`, userID, admin, limit, cursorTime, cursorID)
	if err != nil {
		return nil, fmt.Errorf("list agent runs: %w", err)
	}
	defer rows.Close()
	runs := make([]Run, 0, limit)
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan agent run page: %w", scanErr)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent run page: %w", err)
	}
	return runs, nil
}

func (r *Repository) CancelRun(ctx context.Context, runID string, userID int64, admin bool) (Run, error) {
	defer observability.ObserveDB("agent_run_cancel", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return Run{}, fmt.Errorf("begin agent run cancel: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var updatedID string
	err = tx.QueryRowxContext(ctx, `
UPDATE agent_runs
SET cancellation_requested=TRUE,
    status=CASE WHEN status='queued' THEN 'cancelled' ELSE status END,
    completed_at=CASE WHEN status='queued' THEN clock_timestamp() ELSE completed_at END
WHERE id=$1 AND ($3 OR requested_by=$2) AND status IN ('queued','running')
RETURNING id::text`, runID, userID, admin).Scan(&updatedID)
	if errors.Is(err, sql.ErrNoRows) {
		var status string
		lookupErr := tx.QueryRowxContext(ctx, `
SELECT status FROM agent_runs WHERE id=$1 AND ($3 OR requested_by=$2)`, runID, userID, admin).Scan(&status)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		if lookupErr != nil {
			return Run{}, fmt.Errorf("load agent run cancel state: %w", lookupErr)
		}
		return Run{}, fmt.Errorf("%w: run is already %s", ErrConflict, status)
	}
	if err != nil {
		return Run{}, fmt.Errorf("cancel agent run: %w", err)
	}
	run, err := scanRun(tx.QueryRowxContext(ctx, runSelectSQL()+` WHERE ar.id=$1`, updatedID))
	if err != nil {
		return Run{}, fmt.Errorf("load cancelled agent run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit agent run cancel: %w", err)
	}
	return run, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanRun(row rowScanner) (Run, error) {
	var run Run
	var intentText, retrievalPlanText, reportText string
	var idempotencyKey, requestID, traceID, failureCode, failureMessage sql.NullString
	var startedAt, completedAt sql.NullTime
	var modelID sql.NullInt64
	var modelProvider, modelName, modelRevision, modelParameters, modelArtifact sql.NullString
	var modelCreatedAt sql.NullTime
	err := row.Scan(
		&run.ID, &run.ProjectID, &run.DatasetVersionID, &run.IngestionRunID,
		&run.RequestedBy, &run.Query, &run.RequestedMode, &intentText,
		&retrievalPlanText, &reportText, &run.Prompt.ID, &run.Prompt.PromptKey,
		&run.Prompt.Version, &run.Prompt.Purpose, &run.Prompt.TemplateSHA256,
		&run.Prompt.CreatedAt, &modelID, &modelProvider, &modelName, &modelRevision,
		&modelParameters, &modelArtifact, &modelCreatedAt, &run.Status,
		&run.Budget.MaxSteps, &run.Budget.MaxRetrievalCalls, &run.Budget.MaxModelCalls,
		&run.Budget.MaxInputTokens, &run.Budget.MaxOutputTokens, &run.Budget.MaxDurationMS,
		&run.Budget.MaxCostMicros, &run.Usage.Steps, &run.Usage.RetrievalCalls,
		&run.Usage.ModelCalls, &run.Usage.InputTokens, &run.Usage.OutputTokens,
		&run.Usage.CostMicros, &run.CancellationRequested, &idempotencyKey,
		&requestID, &traceID, &failureCode, &failureMessage, &startedAt,
		&completedAt, &run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		return Run{}, err
	}
	run.Intent = json.RawMessage(intentText)
	if err := json.Unmarshal([]byte(retrievalPlanText), &run.RetrievalPlan); err != nil {
		return Run{}, fmt.Errorf("decode agent retrieval plan: %w", err)
	}
	if reportText != "" {
		run.Report = json.RawMessage(reportText)
	}
	if modelID.Valid {
		run.Model = &ModelVersion{
			ID: modelID.Int64, Provider: modelProvider.String, Model: modelName.String,
			Revision: modelRevision.String, Parameters: json.RawMessage(modelParameters.String),
			ArtifactSHA256: modelArtifact.String, CreatedAt: modelCreatedAt.Time,
		}
	}
	run.IdempotencyKey = idempotencyKey.String
	run.RequestID = requestID.String
	run.TraceID = traceID.String
	run.FailureCode = failureCode.String
	run.FailureMessage = failureMessage.String
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	return run, nil
}

func runSelectSQL() string {
	return `
SELECT ar.id::text, ar.project_id, ar.dataset_version_id, ar.ingestion_run_id,
       ar.requested_by, ar.query, ar.requested_mode, ar.intent::text,
       ar.retrieval_plan::text, COALESCE(ar.report::text,''),
       ap.id, ap.prompt_key, ap.version, ap.purpose, ap.template_sha256, ap.created_at,
       am.id, am.provider, am.model, am.revision, am.parameters::text,
       am.artifact_sha256, am.created_at,
       ar.status, ar.max_steps, ar.max_retrieval_calls, ar.max_model_calls,
       ar.max_input_tokens, ar.max_output_tokens, ar.max_duration_ms, ar.max_cost_micros,
       ar.used_steps, ar.used_retrieval_calls, ar.used_model_calls,
       ar.used_input_tokens, ar.used_output_tokens, ar.used_cost_micros,
       ar.cancellation_requested, ar.idempotency_key, ar.request_id, ar.trace_id,
       ar.failure_code, ar.failure_message, ar.started_at, ar.completed_at,
       ar.created_at, ar.updated_at
FROM agent_runs ar
JOIN agent_prompt_versions ap ON ap.id=ar.prompt_version_id
LEFT JOIN agent_model_versions am ON am.id=ar.model_version_id`
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
