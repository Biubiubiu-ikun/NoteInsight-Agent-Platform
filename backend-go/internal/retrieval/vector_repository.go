package retrieval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (r *Repository) LoadVectorIndex(ctx context.Context, ingestionRunID string) (VectorIndex, error) {
	var index VectorIndex
	err := scanVectorIndex(r.db.QueryRowxContext(ctx, vectorIndexSelectSQL()+`
WHERE ingestion_run_id=$1 AND index_version=$2`, ingestionRunID, VectorIndexVersion), &index)
	if errors.Is(err, sql.ErrNoRows) {
		return VectorIndex{}, ErrIndexNotReady
	}
	if err != nil {
		return VectorIndex{}, fmt.Errorf("load vector index: %w", err)
	}
	return index, nil
}

func (r *Repository) StartVectorIndex(ctx context.Context, index VectorIndex) error {
	result, err := r.db.ExecContext(ctx, `
INSERT INTO retrieval_vector_indexes (
    ingestion_run_id,index_version,embedding_model,embedding_revision,
    vector_dimension,distance_metric,collection_name,status,point_count,
    started_at,created_at,updated_at
)
SELECT ir.run_id,$2,$3,$4,$5,'Cosine',$6,'building',0,
       clock_timestamp(),clock_timestamp(),clock_timestamp()
FROM ingestion_runs ir
WHERE ir.run_id=$1 AND ir.status='completed'
ON CONFLICT (ingestion_run_id,index_version) DO UPDATE
SET embedding_model=EXCLUDED.embedding_model,
    embedding_revision=EXCLUDED.embedding_revision,
    vector_dimension=EXCLUDED.vector_dimension,
    distance_metric=EXCLUDED.distance_metric,
    collection_name=EXCLUDED.collection_name,
    status='building',point_count=0,index_checksum=NULL,error_message=NULL,
    started_at=clock_timestamp(),completed_at=NULL,updated_at=clock_timestamp()
WHERE retrieval_vector_indexes.status<>'completed'`,
		index.IngestionRunID, index.IndexVersion, index.EmbeddingModel,
		index.EmbeddingRevision, index.VectorDimension, index.CollectionName,
	)
	if err != nil {
		return fmt.Errorf("start vector index: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read vector index start result: %w", err)
	}
	if rows == 0 {
		return ErrIndexVersionMismatch
	}
	return nil
}

func (r *Repository) CompleteVectorIndex(ctx context.Context, ingestionRunID string, pointCount int64, checksum string) (VectorIndex, error) {
	var index VectorIndex
	err := scanVectorIndex(r.db.QueryRowxContext(ctx, `
UPDATE retrieval_vector_indexes
SET status='completed',point_count=$3,index_checksum=$4,
    completed_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE ingestion_run_id=$1 AND index_version=$2 AND status='building'
RETURNING ingestion_run_id,index_version,embedding_model,embedding_revision,
          vector_dimension,distance_metric,collection_name,status,point_count,
          COALESCE(index_checksum,''),started_at,completed_at`,
		ingestionRunID, VectorIndexVersion, pointCount, checksum), &index)
	if err != nil {
		return VectorIndex{}, fmt.Errorf("complete vector index: %w", err)
	}
	return index, nil
}

