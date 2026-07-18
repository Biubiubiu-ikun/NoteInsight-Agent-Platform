package dataset

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type CorpusDatasetResult struct {
	DatasetID   int64  `json:"dataset_id"`
	ProjectID   int64  `json:"project_id"`
	Slug        string `json:"slug"`
	SourceRunID string `json:"source_run_id"`
	NoteCount   int64  `json:"note_count"`
	SourceCount int64  `json:"source_count"`
}

func (r *Repository) PrepareCorpusDataset(ctx context.Context, projectID int64, slug string, name string, sourceRunID string, createdBy int64) (CorpusDatasetResult, error) {
	slug = strings.TrimSpace(slug)
	name = strings.TrimSpace(name)
	sourceRunID = strings.TrimSpace(sourceRunID)
	if projectID <= 0 || slug == "" || name == "" || sourceRunID == "" {
		return CorpusDatasetResult{}, fmt.Errorf("project_id, slug, name, and source_run_id are required")
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return CorpusDatasetResult{}, fmt.Errorf("begin corpus dataset preparation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "corpus-dataset:"+slug); err != nil {
		return CorpusDatasetResult{}, fmt.Errorf("lock corpus dataset preparation: %w", err)
	}
	var runStatus string
	if err := tx.QueryRowxContext(ctx, `SELECT status FROM content_corpus_runs WHERE run_id=$1`, sourceRunID).Scan(&runStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CorpusDatasetResult{}, fmt.Errorf("content corpus run not found")
		}
		return CorpusDatasetResult{}, fmt.Errorf("load content corpus run: %w", err)
	}
	if runStatus != "completed" {
		return CorpusDatasetResult{}, fmt.Errorf("content corpus run is not completed")
	}
	var datasetID int64
	if err := tx.QueryRowxContext(ctx, `
INSERT INTO datasets (project_id,slug,name,description,status,created_by)
VALUES ($1,$2,$3,$4,'active',NULLIF($5,0))
ON CONFLICT (project_id,slug) DO UPDATE
SET name=EXCLUDED.name, description=EXCLUDED.description, updated_at=clock_timestamp()
RETURNING id`, projectID, slug, name,
		"Evidence sources selected from immutable content corpus run "+sourceRunID, createdBy,
	).Scan(&datasetID); err != nil {
		return CorpusDatasetResult{}, fmt.Errorf("upsert corpus dataset: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dataset_source_memberships WHERE dataset_id=$1`, datasetID); err != nil {
		return CorpusDatasetResult{}, fmt.Errorf("clear corpus dataset source membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dataset_notes WHERE dataset_id=$1`, datasetID); err != nil {
		return CorpusDatasetResult{}, fmt.Errorf("clear corpus dataset notes: %w", err)
	}
	notesResult, err := tx.ExecContext(ctx, `
INSERT INTO dataset_notes (dataset_id,note_id,note_version)
SELECT $1, n.id, n.content_version
FROM content_scenarios scenario
JOIN notes n ON n.id=scenario.note_id
WHERE scenario.run_id=$2 AND n.project_id=$3
ORDER BY n.id`, datasetID, sourceRunID, projectID)
	if err != nil {
		return CorpusDatasetResult{}, fmt.Errorf("add corpus dataset notes: %w", err)
	}
	sourcesResult, err := tx.ExecContext(ctx, `
INSERT INTO dataset_source_memberships (
    dataset_id,evidence_source_id,membership_reason,source_run_id,added_by
)
SELECT DISTINCT $1::bigint, es.id, 'content_corpus_run', $2::varchar, NULLIF($4::bigint,0)
FROM content_scenarios scenario
JOIN evidence_sources es ON es.project_id=$3
JOIN evidence_source_payloads payload ON payload.evidence_source_id=es.id
WHERE scenario.run_id=$2
  AND es.deleted_at IS NULL AND es.index_status<>'deleted'
  AND (
      (es.source_type='note' AND es.source_id=scenario.note_id)
      OR (es.source_type IN ('note_media','note_comment')
          AND NULLIF(payload.source_payload->>'note_id','')::bigint=scenario.note_id)
  )
ORDER BY es.id`, datasetID, sourceRunID, projectID, createdBy)
	if err != nil {
		return CorpusDatasetResult{}, fmt.Errorf("add corpus dataset evidence sources: %w", err)
	}
	noteCount, _ := notesResult.RowsAffected()
	sourceCount, _ := sourcesResult.RowsAffected()
	if noteCount == 0 || sourceCount == 0 {
		return CorpusDatasetResult{}, fmt.Errorf("content corpus run produced an empty dataset")
	}
	if err := tx.Commit(); err != nil {
		return CorpusDatasetResult{}, fmt.Errorf("commit corpus dataset preparation: %w", err)
	}
	return CorpusDatasetResult{
		DatasetID: datasetID, ProjectID: projectID, Slug: slug,
		SourceRunID: sourceRunID, NoteCount: noteCount, SourceCount: sourceCount,
	}, nil
}
