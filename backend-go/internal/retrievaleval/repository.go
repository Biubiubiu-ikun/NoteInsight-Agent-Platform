package retrievaleval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"creatorinsight/backend-go/internal/evalbench"

	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Save(ctx context.Context, config Config, manifest evalbench.Manifest, report Report) error {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin retrieval evaluation save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode retrieval evaluation config: %w", err)
	}
	var releaseID any
	if config.ReleaseID != "" {
		releaseID = config.ReleaseID
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO retrieval_eval_runs (
    run_id, benchmark_id, benchmark_manifest_checksum, split, release_id,
    ingestion_run_id, dataset_version_id, retriever_version, reranker_version,
    metric_version, config, config_checksum, status, started_at, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'running',$13,clock_timestamp())`,
		report.RunID, report.BenchmarkID, report.BenchmarkManifestChecksum,
		report.Split, releaseID, report.Scope.IngestionRunID, report.Scope.DatasetVersionID,
		report.RetrieverVersion, report.RerankerVersion, report.MetricVersion,
		configJSON, report.ConfigChecksum, report.StartedAt,
	); err != nil {
		return fmt.Errorf("create retrieval evaluation run: %w", err)
	}
	for _, result := range report.Cases {
		gold, err := json.Marshal(result.GoldSources)
		if err != nil {
			return fmt.Errorf("encode gold sources for case %s: %w", result.CaseChecksum, err)
		}
		retrieved, err := json.Marshal(result.RetrievedSources)
		if err != nil {
			return fmt.Errorf("encode retrieved sources for case %s: %w", result.CaseChecksum, err)
		}
		caseMetrics, err := json.Marshal(result.Metrics)
		if err != nil {
			return fmt.Errorf("encode metrics for case %s: %w", result.CaseChecksum, err)
		}
		var failure any
		if result.FailureCategory != "" {
			failure = result.FailureCategory
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO retrieval_eval_case_results (
    eval_run_id, case_checksum, task_type, answerable, gold_sources,
    retrieved_sources, metrics, failure_category, latency_ms, result_count
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			report.RunID, result.CaseChecksum, result.TaskType, result.Answerable,
			gold, retrieved, caseMetrics, failure, result.LatencyMilliseconds,
			result.ResultCount,
		); err != nil {
			return fmt.Errorf("save retrieval case %s: %w", result.CaseChecksum, err)
		}
	}
	metricsJSON, err := json.Marshal(report.Metrics)
	if err != nil {
		return fmt.Errorf("encode retrieval evaluation metrics: %w", err)
	}
	failuresJSON, err := json.Marshal(report.FailureCounts)
	if err != nil {
		return fmt.Errorf("encode retrieval evaluation failures: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE retrieval_eval_runs
SET status='completed', case_count=$2, metrics=$3, failure_counts=$4,
    completed_at=$5
WHERE run_id=$1 AND status='running'`, report.RunID, len(report.Cases),
		metricsJSON, failuresJSON, report.CompletedAt,
	); err != nil {
		return fmt.Errorf("complete retrieval evaluation run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval evaluation run: %w", err)
	}
	return nil
}