func (r *Repository) MarkVectorIndexFailed(ingestionRunID string, cause error) {
	message := "unknown vector index failure"
	if cause != nil {
		message = cause.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.db.ExecContext(ctx, `
UPDATE retrieval_vector_indexes
SET status='failed',error_message=left($3,4000),completed_at=NULL,updated_at=clock_timestamp()
WHERE ingestion_run_id=$1 AND index_version=$2 AND status<>'completed'`,
		ingestionRunID, VectorIndexVersion, message)
}

func (r *Repository) ListVectorChunks(ctx context.Context, ingestionRunID string, afterChunkID int64, limit int) ([]VectorChunk, error) {
	rows, err := r.db.QueryxContext(ctx, `
SELECT c.id,c.content,c.content_hash,d.id,d.document_type,d.source_type,
       d.source_id,d.source_version,
       CASE
         WHEN d.source_type IN ('note','note_comment_cluster','note_daily_fact') THEN d.source_id
         WHEN d.source_type='note_media' THEN NULLIF(esp.source_payload->>'note_id','')::bigint
         ELSE NULL
       END AS note_id,
       CASE WHEN d.source_type='note_media'
            THEN NULLIF(esp.source_payload->>'position','')::int ELSE NULL END AS media_position,
       d.project_id,p.visibility,d.visibility,d.lifecycle_status
FROM ingestion_run_documents rd
JOIN evidence_documents d ON d.id=rd.document_id
JOIN projects p ON p.id=d.project_id AND p.status='active'
JOIN evidence_chunks c ON c.document_id=d.id
LEFT JOIN evidence_document_sources primary_source
  ON primary_source.document_id=d.id AND primary_source.source_order=0
LEFT JOIN evidence_source_payloads esp
  ON esp.evidence_source_id=primary_source.evidence_source_id
WHERE rd.run_id=$1 AND c.id>$2
  AND d.lifecycle_status IN ('ready','superseded')
  AND NOT EXISTS (
      SELECT 1
      FROM evidence_document_sources document_source
      JOIN evidence_sources captured_source
        ON captured_source.id=document_source.evidence_source_id
      WHERE document_source.document_id=d.id
        AND (captured_source.deleted_at IS NOT NULL OR captured_source.index_status='deleted')
        AND NOT EXISTS (
            SELECT 1 FROM evidence_sources active_source
            WHERE active_source.project_id=captured_source.project_id
              AND active_source.source_type=captured_source.source_type
              AND active_source.source_id=captured_source.source_id
              AND active_source.source_version>captured_source.source_version
              AND active_source.deleted_at IS NULL
              AND active_source.index_status<>'deleted'
        )
  )
ORDER BY c.id
LIMIT $3`, ingestionRunID, afterChunkID, limit)
	if err != nil {
		return nil, fmt.Errorf("list vector chunks: %w", err)
	}
	defer rows.Close()
	chunks := make([]VectorChunk, 0, limit)
	for rows.Next() {
		var chunk VectorChunk
		var sourceID, noteID sql.NullInt64
		var mediaPosition sql.NullInt32
		if err := rows.Scan(
			&chunk.ChunkID, &chunk.Content, &chunk.ContentHash, &chunk.DocumentID,
			&chunk.DocumentType, &chunk.SourceType, &sourceID, &chunk.SourceVersion,
			&noteID, &mediaPosition, &chunk.ProjectID, &chunk.ProjectVisibility,
			&chunk.DocumentVisibility, &chunk.DocumentLifecycle,
		); err != nil {
			return nil, fmt.Errorf("scan vector chunk: %w", err)
		}
		if sourceID.Valid {
			value := sourceID.Int64
			chunk.SourceID = &value
		}
		if noteID.Valid {
			value := noteID.Int64
			chunk.NoteID = &value
		}
		if mediaPosition.Valid {
			value := int(mediaPosition.Int32)
			chunk.MediaPosition = &value
		}
		chunks = append(chunks, chunk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector chunks: %w", err)
	}
	return chunks, nil
}

func vectorIndexSelectSQL() string {
	return `SELECT ingestion_run_id,index_version,embedding_model,embedding_revision,
       vector_dimension,distance_metric,collection_name,status,point_count,
       COALESCE(index_checksum,''),started_at,completed_at
FROM retrieval_vector_indexes `
}

func scanVectorIndex(row rowScanner, index *VectorIndex) error {
	var completedAt sql.NullTime
	if err := row.Scan(
		&index.IngestionRunID, &index.IndexVersion, &index.EmbeddingModel,
		&index.EmbeddingRevision, &index.VectorDimension, &index.DistanceMetric,
		&index.CollectionName, &index.Status, &index.PointCount, &index.IndexChecksum,
		&index.StartedAt, &completedAt,
	); err != nil {
		return err
	}
	if completedAt.Valid {
		index.CompletedAt = completedAt.Time
	}
	return nil
}
