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

func (r *Repository) AcquireVectorIndex(ctx context.Context, index VectorIndex, leaseOwner string, leaseToken string, leaseDuration time.Duration) (VectorIndex, error) {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO retrieval_vector_indexes (
    ingestion_run_id,index_version,embedding_model,embedding_revision,
    vector_dimension,distance_metric,collection_name,status,point_count,
    checkpoint_chunk_id,checkpoint_point_count,build_attempt,
    started_at,created_at,updated_at
)
SELECT ir.run_id,$2,$3,$4,$5,'Cosine',$6,'failed',0,
       0,0,0,clock_timestamp(),clock_timestamp(),clock_timestamp()
FROM ingestion_runs ir
WHERE ir.run_id=$1 AND ir.status='completed'
ON CONFLICT (ingestion_run_id,index_version) DO NOTHING`,
		index.IngestionRunID, index.IndexVersion, index.EmbeddingModel,
		index.EmbeddingRevision, index.VectorDimension, index.CollectionName,
	)
	if err != nil {
		return VectorIndex{}, fmt.Errorf("create vector index control row: %w", err)
	}
	leaseSeconds := int64(leaseDuration / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	var acquired VectorIndex
	err = scanVectorIndex(r.db.QueryRowxContext(ctx, `
UPDATE retrieval_vector_indexes
SET status='building',build_attempt=build_attempt+1,
    point_count=checkpoint_point_count,index_checksum=NULL,error_message=NULL,
    lease_owner=$7,lease_token=$8,
    lease_expires_at=clock_timestamp()+($9*interval '1 second'),
    heartbeat_at=clock_timestamp(),completed_at=NULL,updated_at=clock_timestamp(),
    started_at=CASE WHEN build_attempt=0 THEN clock_timestamp() ELSE started_at END
WHERE ingestion_run_id=$1 AND index_version=$2
  AND embedding_model=$3 AND embedding_revision=$4
  AND vector_dimension=$5 AND collection_name=$6
  AND status<>'completed'
  AND (lease_token IS NULL OR lease_expires_at<=clock_timestamp())
RETURNING `+vectorIndexColumnsSQL(),
		index.IngestionRunID, index.IndexVersion, index.EmbeddingModel,
		index.EmbeddingRevision, index.VectorDimension, index.CollectionName,
		leaseOwner, leaseToken, leaseSeconds), &acquired)
	if err == nil {
		return acquired, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return VectorIndex{}, fmt.Errorf("acquire vector index lease: %w", err)
	}
	current, loadErr := r.LoadVectorIndex(ctx, index.IngestionRunID)
	if loadErr != nil {
		return VectorIndex{}, loadErr
	}
	if current.Status == "completed" {
		return current, nil
	}
	if current.EmbeddingModel != index.EmbeddingModel || current.EmbeddingRevision != index.EmbeddingRevision ||
		current.VectorDimension != index.VectorDimension || current.CollectionName != index.CollectionName {
		return VectorIndex{}, ErrIndexVersionMismatch
	}
	return VectorIndex{}, ErrIndexBuildLeased
}

func (r *Repository) SaveVectorIndexCheckpoint(
	ctx context.Context,
	ingestionRunID string,
	leaseToken string,
	chunkID int64,
	pointCount int64,
	orphanPointCount int64,
	missingPointCount int64,
	leaseDuration time.Duration,
	reconciled bool,
) error {
	leaseSeconds := int64(leaseDuration / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE retrieval_vector_indexes
SET checkpoint_chunk_id=$4,checkpoint_point_count=$5,point_count=$5,
    orphan_point_count=CASE WHEN $9 THEN $6 ELSE orphan_point_count END,
    missing_point_count=CASE WHEN $9 THEN $7 ELSE missing_point_count END,
    last_reconciled_at=CASE WHEN $9 THEN clock_timestamp() ELSE last_reconciled_at END,
    heartbeat_at=clock_timestamp(),
    lease_expires_at=clock_timestamp()+($8*interval '1 second'),
    updated_at=clock_timestamp()
WHERE ingestion_run_id=$1 AND index_version=$2 AND status='building' AND lease_token=$3
  AND lease_expires_at>clock_timestamp()`,
		ingestionRunID, VectorIndexVersion, leaseToken, chunkID, pointCount,
		orphanPointCount, missingPointCount, leaseSeconds, reconciled)
	if err != nil {
		return fmt.Errorf("save vector index checkpoint: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read vector index checkpoint result: %w", err)
	}
	if rows != 1 {
		return ErrIndexLeaseLost
	}
	return nil
}

