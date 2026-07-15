package evalbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

var ErrBenchmarkExists = errors.New("retrieval benchmark already exists")

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) LoadSourceDocuments(ctx context.Context, runID string) ([]SourceDocument, error) {
	rows, err := r.db.QueryxContext(ctx, `
SELECT n.id, n.project_id, n.title, n.body, cs.scenario::text
FROM content_scenarios cs
JOIN notes n ON n.id = cs.note_id
WHERE cs.run_id = $1
  AND n.deleted_at IS NULL
  AND n.status = 'published'
ORDER BY n.id`, runID)
	if err != nil {
		return nil, fmt.Errorf("query benchmark source documents: %w", err)
	}
	defer rows.Close()

	documents := make([]SourceDocument, 0)
	for rows.Next() {
		var document SourceDocument
		var scenarioJSON string
		if err := rows.Scan(&document.NoteID, &document.ProjectID, &document.Title, &document.Body, &scenarioJSON); err != nil {
			return nil, fmt.Errorf("scan benchmark source document: %w", err)
		}
		if err := json.Unmarshal([]byte(scenarioJSON), &document.Scenario); err != nil {
			return nil, fmt.Errorf("decode scenario for note %d: %w", document.NoteID, err)
		}
		documents = append(documents, document)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate benchmark source documents: %w", err)
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("source run %q has no active scenario documents", runID)
	}
	return documents, nil
}

func (r *Repository) SaveFrozen(ctx context.Context, benchmark Benchmark) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin benchmark freeze: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if benchmark.Config.CommitmentScheme == NonceCommitmentScheme {
		var status string
		var sourceCount int64
		err := tx.QueryRowContext(ctx, `
SELECT status, source_count
FROM dataset_versions
WHERE id=$1
FOR SHARE`, benchmark.Config.DatasetVersionID).Scan(&status, &sourceCount)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("dataset version %d does not exist", benchmark.Config.DatasetVersionID)
		}
		if err != nil {
			return fmt.Errorf("verify dataset version: %w", err)
		}
		if status != "frozen" || sourceCount <= 0 {
			return fmt.Errorf("dataset version %d must be frozen and non-empty", benchmark.Config.DatasetVersionID)
		}
	}

	assignment := "authored_explicit"
	if benchmark.Config.CommitmentScheme == LegacyCommitmentScheme {
		assignment = "seeded_exact_permutation"
	}
	splitPolicy, err := json.Marshal(map[string]any{
		"development_cases": benchmark.Config.DevelopmentCases,
		"holdout_cases":     benchmark.Config.CaseCount - benchmark.Config.DevelopmentCases,
		"assignment":        assignment,
	})
	if err != nil {
		return fmt.Errorf("encode benchmark split policy: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO retrieval_benchmarks (
    benchmark_id, benchmark_version, source_run_id, generator_version,
    seed, split_policy, status, dataset_version_id, commitment_scheme, created_at
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'building', NULLIF($7, 0), $8, now())`,
		benchmark.Config.BenchmarkID,
		benchmark.Config.BenchmarkVersion,
		benchmark.Config.SourceRunID,
		benchmark.Config.GeneratorVersion,
		benchmark.Config.Seed,
		string(splitPolicy),
		benchmark.Config.DatasetVersionID,
		benchmark.Config.CommitmentScheme,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrBenchmarkExists
		}
		return fmt.Errorf("create retrieval benchmark: %w", err)
	}

	statement, err := tx.PreparexContext(ctx, `
INSERT INTO retrieval_benchmark_cases (
    benchmark_id, split, task_type, query, expected_answer, gold_sources,
    adversarial_tags, provenance, review_status, case_checksum, commitment_hash, metadata, created_at
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, NULLIF($11, ''), $12::jsonb, now())`)
	if err != nil {
		return fmt.Errorf("prepare benchmark case insert: %w", err)
	}
	defer statement.Close()

	for _, evalCase := range benchmark.Cases {
		goldSources, err := json.Marshal(evalCase.GoldSources)
		if err != nil {
			return fmt.Errorf("encode gold sources: %w", err)
		}
		tags, err := json.Marshal(evalCase.AdversarialTags)
		if err != nil {
			return fmt.Errorf("encode adversarial tags: %w", err)
		}
		metadata, err := json.Marshal(evalCase.Metadata)
		if err != nil {
			return fmt.Errorf("encode benchmark metadata: %w", err)
		}
		if _, err := statement.ExecContext(ctx,
			benchmark.Config.BenchmarkID,
			evalCase.Split,
			evalCase.TaskType,
			evalCase.Query,
			evalCase.ExpectedAnswer,
			string(goldSources),
			string(tags),
			evalCase.Provenance,
			evalCase.ReviewStatus,
			evalCase.CaseChecksum,
			evalCase.CommitmentHash,
			string(metadata),
		); err != nil {
			return fmt.Errorf("insert retrieval benchmark case: %w", err)
		}
	}

	result, err := tx.ExecContext(ctx, `
UPDATE retrieval_benchmarks
SET status = 'frozen',
    case_count = $2,
    manifest_checksum = $3,
    frozen_at = now()
WHERE benchmark_id = $1 AND status = 'building'`,
		benchmark.Config.BenchmarkID,
		len(benchmark.Cases),
		benchmark.Manifest.ManifestChecksum,
	)
	if err != nil {
		return fmt.Errorf("freeze retrieval benchmark: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read freeze result: %w", err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("freeze retrieval benchmark affected %d rows", rowsAffected)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retrieval benchmark: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
