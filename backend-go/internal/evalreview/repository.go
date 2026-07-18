package evalreview

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type SourceResolver interface {
	ResolveSources(ctx context.Context, datasetVersionID int64, ingestionRunID string, refs []CandidateRef) ([]CandidateSource, error)
}

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) ResolveSources(ctx context.Context, datasetVersionID int64, ingestionRunID string, refs []CandidateRef) ([]CandidateSource, error) {
	if datasetVersionID <= 0 || !validIdentifier(ingestionRunID) || len(refs) == 0 {
		return nil, fmt.Errorf("dataset version, ingestion run, and source references are required")
	}
	rawRefs, err := json.Marshal(refs)
	if err != nil {
		return nil, fmt.Errorf("encode candidate source references: %w", err)
	}
	rows, err := r.db.QueryxContext(ctx, `
WITH requested AS (
    SELECT source_type, source_id, source_version
    FROM jsonb_to_recordset($3::jsonb)
      AS item(source_type text, source_id bigint, source_version bigint)
)
SELECT dvs.source_type, dvs.source_id, dvs.source_version, dvs.visibility,
       dvs.content_hash, esp.canonical_text, esp.source_payload
FROM requested req
JOIN dataset_version_sources dvs
  ON dvs.dataset_version_id=$1
 AND dvs.source_type=req.source_type
 AND dvs.source_id=req.source_id
 AND dvs.source_version=req.source_version
JOIN evidence_source_payloads esp ON esp.evidence_source_id=dvs.evidence_source_id
WHERE EXISTS (
    SELECT 1
    FROM ingestion_runs ir
    JOIN ingestion_run_documents ird ON ird.run_id=ir.run_id
    JOIN source_citations sc ON sc.document_id=ird.document_id
    WHERE ir.run_id=$2
      AND ir.dataset_version_id=$1
      AND ir.status='completed'
      AND sc.evidence_source_id=dvs.evidence_source_id
)
ORDER BY dvs.source_type, dvs.source_id, dvs.source_version`, datasetVersionID, ingestionRunID, rawRefs)
	if err != nil {
		return nil, fmt.Errorf("resolve review candidate sources: %w", err)
	}
	defer rows.Close()

	resolved := make([]CandidateSource, 0, len(refs))
	for rows.Next() {
		var source CandidateSource
		var payloadRaw []byte
		if err := rows.Scan(
			&source.SourceType, &source.SourceID, &source.SourceVersion,
			&source.Visibility, &source.ContentHash, &source.CanonicalText, &payloadRaw,
		); err != nil {
			return nil, fmt.Errorf("scan review candidate source: %w", err)
		}
		var payload struct {
			NoteID   int64 `json:"note_id"`
			Position int   `json:"position"`
		}
		if err := json.Unmarshal(payloadRaw, &payload); err != nil {
			return nil, fmt.Errorf("decode candidate source payload %s:%d:%d: %w", source.SourceType, source.SourceID, source.SourceVersion, err)
		}
		if source.SourceType == "note" || source.SourceType == "note_body" {
			source.NoteID = source.SourceID
		} else {
			source.NoteID = payload.NoteID
			source.Position = payload.Position
		}
		if source.NoteID <= 0 {
			return nil, fmt.Errorf("candidate source %s:%d:%d has no note identity", source.SourceType, source.SourceID, source.SourceVersion)
		}
		source.DatasetVersionID = datasetVersionID
		source.IngestionRunID = ingestionRunID
		resolved = append(resolved, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review candidate sources: %w", err)
	}
	if len(resolved) != len(refs) {
		return nil, fmt.Errorf("resolved %d of %d candidate sources from the frozen ingestion snapshot", len(resolved), len(refs))
	}
	return resolved, nil
}