func (r *Repository) CompleteVectorIndex(ctx context.Context, ingestionRunID string, leaseToken string, pointCount int64, checksum string) (VectorIndex, error) {
	var index VectorIndex
	err := scanVectorIndex(r.db.QueryRowxContext(ctx, `
UPDATE retrieval_vector_indexes
SET status='completed',point_count=$4,checkpoint_point_count=$4,index_checksum=$5,
    lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,
    completed_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE ingestion_run_id=$1 AND index_version=$2 AND status='building' AND lease_token=$3
  AND lease_expires_at>clock_timestamp()
  AND checkpoint_point_count=$4
RETURNING `+vectorIndexColumnsSQL(),
		ingestionRunID, VectorIndexVersion, leaseToken, pointCount, checksum), &index)
	if errors.Is(err, sql.ErrNoRows) {
		return VectorIndex{}, ErrIndexLeaseLost
	}
	if err != nil {
		return VectorIndex{}, fmt.Errorf("complete vector index: %w", err)
	}
	return index, nil
}

func (r *Repository) MarkVectorIndexFailed(ingestionRunID string, leaseToken string, cause error) {
	message := "unknown vector index failure"
	if cause != nil {
		message = cause.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.db.ExecContext(ctx, `
UPDATE retrieval_vector_indexes
SET status='failed',error_message=left($4,4000),completed_at=NULL,
    lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,
    updated_at=clock_timestamp()
WHERE ingestion_run_id=$1 AND index_version=$2 AND status='building' AND lease_token=$3`,
		ingestionRunID, VectorIndexVersion, leaseToken, message)
}

func (r *Repository) ListVectorManifest(ctx context.Context, ingestionRunID string) ([]VectorManifestEntry, error) {
	rows, err := r.db.QueryxContext(ctx, `
SELECT c.id,c.content_hash
`+vectorChunkFromSQL()+`
WHERE rd.run_id=$1
`+vectorChunkEligibilitySQL()+`
ORDER BY c.id`, ingestionRunID)
	if err != nil {
		return nil, fmt.Errorf("list vector manifest: %w", err)
	}
	defer rows.Close()
	entries := make([]VectorManifestEntry, 0)
	for rows.Next() {
		var entry VectorManifestEntry
		if err := rows.Scan(&entry.ChunkID, &entry.ContentHash); err != nil {
			return nil, fmt.Errorf("scan vector manifest: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector manifest: %w", err)
	}
	return entries, nil
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
`+vectorChunkFromSQL()+`
WHERE rd.run_id=$1 AND c.id>$2
`+vectorChunkEligibilitySQL()+`
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

func vectorChunkFromSQL() string {
	return `FROM ingestion_run_documents rd
JOIN evidence_documents d ON d.id=rd.document_id
JOIN projects p ON p.id=d.project_id AND p.status='active'
JOIN evidence_chunks c ON c.document_id=d.id
LEFT JOIN evidence_document_sources primary_source
  ON primary_source.document_id=d.id AND primary_source.source_order=0
LEFT JOIN evidence_source_payloads esp
  ON esp.evidence_source_id=primary_source.evidence_source_id
`
}

func vectorChunkEligibilitySQL() string {
	return `  AND d.lifecycle_status IN ('ready','superseded')
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
`
}

func vectorIndexSelectSQL() string {
	return `SELECT ` + vectorIndexColumnsSQL() + `
FROM retrieval_vector_indexes `
}

func vectorIndexColumnsSQL() string {
	return `ingestion_run_id,index_version,embedding_model,embedding_revision,
       vector_dimension,distance_metric,collection_name,status,point_count,
       checkpoint_chunk_id,checkpoint_point_count,build_attempt,
       COALESCE(lease_owner,''),lease_expires_at,heartbeat_at,last_reconciled_at,
       orphan_point_count,missing_point_count,
       COALESCE(index_checksum,''),started_at,completed_at`
}

func scanVectorIndex(row rowScanner, index *VectorIndex) error {
	var leaseExpiresAt, heartbeatAt, lastReconciledAt, completedAt sql.NullTime
	if err := row.Scan(
		&index.IngestionRunID, &index.IndexVersion, &index.EmbeddingModel,
		&index.EmbeddingRevision, &index.VectorDimension, &index.DistanceMetric,
		&index.CollectionName, &index.Status, &index.PointCount,
		&index.CheckpointChunkID, &index.CheckpointPoints, &index.BuildAttempt,
		&index.LeaseOwner, &leaseExpiresAt, &heartbeatAt, &lastReconciledAt,
		&index.OrphanPointCount, &index.MissingPointCount,
		&index.IndexChecksum, &index.StartedAt, &completedAt,
	); err != nil {
		return err
	}
	if leaseExpiresAt.Valid {
		value := leaseExpiresAt.Time
		index.LeaseExpiresAt = &value
	}
	if heartbeatAt.Valid {
		value := heartbeatAt.Time
		index.HeartbeatAt = &value
	}
	if lastReconciledAt.Valid {
		value := lastReconciledAt.Time
		index.LastReconciledAt = &value
	}
	if completedAt.Valid {
		index.CompletedAt = completedAt.Time
	}
	return nil
}
