package evalbench

import (
	"context"
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

	splitPolicy, err := json.Marshal(map[string]any{
		"development_cases": benchmark.Config.DevelopmentCases,
		"holdout_cases":     benchmark.Config.CaseCount - benchmark.Config.DevelopmentCases,
		"assignment":        "seeded_exact_permutation",
	})
	if err != nil {
		return fmt.Errorf("encode benchmark split policy: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO retrieval_benchmarks (
    benchmark_id, benchmark_version, source_run_id, generator_version,
    seed, split_policy, status, created_at
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'building', now())`,
		benchmark.Config.BenchmarkID,
		benchmark.Config.BenchmarkVersion,
		benchmark.Config.SourceRunID,
		benchmark.Config.GeneratorVersion,
		benchmark.Config.Seed,
		string(splitPolicy),
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
    adversarial_tags, provenance, review_status, case_checksum, metadata, created_at
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11::jsonb, now())`)
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
