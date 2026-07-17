package evidence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"time"

	"github.com/jmoiron/sqlx"
)

var (
	ErrDatasetVersionNotFound  = errors.New("dataset version not found")
	ErrDatasetVersionNotFrozen = errors.New("dataset version is not frozen")
	ErrRunConflict             = errors.New("ingestion run parameters conflict")
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) BeginRun(ctx context.Context, request IngestRequest) (Run, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Run{}, fmt.Errorf("begin ingestion run transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, request.RunID); err != nil {
		return Run{}, fmt.Errorf("lock ingestion run: %w", err)
	}

	existing, found, err := getRun(ctx, tx, request.RunID)
	if err != nil {
		return Run{}, err
	}
	if found {
		if existing.DatasetVersionID != request.DatasetVersionID || existing.Mode != request.Mode ||
			existing.ParserVersion != ParserVersion || existing.ChunkerVersion != ChunkerVersion ||
			existing.TokenizerVersion != TokenizerVersion {
			return Run{}, ErrRunConflict
		}
		if existing.Status == "failed" {
			if _, err := tx.ExecContext(ctx, `
UPDATE ingestion_runs
SET status='running', error_message=NULL, completed_at=NULL, updated_at=now()
WHERE run_id=$1`, request.RunID); err != nil {
				return Run{}, fmt.Errorf("resume ingestion run: %w", err)
			}
			existing.Status = "running"
		}
		existing.Resumed = true
		if err := tx.Commit(); err != nil {
			return Run{}, fmt.Errorf("commit ingestion run resume: %w", err)
		}
		return existing, nil
	}

	var run Run
	if err := tx.QueryRowxContext(ctx, `
SELECT dv.id, dv.dataset_id, dv.project_id, dv.version, dv.status,
       dv.manifest_checksum, dv.source_count
FROM dataset_versions dv
WHERE dv.id=$1`, request.DatasetVersionID).Scan(
		&run.DatasetVersionID,
		&run.DatasetID,
		&run.ProjectID,
		&run.DatasetVersion,
		&run.Status,
		&run.DatasetManifestChecksum,
		&run.SourceCount,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrDatasetVersionNotFound
		}
		return Run{}, fmt.Errorf("load dataset version: %w", err)
	}
	if run.Status != "frozen" {
		return Run{}, ErrDatasetVersionNotFrozen
	}
	run.RunID = request.RunID
	run.Mode = request.Mode
	run.ParserVersion = ParserVersion
	run.ChunkerVersion = ChunkerVersion
	run.TokenizerVersion = TokenizerVersion
	run.Status = "running"
	if err := tx.QueryRowxContext(ctx, `
INSERT INTO ingestion_runs (
    run_id, dataset_version_id, dataset_id, project_id, mode,
    parser_version, chunker_version, tokenizer_version, status,
    dataset_manifest_checksum, source_count, started_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'running',$9,$10,now(),now(),now())
RETURNING started_at`,
		run.RunID, run.DatasetVersionID, run.DatasetID, run.ProjectID, run.Mode,
		run.ParserVersion, run.ChunkerVersion, run.TokenizerVersion,
		run.DatasetManifestChecksum, run.SourceCount,
	).Scan(&run.StartedAt); err != nil {
		return Run{}, fmt.Errorf("create ingestion run: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO ingestion_run_fact_inputs (
    run_id, daily_fact_payload_id, project_id, fact_type,
    subject_id, fact_date, source_version, content_hash
)
SELECT DISTINCT ON (p.fact_type, p.subject_id, p.fact_date)
       $1, p.id, p.project_id, p.fact_type, p.subject_id,
       p.fact_date, p.source_version, p.payload_hash
FROM daily_fact_payloads p
JOIN dataset_version_sources dvs
  ON dvs.dataset_version_id=$2
 AND dvs.source_type='note'
 AND dvs.source_id=p.subject_id
WHERE p.project_id=$3 AND p.fact_type='note_daily_fact'
ORDER BY p.fact_type, p.subject_id, p.fact_date, p.source_version DESC, p.id DESC`, run.RunID, run.DatasetVersionID, run.ProjectID); err != nil {
		return Run{}, fmt.Errorf("snapshot note daily facts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO ingestion_run_fact_inputs (
    run_id, daily_fact_payload_id, project_id, fact_type,
    subject_id, fact_date, source_version, content_hash
)
SELECT DISTINCT ON (p.fact_type, p.subject_id, p.fact_date)
       $1, p.id, p.project_id, p.fact_type, p.subject_id,
       p.fact_date, p.source_version, p.payload_hash
FROM daily_fact_payloads p
WHERE p.project_id=$2 AND p.fact_type='user_daily_fact'
ORDER BY p.fact_type, p.subject_id, p.fact_date, p.source_version DESC, p.id DESC`, run.RunID, run.ProjectID); err != nil {
		return Run{}, fmt.Errorf("snapshot user daily facts: %w", err)
	}

	checksum, factCount, err := factInputChecksum(ctx, tx, run)
	if err != nil {
		return Run{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE ingestion_runs
SET input_checksum=$2, fact_source_count=$3, updated_at=now()
WHERE run_id=$1`, run.RunID, checksum, factCount); err != nil {
		return Run{}, fmt.Errorf("seal ingestion input: %w", err)
	}
	run.InputChecksum = checksum
	run.FactSourceCount = factCount
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit ingestion run: %w", err)
	}
	return run, nil
}

func (r *Repository) ListSources(ctx context.Context, run Run) ([]SourceInput, error) {
	rows, err := r.db.QueryxContext(ctx, `
SELECT dvs.evidence_source_id, dvs.project_id, dv.dataset_id, dvs.dataset_version_id,
       dv.version, dvs.source_type, dvs.source_id, dvs.source_version,
       dvs.content_hash, dvs.visibility, esp.canonical_text, esp.source_payload
FROM dataset_version_sources dvs
JOIN dataset_versions dv ON dv.id=dvs.dataset_version_id
JOIN evidence_source_payloads esp ON esp.evidence_source_id=dvs.evidence_source_id
WHERE dvs.dataset_version_id=$1
ORDER BY dvs.source_type, dvs.source_id, dvs.source_version, dvs.evidence_source_id`, run.DatasetVersionID)
	if err != nil {
		return nil, fmt.Errorf("query ingestion sources: %w", err)
	}
	defer rows.Close()
	sources := make([]SourceInput, 0, run.SourceCount)
	for rows.Next() {
		var source SourceInput
		var payload []byte
		if err := rows.Scan(
			&source.EvidenceSourceID, &source.ProjectID, &source.DatasetID,
			&source.DatasetVersionID, &source.DatasetVersion, &source.SourceType,
			&source.SourceID, &source.SourceVersion, &source.ContentHash,
			&source.Visibility, &source.CanonicalText, &payload,
		); err != nil {
			return nil, fmt.Errorf("scan ingestion source: %w", err)
		}
		source.SourcePayload = json.RawMessage(payload)
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ingestion sources: %w", err)
	}
	return sources, nil
}

func (r *Repository) ListFacts(ctx context.Context, run Run) ([]FactInput, error) {
	rows, err := r.db.QueryxContext(ctx, `
SELECT p.id, p.project_id, ir.dataset_id, ir.dataset_version_id, dv.version,
       p.fact_type, p.subject_id, p.fact_date, p.source_version,
       p.payload_hash, p.source_payload, p.captured_at
FROM ingestion_run_fact_inputs input
JOIN ingestion_runs ir ON ir.run_id=input.run_id
JOIN dataset_versions dv ON dv.id=ir.dataset_version_id
JOIN daily_fact_payloads p ON p.id=input.daily_fact_payload_id
WHERE input.run_id=$1
ORDER BY p.fact_type, p.subject_id, p.fact_date, p.source_version, p.id`, run.RunID)
	if err != nil {
		return nil, fmt.Errorf("query ingestion facts: %w", err)
	}
	defer rows.Close()
	facts := make([]FactInput, 0, run.FactSourceCount)
	for rows.Next() {
		var fact FactInput
		var payload []byte
		if err := rows.Scan(
			&fact.DailyFactPayloadID, &fact.ProjectID, &fact.DatasetID,
			&fact.DatasetVersionID, &fact.DatasetVersion, &fact.FactType,
			&fact.SubjectID, &fact.FactDate, &fact.SourceVersion,
			&fact.ContentHash, &payload, &fact.CapturedAt,
		); err != nil {
			return nil, fmt.Errorf("scan ingestion fact: %w", err)
		}
		fact.SourcePayload = json.RawMessage(payload)
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ingestion facts: %w", err)
	}
	return facts, nil
}

func (r *Repository) LinkReusableDocuments(ctx context.Context, runID string, documents []DocumentInput) (map[string]struct{}, error) {
	reused := make(map[string]struct{})
	for start := 0; start < len(documents); start += ReuseBatchSize {
		end := start + ReuseBatchSize
		if end > len(documents) {
			end = len(documents)
		}
		batch := documents[start:end]
		keys := make([]string, 0, len(batch))
		expected := make(map[string]DocumentInput, len(batch))
		for _, document := range batch {
			keys = append(keys, document.DocumentKey)
			expected[document.DocumentKey] = document
		}
		query, arguments, err := sqlx.In(`
SELECT document_key, lifecycle_status, content_hash, expected_chunk_count
FROM evidence_documents
WHERE document_key IN (?)`, keys)
		if err != nil {
			return nil, fmt.Errorf("build reusable document query: %w", err)
		}
		tx, err := r.db.BeginTxx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("begin reusable document transaction: %w", err)
		}
		rows, err := tx.QueryxContext(ctx, tx.Rebind(query), arguments...)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("query reusable documents: %w", err)
		}
		validKeys := make([]string, 0, len(batch))
		for rows.Next() {
			var documentKey, status, contentHash string
			var expectedChunkCount int
			if err := rows.Scan(&documentKey, &status, &contentHash, &expectedChunkCount); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return nil, fmt.Errorf("scan reusable document: %w", err)
			}
			document, found := expected[documentKey]
			if found && status == "ready" && contentHash == document.ContentHash && expectedChunkCount == len(document.Chunks) {
				validKeys = append(validKeys, documentKey)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return nil, fmt.Errorf("iterate reusable documents: %w", err)
		}
		_ = rows.Close()
		if len(validKeys) > 0 {
			linkQuery, linkArguments, err := sqlx.In(`
INSERT INTO ingestion_run_documents (run_id, document_id, disposition)
SELECT ?, id, 'reused'
FROM evidence_documents
WHERE document_key IN (?)
ON CONFLICT (run_id, document_id) DO NOTHING`, runID, validKeys)
			if err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("build reusable document link: %w", err)
			}
			if _, err := tx.ExecContext(ctx, tx.Rebind(linkQuery), linkArguments...); err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("link reusable documents: %w", err)
			}
			for _, documentKey := range validKeys {
				reused[documentKey] = struct{}{}
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit reusable document links: %w", err)
		}
	}
	return reused, nil
}

func (r *Repository) SaveDocument(ctx context.Context, runID string, document DocumentInput) (SaveResult, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return SaveResult{}, fmt.Errorf("begin document transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	metadata := normalizedJSON(document.Metadata)
	var documentID int64
	var existingStatus, existingContentHash string
	var existingExpectedChunks int
	var existingChunkCount, existingCitationCount int64
	err = tx.QueryRowxContext(ctx, `
SELECT d.id, d.lifecycle_status, d.content_hash, d.expected_chunk_count,
       (SELECT COUNT(*) FROM evidence_chunks c WHERE c.document_id=d.id),
       (SELECT COUNT(*) FROM source_citations sc WHERE sc.document_id=d.id)
FROM evidence_documents d
WHERE d.document_key=$1`, document.DocumentKey).Scan(
		&documentID, &existingStatus, &existingContentHash, &existingExpectedChunks,
		&existingChunkCount, &existingCitationCount,
	)
	if err == nil {
		expectedCitations := 0
		for _, chunk := range document.Chunks {
			expectedCitations += len(chunk.Citations)
		}
		if existingStatus != "ready" || existingContentHash != document.ContentHash ||
			existingExpectedChunks != len(document.Chunks) || existingChunkCount != int64(len(document.Chunks)) ||
			existingCitationCount != int64(expectedCitations) {
			return SaveResult{}, fmt.Errorf("existing document %s failed immutable child validation", document.DocumentKey)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO ingestion_run_documents (run_id, document_id, disposition)
VALUES ($1,$2,'reused')
ON CONFLICT (run_id, document_id) DO NOTHING`, runID, documentID); err != nil {
			return SaveResult{}, fmt.Errorf("link reused ingestion document: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return SaveResult{}, fmt.Errorf("commit reused evidence document: %w", err)
		}
		return SaveResult{
			DocumentID: documentID, Reused: true,
			ChunkCount: existingChunkCount, CitationCount: existingCitationCount,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SaveResult{}, fmt.Errorf("query existing evidence document: %w", err)
	}

	created := true
	err = tx.QueryRowxContext(ctx, `
INSERT INTO evidence_documents (
    document_key, project_id, dataset_id, dataset_version_id,
    document_type, source_type, source_id, source_key, source_version,
    source_content_hash, parser_version, visibility, canonical_text,
    content_hash, metadata, expected_chunk_count, lifecycle_status,
    source_created_at, source_updated_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'building',$17,$18,now(),now())
ON CONFLICT (document_key) DO NOTHING
RETURNING id`,
		document.DocumentKey, document.ProjectID, document.DatasetID, document.DatasetVersionID,
		document.DocumentType, document.SourceType, document.SourceID, document.SourceKey,
		document.SourceVersion, document.SourceContentHash, document.ParserVersion,
		document.Visibility, document.CanonicalText, document.ContentHash, metadata,
		len(document.Chunks), document.SourceCreatedAt, document.SourceUpdatedAt,
	).Scan(&documentID)
	if errors.Is(err, sql.ErrNoRows) {
		created = false
		if err := tx.GetContext(ctx, &documentID, `SELECT id FROM evidence_documents WHERE document_key=$1`, document.DocumentKey); err != nil {
			return SaveResult{}, fmt.Errorf("load existing evidence document: %w", err)
		}
	} else if err != nil {
		return SaveResult{}, fmt.Errorf("insert evidence document: %w", err)
	}

	for index := range document.Sources {
		source := document.Sources[index]
		source.SourceOrder = index
		if _, err := tx.ExecContext(ctx, `
INSERT INTO evidence_document_sources (
    document_id, evidence_source_id, daily_fact_payload_id, source_type,
    source_id, source_version, content_hash, source_order
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (document_id, source_order) DO NOTHING`,
			documentID, source.EvidenceSourceID, source.DailyFactPayloadID,
			source.SourceType, source.SourceID, source.SourceVersion,
			source.ContentHash, source.SourceOrder,
		); err != nil {
			return SaveResult{}, fmt.Errorf("insert document source: %w", err)
		}
	}

	for _, chunk := range document.Chunks {
		var chunkID int64
		err := tx.QueryRowxContext(ctx, `
INSERT INTO evidence_chunks (
    chunk_key, document_id, chunk_index, start_byte, end_byte,
    start_rune, end_rune, content, content_hash, chunker_version,
    tokenizer_version, lexemes, metadata, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,now())
ON CONFLICT (chunk_key) DO NOTHING
RETURNING id`,
			chunk.ChunkKey, documentID, chunk.ChunkIndex, chunk.StartByte, chunk.EndByte,
			chunk.StartRune, chunk.EndRune, chunk.Content, chunk.ContentHash,
			chunk.ChunkerVersion, chunk.TokenizerVersion, chunk.Lexemes, normalizedJSON(chunk.Metadata),
		).Scan(&chunkID)
		if errors.Is(err, sql.ErrNoRows) {
			if err := tx.GetContext(ctx, &chunkID, `
SELECT id FROM evidence_chunks WHERE chunk_key=$1 AND document_id=$2`, chunk.ChunkKey, documentID); err != nil {
				return SaveResult{}, fmt.Errorf("load existing evidence chunk: %w", err)
			}
		} else if err != nil {
			return SaveResult{}, fmt.Errorf("insert evidence chunk: %w", err)
		}
		for _, citation := range chunk.Citations {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO source_citations (
    citation_key, document_id, chunk_id, project_id, dataset_id,
    dataset_version_id, evidence_source_id, daily_fact_payload_id,
    source_type, source_id, source_version, source_content_hash,
    parser_version, document_start_byte, document_end_byte,
    source_start_byte, source_end_byte, quote_hash, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,now())
ON CONFLICT (citation_key) DO NOTHING`,
				citation.CitationKey, documentID, chunkID, document.ProjectID,
				document.DatasetID, document.DatasetVersionID, citation.EvidenceSourceID,
				citation.DailyFactPayloadID, citation.SourceType, citation.SourceID,
				citation.SourceVersion, citation.SourceContentHash, document.ParserVersion,
				citation.DocumentStartByte, citation.DocumentEndByte,
				citation.SourceStartByte, citation.SourceEndByte, citation.QuoteHash,
			); err != nil {
				return SaveResult{}, fmt.Errorf("insert source citation: %w", err)
			}
		}
	}

	var chunkCount, citationCount int64
	if err := tx.QueryRowxContext(ctx, `
SELECT COUNT(DISTINCT c.id), COUNT(sc.id)
FROM evidence_chunks c
LEFT JOIN source_citations sc ON sc.chunk_id=c.id
WHERE c.document_id=$1`, documentID).Scan(&chunkCount, &citationCount); err != nil {
		return SaveResult{}, fmt.Errorf("validate document children: %w", err)
	}
	if chunkCount != int64(len(document.Chunks)) {
		return SaveResult{}, fmt.Errorf("document %s has %d chunks, want %d", document.DocumentKey, chunkCount, len(document.Chunks))
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE evidence_documents
SET lifecycle_status='ready', deleted_at=NULL, updated_at=now()
WHERE id=$1 AND lifecycle_status IN ('building','failed')`, documentID); err != nil {
		return SaveResult{}, fmt.Errorf("publish evidence document: %w", err)
	}
	disposition := "reused"
	if created {
		disposition = "created"
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO ingestion_run_documents (run_id, document_id, disposition)
VALUES ($1,$2,$3)
ON CONFLICT (run_id, document_id) DO NOTHING`, runID, documentID, disposition); err != nil {
		return SaveResult{}, fmt.Errorf("link ingestion document: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE evidence_documents
SET lifecycle_status='superseded', deleted_at=COALESCE(deleted_at, now()), updated_at=now()
WHERE project_id=$1 AND source_type=$2 AND source_key=$3
  AND id<>$4 AND lifecycle_status='ready'`,
		document.ProjectID, document.SourceType, document.SourceKey, documentID); err != nil {
		return SaveResult{}, fmt.Errorf("supersede old evidence document: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SaveResult{}, fmt.Errorf("commit evidence document: %w", err)
	}
	return SaveResult{DocumentID: documentID, Reused: !created, ChunkCount: chunkCount, CitationCount: citationCount}, nil
}

func (r *Repository) CompleteRun(ctx context.Context, runID string) (Run, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return Run{}, fmt.Errorf("begin complete ingestion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var invalid int64
	if err := tx.GetContext(ctx, &invalid, `
SELECT COUNT(*)
FROM ingestion_run_documents rd
JOIN evidence_documents d ON d.id=rd.document_id
WHERE rd.run_id=$1 AND d.lifecycle_status<>'ready'`, runID); err != nil {
		return Run{}, fmt.Errorf("validate run documents: %w", err)
	}
	if invalid != 0 {
		return Run{}, fmt.Errorf("ingestion run %s has %d non-ready documents", runID, invalid)
	}
	outputChecksum, err := outputChecksum(ctx, tx, runID)
	if err != nil {
		return Run{}, err
	}
	var run Run
	if err := tx.QueryRowxContext(ctx, `
WITH counts AS (
    SELECT COUNT(DISTINCT rd.document_id) AS documents,
           COUNT(DISTINCT c.id) AS chunks,
           COUNT(DISTINCT sc.id) AS citations,
           COUNT(DISTINCT rd.document_id) FILTER (WHERE rd.disposition='reused') AS reused
    FROM ingestion_run_documents rd
    LEFT JOIN evidence_chunks c ON c.document_id=rd.document_id
    LEFT JOIN source_citations sc ON sc.chunk_id=c.id
    WHERE rd.run_id=$1
)
UPDATE ingestion_runs ir
SET status='completed', output_checksum=$2,
    document_count=counts.documents, chunk_count=counts.chunks,
    citation_count=counts.citations, reused_document_count=counts.reused,
    completed_at=now(), updated_at=now()
FROM counts
WHERE ir.run_id=$1 AND ir.status='running'
RETURNING ir.run_id, ir.dataset_version_id, ir.dataset_id, ir.project_id,
          (SELECT version FROM dataset_versions WHERE id=ir.dataset_version_id),
          ir.mode, ir.parser_version, ir.chunker_version, ir.tokenizer_version,
          ir.status, ir.dataset_manifest_checksum, ir.input_checksum,
          ir.output_checksum, ir.source_count, ir.fact_source_count,
          ir.document_count, ir.chunk_count, ir.citation_count,
          ir.reused_document_count, ir.started_at, ir.completed_at`, runID, outputChecksum).Scan(
		&run.RunID, &run.DatasetVersionID, &run.DatasetID, &run.ProjectID, &run.DatasetVersion,
		&run.Mode, &run.ParserVersion, &run.ChunkerVersion, &run.TokenizerVersion,
		&run.Status, &run.DatasetManifestChecksum, &run.InputChecksum,
		&run.OutputChecksum, &run.SourceCount, &run.FactSourceCount,
		&run.DocumentCount, &run.ChunkCount, &run.CitationCount,
		&run.ReusedDocumentCount, &run.StartedAt, &run.CompletedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			existing, found, getErr := getRun(ctx, tx, runID)
			if getErr == nil && found && existing.Status == "completed" {
				return existing, nil
			}
		}
		return Run{}, fmt.Errorf("complete ingestion run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE evidence_sources es
SET index_status='indexed', updated_at=now()
FROM evidence_document_sources ds
JOIN ingestion_run_documents rd ON rd.document_id=ds.document_id
WHERE rd.run_id=$1 AND ds.evidence_source_id=es.id
  AND es.deleted_at IS NULL AND es.index_status<>'deleted'`, runID); err != nil {
		return Run{}, fmt.Errorf("mark evidence sources indexed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit ingestion completion: %w", err)
	}
	return run, nil
}

func (r *Repository) MarkFailed(ctx context.Context, runID string, cause error) error {
	message := "unknown ingestion failure"
	if cause != nil {
		message = cause.Error()
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE ingestion_runs
SET status='failed', error_message=left($2,4000), updated_at=now()
WHERE run_id=$1 AND status='running'`, runID, message)
	if err != nil {
		return fmt.Errorf("mark ingestion failed: %w", err)
	}
	return nil
}

func (r *Repository) ReconcileLifecycle(ctx context.Context) (ReconcileResult, error) {
	var result ReconcileResult
	stale, err := r.db.ExecContext(ctx, `
UPDATE evidence_documents d
SET lifecycle_status='stale', deleted_at=COALESCE(d.deleted_at, now()), updated_at=now()
WHERE d.lifecycle_status='ready'
  AND d.document_type='note_comment_cluster'
  AND EXISTS (
      SELECT 1 FROM evidence_document_sources ds
      JOIN evidence_sources es ON es.id=ds.evidence_source_id
      WHERE ds.document_id=d.id AND (es.deleted_at IS NOT NULL OR es.index_status='deleted')
  )`)
	if err != nil {
		return result, fmt.Errorf("reconcile stale comment clusters: %w", err)
	}
	result.StaleDocuments, _ = stale.RowsAffected()
	superseded, err := r.db.ExecContext(ctx, `
UPDATE evidence_documents d
SET lifecycle_status='superseded', deleted_at=COALESCE(d.deleted_at, now()), updated_at=now()
WHERE d.lifecycle_status='ready'
  AND d.document_type IN ('note','note_media')
  AND EXISTS (
      SELECT 1
      FROM evidence_document_sources ds
      JOIN evidence_sources current_source ON current_source.id=ds.evidence_source_id
      JOIN evidence_sources newer
        ON newer.project_id=current_source.project_id
       AND newer.source_type=current_source.source_type
       AND newer.source_id=current_source.source_id
       AND newer.source_version>current_source.source_version
       AND newer.deleted_at IS NULL
       AND newer.index_status<>'deleted'
      WHERE ds.document_id=d.id
        AND (current_source.deleted_at IS NOT NULL OR current_source.index_status='deleted')
  )`)
	if err != nil {
		return result, fmt.Errorf("reconcile superseded documents: %w", err)
	}
	result.SupersededDocuments, _ = superseded.RowsAffected()
	deleted, err := r.db.ExecContext(ctx, `
UPDATE evidence_documents d
SET lifecycle_status='deleted', deleted_at=COALESCE(d.deleted_at, now()), updated_at=now()
WHERE d.lifecycle_status='ready'
  AND d.document_type IN ('note','note_media')
  AND EXISTS (
      SELECT 1
      FROM evidence_document_sources ds
      JOIN evidence_sources es ON es.id=ds.evidence_source_id
      WHERE ds.document_id=d.id
        AND (es.deleted_at IS NOT NULL OR es.index_status='deleted')
  )`)
	if err != nil {
		return result, fmt.Errorf("reconcile deleted documents: %w", err)
	}
	result.DeletedDocuments, _ = deleted.RowsAffected()
	factSuperseded, err := r.db.ExecContext(ctx, `
UPDATE evidence_documents d
SET lifecycle_status='superseded', deleted_at=COALESCE(d.deleted_at, now()), updated_at=now()
WHERE d.lifecycle_status='ready'
  AND d.document_type IN ('note_daily_fact','user_daily_fact')
  AND EXISTS (
      SELECT 1
      FROM evidence_document_sources ds
      JOIN daily_fact_payloads old_fact ON old_fact.id=ds.daily_fact_payload_id
      JOIN daily_fact_payloads newer
        ON newer.project_id=old_fact.project_id
       AND newer.fact_type=old_fact.fact_type
       AND newer.subject_id=old_fact.subject_id
       AND newer.fact_date=old_fact.fact_date
       AND newer.source_version>old_fact.source_version
      WHERE ds.document_id=d.id
  )`)
	if err != nil {
		return result, fmt.Errorf("reconcile superseded fact documents: %w", err)
	}
	factCount, _ := factSuperseded.RowsAffected()
	result.SupersededDocuments += factCount
	return result, nil
}

func (r *Repository) Audit(ctx context.Context, runID string) (AuditReport, error) {
	report := AuditReport{RunID: runID, CheckedAt: time.Now().UTC(), Violations: make(map[string]int64)}
	queries := map[string]string{
		"incomplete_documents": `
SELECT COUNT(*) FROM ingestion_run_documents rd
JOIN evidence_documents d ON d.id=rd.document_id
WHERE rd.run_id=$1 AND d.lifecycle_status IN ('building','failed')`,
		"chunk_count_mismatch": `
SELECT COUNT(*) FROM (
  SELECT d.id
  FROM ingestion_run_documents rd
  JOIN evidence_documents d ON d.id=rd.document_id
  LEFT JOIN evidence_chunks c ON c.document_id=d.id
  WHERE rd.run_id=$1
  GROUP BY d.id, d.expected_chunk_count
  HAVING COUNT(c.id)<>d.expected_chunk_count
) invalid`,
		"invalid_chunk_slice": `
SELECT COUNT(*)
FROM ingestion_run_documents rd
JOIN evidence_documents d ON d.id=rd.document_id
JOIN evidence_chunks c ON c.document_id=d.id
WHERE rd.run_id=$1 AND (
  c.start_byte<0 OR c.end_byte>octet_length(d.canonical_text)
  OR substring(convert_to(d.canonical_text,'UTF8') FROM c.start_byte+1 FOR c.end_byte-c.start_byte)
     <> convert_to(c.content,'UTF8')
  OR encode(digest(convert_to(c.content,'UTF8'),'sha256'),'hex')<>c.content_hash
)`,
		"chunks_without_citation": `
SELECT COUNT(*) FROM (
  SELECT c.id
  FROM ingestion_run_documents rd
  JOIN evidence_chunks c ON c.document_id=rd.document_id
  LEFT JOIN source_citations sc ON sc.chunk_id=c.id
  WHERE rd.run_id=$1
  GROUP BY c.id
  HAVING COUNT(sc.id)=0
) invalid`,
		"invalid_citation_slice": `
SELECT COUNT(*)
FROM ingestion_run_documents rd
JOIN evidence_documents d ON d.id=rd.document_id
JOIN evidence_chunks c ON c.document_id=d.id
JOIN source_citations sc ON sc.chunk_id=c.id
WHERE rd.run_id=$1 AND (
  sc.document_start_byte<c.start_byte OR sc.document_end_byte>c.end_byte
  OR encode(digest(substring(convert_to(d.canonical_text,'UTF8')
       FROM sc.document_start_byte+1 FOR sc.document_end_byte-sc.document_start_byte),'sha256'),'hex')<>sc.quote_hash
)`,
		"scope_mismatch": `
SELECT COUNT(*)
FROM ingestion_run_documents rd
JOIN ingestion_runs ir ON ir.run_id=rd.run_id
JOIN evidence_documents d ON d.id=rd.document_id
WHERE rd.run_id=$1 AND (
  d.project_id<>ir.project_id OR d.dataset_id<>ir.dataset_id
  OR d.dataset_version_id<>ir.dataset_version_id
)`,
		"active_deleted_source": `
SELECT COUNT(DISTINCT d.id)
FROM ingestion_run_documents rd
JOIN evidence_documents d ON d.id=rd.document_id
JOIN evidence_document_sources ds ON ds.document_id=d.id
JOIN evidence_sources es ON es.id=ds.evidence_source_id
WHERE rd.run_id=$1 AND d.lifecycle_status='ready'
  AND (es.deleted_at IS NOT NULL OR es.index_status='deleted')`,
	}
	for name, query := range queries {
		var count int64
		err := r.db.QueryRowxContext(ctx, query, runID).Scan(&count)
		if err != nil {
			return AuditReport{}, fmt.Errorf("audit %s: %w", name, err)
		}
		report.Violations[name] = count
	}
	var status, expectedChecksum string
	if err := r.db.QueryRowxContext(ctx, `SELECT status, COALESCE(output_checksum,'') FROM ingestion_runs WHERE run_id=$1`, runID).Scan(&status, &expectedChecksum); err != nil {
		return AuditReport{}, fmt.Errorf("load audited ingestion run: %w", err)
	}
	if status == "completed" {
		actualChecksum, err := outputChecksum(ctx, r.db, runID)
		if err != nil {
			return AuditReport{}, err
		}
		if actualChecksum != expectedChecksum {
			report.Violations["output_checksum_mismatch"] = 1
		} else {
			report.Violations["output_checksum_mismatch"] = 0
		}
	}
	report.Healthy = true
	for _, count := range report.Violations {
		if count != 0 {
			report.Healthy = false
			break
		}
	}
	return report, nil
}

type queryer interface {
	QueryxContext(context.Context, string, ...any) (*sqlx.Rows, error)
}

func outputChecksum(ctx context.Context, db queryer, runID string) (string, error) {
	rows, err := db.QueryxContext(ctx, `
SELECT item_type, item_key
FROM (
    SELECT 'document' AS item_type, d.document_key AS item_key
    FROM ingestion_run_documents rd
    JOIN evidence_documents d ON d.id=rd.document_id
    WHERE rd.run_id=$1
    UNION ALL
    SELECT 'chunk', c.chunk_key
    FROM ingestion_run_documents rd
    JOIN evidence_chunks c ON c.document_id=rd.document_id
    WHERE rd.run_id=$1
    UNION ALL
    SELECT 'citation', sc.citation_key
    FROM ingestion_run_documents rd
    JOIN evidence_chunks c ON c.document_id=rd.document_id
    JOIN source_citations sc ON sc.chunk_id=c.id
    WHERE rd.run_id=$1
) manifest
ORDER BY item_type, item_key`, runID)
	if err != nil {
		return "", fmt.Errorf("query ingestion output manifest: %w", err)
	}
	defer rows.Close()
	hasher := sha256.New()
	_, _ = fmt.Fprint(hasher, "evidence_ingestion_output_v1\n")
	for rows.Next() {
		var itemType, itemKey string
		if err := rows.Scan(&itemType, &itemKey); err != nil {
			return "", fmt.Errorf("scan ingestion output manifest: %w", err)
		}
		_, _ = fmt.Fprintf(hasher, "%s\x1f%s\n", itemType, itemKey)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate ingestion output manifest: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func factInputChecksum(ctx context.Context, tx *sqlx.Tx, run Run) (string, int64, error) {
	rows, err := tx.QueryxContext(ctx, `
SELECT fact_type, subject_id, fact_date, source_version, content_hash
FROM ingestion_run_fact_inputs
WHERE run_id=$1
ORDER BY fact_type, subject_id, fact_date, source_version, daily_fact_payload_id`, run.RunID)
	if err != nil {
		return "", 0, fmt.Errorf("query fact input manifest: %w", err)
	}
	defer rows.Close()
	hasher := sha256.New()
	writeInputHeader(hasher, run)
	var count int64
	for rows.Next() {
		var factType, contentHash string
		var subjectID, sourceVersion int64
		var factDate time.Time
		if err := rows.Scan(&factType, &subjectID, &factDate, &sourceVersion, &contentHash); err != nil {
			return "", 0, fmt.Errorf("scan fact input manifest: %w", err)
		}
		_, _ = fmt.Fprintf(hasher, "%s\x1f%d\x1f%s\x1f%d\x1f%s\n", factType, subjectID, factDate.Format("2006-01-02"), sourceVersion, contentHash)
		count++
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("iterate fact input manifest: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), count, nil
}

func writeInputHeader(hasher hash.Hash, run Run) {
	_, _ = fmt.Fprintf(hasher, "evidence_ingestion_input_v1\n%d\n%s\n%s\n%s\n%s\n",
		run.DatasetVersionID, run.DatasetManifestChecksum, ParserVersion, ChunkerVersion, TokenizerVersion)
}

type rowQueryer interface {
	QueryRowxContext(context.Context, string, ...any) *sqlx.Row
}

func getRun(ctx context.Context, db rowQueryer, runID string) (Run, bool, error) {
	var run Run
	var completedAt sql.NullTime
	err := db.QueryRowxContext(ctx, `
SELECT ir.run_id, ir.dataset_version_id, ir.dataset_id, ir.project_id, dv.version,
       ir.mode, ir.parser_version, ir.chunker_version, ir.tokenizer_version,
       ir.status, ir.dataset_manifest_checksum, COALESCE(ir.input_checksum,''),
       COALESCE(ir.output_checksum,''), ir.source_count, ir.fact_source_count,
       ir.document_count, ir.chunk_count, ir.citation_count,
       ir.reused_document_count, ir.started_at, ir.completed_at
FROM ingestion_runs ir
JOIN dataset_versions dv ON dv.id=ir.dataset_version_id
WHERE ir.run_id=$1`, runID).Scan(
		&run.RunID, &run.DatasetVersionID, &run.DatasetID, &run.ProjectID,
		&run.DatasetVersion, &run.Mode, &run.ParserVersion, &run.ChunkerVersion,
		&run.TokenizerVersion, &run.Status, &run.DatasetManifestChecksum,
		&run.InputChecksum, &run.OutputChecksum, &run.SourceCount,
		&run.FactSourceCount, &run.DocumentCount, &run.ChunkCount,
		&run.CitationCount, &run.ReusedDocumentCount, &run.StartedAt, &completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("query ingestion run: %w", err)
	}
	if completedAt.Valid {
		run.CompletedAt = completedAt.Time
	}
	return run, true, nil
}

func normalizedJSON(value json.RawMessage) []byte {
	if len(value) == 0 || !json.Valid(value) {
		return []byte(`{}`)
	}
	return value
}
