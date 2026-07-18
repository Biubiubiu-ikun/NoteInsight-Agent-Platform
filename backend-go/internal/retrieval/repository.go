package retrieval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) ResolveScope(ctx context.Context, projectID int64, datasetVersionID int64, ingestionRunID string) (Scope, error) {
	if r.db == nil {
		return Scope{}, ErrScopeNotFound
	}
	var scope Scope
	err := r.db.QueryRowxContext(ctx, `
SELECT ir.project_id, p.visibility, ir.dataset_id, ir.dataset_version_id, dv.version,
       ir.dataset_manifest_checksum, ir.run_id, ir.output_checksum,
       ir.parser_version, ir.chunker_version, ir.tokenizer_version,
       COALESCE(li.index_version,''), COALESCE(li.index_checksum,''),
       COALESCE(vi.index_version,''), COALESCE(vi.index_checksum,''),
       COALESCE(vi.collection_name,''), COALESCE(vi.embedding_model,''),
       COALESCE(vi.embedding_revision,'')
FROM ingestion_runs ir
JOIN dataset_versions dv ON dv.id=ir.dataset_version_id
JOIN projects p ON p.id=ir.project_id
LEFT JOIN retrieval_lexical_indexes li
  ON li.ingestion_run_id=ir.run_id AND li.status='completed'
LEFT JOIN retrieval_vector_indexes vi
  ON vi.ingestion_run_id=ir.run_id AND vi.index_version=$4 AND vi.status='completed'
WHERE ir.status='completed'
  AND ($1='' OR ir.run_id=$1)
  AND ($2=0 OR ir.project_id=$2)
  AND ($3=0 OR ir.dataset_version_id=$3)
ORDER BY ir.completed_at DESC, ir.run_id DESC
LIMIT 1`, strings.TrimSpace(ingestionRunID), projectID, datasetVersionID, VectorIndexVersion).Scan(
		&scope.ProjectID, &scope.ProjectVisibility, &scope.DatasetID,
		&scope.DatasetVersionID, &scope.DatasetVersion, &scope.DatasetManifestChecksum,
		&scope.IngestionRunID, &scope.IngestionOutputChecksum, &scope.ParserVersion,
		&scope.ChunkerVersion, &scope.TokenizerVersion, &scope.LexicalIndexVersion,
		&scope.LexicalIndexChecksum, &scope.VectorIndexVersion, &scope.VectorIndexChecksum,
		&scope.VectorCollection, &scope.EmbeddingModel, &scope.EmbeddingRevision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, ErrScopeNotFound
	}
	if err != nil {
		return Scope{}, fmt.Errorf("resolve retrieval scope: %w", err)
	}
	return scope, nil
}

func (r *Repository) BuildLexicalIndex(ctx context.Context, ingestionRunID string) (index LexicalIndex, err error) {
	ingestionRunID = strings.TrimSpace(ingestionRunID)
	if ingestionRunID == "" {
		return LexicalIndex{}, fmt.Errorf("%w: ingestion_run_id is required", ErrInvalidInput)
	}
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return LexicalIndex{}, fmt.Errorf("begin lexical index build: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
		if err != nil {
			r.markIndexFailed(ingestionRunID, err)
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "retrieval-index:"+ingestionRunID); err != nil {
		return LexicalIndex{}, fmt.Errorf("lock lexical index build: %w", err)
	}

	var datasetVersionID, documentCount, chunkCount int64
	var tokenizerVersion, runStatus string
	if err := tx.QueryRowxContext(ctx, `
SELECT dataset_version_id, tokenizer_version, status, document_count, chunk_count
FROM ingestion_runs WHERE run_id=$1`, ingestionRunID).Scan(
		&datasetVersionID, &tokenizerVersion, &runStatus, &documentCount, &chunkCount,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LexicalIndex{}, ErrScopeNotFound
		}
		return LexicalIndex{}, fmt.Errorf("load ingestion run for lexical index: %w", err)
	}
	if runStatus != "completed" {
		return LexicalIndex{}, fmt.Errorf("%w: ingestion run is not completed", ErrIndexNotReady)
	}

	var existing LexicalIndex
	existingErr := scanLexicalIndex(tx.QueryRowxContext(ctx, lexicalIndexSelectSQL()+` WHERE ingestion_run_id=$1`, ingestionRunID), &existing)
	if existingErr == nil && existing.Status == "completed" {
		if existing.IndexVersion != LexicalIndexVersion || existing.TokenizerVersion != tokenizerVersion {
			return LexicalIndex{}, ErrIndexVersionMismatch
		}
		if err := ensureVisibilityStats(ctx, tx, ingestionRunID); err != nil {
			return LexicalIndex{}, err
		}
		if err := tx.Commit(); err != nil {
			return LexicalIndex{}, fmt.Errorf("commit existing lexical index lookup: %w", err)
		}
		return existing, nil
	}
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return LexicalIndex{}, fmt.Errorf("load lexical index: %w", existingErr)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO retrieval_lexical_indexes (
    ingestion_run_id, dataset_version_id, tokenizer_version, index_version,
    status, document_count, chunk_count, started_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,'building',$5,$6,clock_timestamp(),clock_timestamp(),clock_timestamp())
ON CONFLICT (ingestion_run_id) DO UPDATE
SET dataset_version_id=EXCLUDED.dataset_version_id,
    tokenizer_version=EXCLUDED.tokenizer_version,
    index_version=EXCLUDED.index_version,
    status='building', lexeme_count=0, index_checksum=NULL,
    error_message=NULL, started_at=clock_timestamp(), completed_at=NULL, updated_at=clock_timestamp()
WHERE retrieval_lexical_indexes.status<>'completed'`,
		ingestionRunID, datasetVersionID, tokenizerVersion, LexicalIndexVersion,
		documentCount, chunkCount,
	); err != nil {
		return LexicalIndex{}, fmt.Errorf("start lexical index build: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM retrieval_lexeme_stats WHERE ingestion_run_id=$1`, ingestionRunID); err != nil {
		return LexicalIndex{}, fmt.Errorf("clear incomplete lexeme stats: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO retrieval_lexeme_stats (
    ingestion_run_id, lexeme, chunk_frequency, occurrence_count,
    inverse_document_frequency
)
SELECT ir.run_id, stats.word, stats.ndoc, stats.nentry,
       ln((ir.chunk_count::double precision + 1.0) / (stats.ndoc::double precision + 1.0)) + 1.0
FROM ingestion_runs ir
CROSS JOIN LATERAL ts_stat(format(
    'SELECT c.search_vector FROM evidence_chunks c JOIN ingestion_run_documents rd ON rd.document_id=c.document_id WHERE rd.run_id=%L',
    ir.run_id
)) stats
WHERE ir.run_id=$1`, ingestionRunID); err != nil {
		return LexicalIndex{}, fmt.Errorf("build lexeme statistics: %w", err)
	}

	var lexemeCount int64
	var checksum string
	if err := tx.QueryRowxContext(ctx, `
SELECT COUNT(*), encode(digest(
    convert_to($2 || E'\x1f' || ir.output_checksum || E'\n' || COALESCE(string_agg(
        concat_ws(E'\x1f', s.lexeme, s.chunk_frequency::text,
                  s.occurrence_count::text, s.inverse_document_frequency::text),
        E'\n' ORDER BY s.lexeme
    ), ''), 'UTF8'), 'sha256'), 'hex')
FROM ingestion_runs ir
LEFT JOIN retrieval_lexeme_stats s ON s.ingestion_run_id=ir.run_id
WHERE ir.run_id=$1
GROUP BY ir.output_checksum`, ingestionRunID, LexicalIndexVersion).Scan(&lexemeCount, &checksum); err != nil {
		return LexicalIndex{}, fmt.Errorf("checksum lexical index: %w", err)
	}
	if err := tx.QueryRowxContext(ctx, `
UPDATE retrieval_lexical_indexes
SET status='completed', lexeme_count=$2, index_checksum=$3,
    completed_at=clock_timestamp(), updated_at=clock_timestamp()
WHERE ingestion_run_id=$1 AND status='building'
RETURNING ingestion_run_id, dataset_version_id, tokenizer_version, index_version,
          status, document_count, chunk_count, lexeme_count, index_checksum,
          started_at, completed_at`, ingestionRunID, lexemeCount, checksum).Scan(
		&index.IngestionRunID, &index.DatasetVersionID, &index.TokenizerVersion,
		&index.IndexVersion, &index.Status, &index.DocumentCount, &index.ChunkCount,
		&index.LexemeCount, &index.IndexChecksum, &index.StartedAt, &index.CompletedAt,
	); err != nil {
		return LexicalIndex{}, fmt.Errorf("complete lexical index: %w", err)
	}
	if err := ensureVisibilityStats(ctx, tx, ingestionRunID); err != nil {
		return LexicalIndex{}, err
	}
	if err := tx.Commit(); err != nil {
		return LexicalIndex{}, fmt.Errorf("commit lexical index: %w", err)
	}
	return index, nil
}

func (r *Repository) ResolveAccess(ctx context.Context, scope Scope, principal Principal) (string, error) {
	var access string
	err := r.db.QueryRowxContext(ctx, `
SELECT CASE
  WHEN EXISTS (
    SELECT 1 FROM project_members pm
    WHERE pm.project_id=$1 AND pm.user_id=$2 AND pm.status='active'
  ) THEN 'member'
  WHEN p.visibility='public' THEN 'public'
  ELSE 'none'
END
FROM projects p
WHERE p.id=$1 AND p.status='active'`, scope.ProjectID, principal.UserID).Scan(&access)
	if errors.Is(err, sql.ErrNoRows) {
		return "none", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve retrieval access: %w", err)
	}
	return access, nil
}

func (r *Repository) GetTermStats(ctx context.Context, ingestionRunID string, accessScope string, terms []string) (map[string]TermStat, error) {
	stats := make(map[string]TermStat, len(terms))
	if len(terms) == 0 {
		return stats, nil
	}
	rows, err := r.db.QueryxContext(ctx, `
SELECT lexeme, chunk_frequency, occurrence_count, inverse_document_frequency
FROM retrieval_lexeme_visibility_stats
WHERE ingestion_run_id=$1 AND access_scope=$2 AND lexeme=ANY($3::text[])`, ingestionRunID, accessScope, terms)
	if err != nil {
		return nil, fmt.Errorf("load query term statistics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var stat TermStat
		if err := rows.Scan(&stat.Lexeme, &stat.ChunkFrequency, &stat.OccurrenceCount, &stat.InverseDocumentFrequency); err != nil {
			return nil, fmt.Errorf("scan query term statistics: %w", err)
		}
		stats[stat.Lexeme] = stat
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate query term statistics: %w", err)
	}
	return stats, nil
}

func ensureVisibilityStats(ctx context.Context, tx *sqlx.Tx, ingestionRunID string) error {
	var existing int64
	if err := tx.QueryRowxContext(ctx, `
SELECT COUNT(*) FROM retrieval_lexeme_visibility_stats
WHERE ingestion_run_id=$1`, ingestionRunID).Scan(&existing); err != nil {
		return fmt.Errorf("check visibility-scoped lexeme stats: %w", err)
	}
	if existing > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO retrieval_lexeme_visibility_stats (
    ingestion_run_id, access_scope, lexeme, chunk_frequency,
    occurrence_count, inverse_document_frequency
)
SELECT ingestion_run_id, 'member', lexeme, chunk_frequency,
       occurrence_count, inverse_document_frequency
FROM retrieval_lexeme_stats
WHERE ingestion_run_id=$1`, ingestionRunID); err != nil {
		return fmt.Errorf("copy member lexeme stats: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
WITH public_scope AS (
    SELECT COUNT(*)::double precision AS chunk_count
    FROM ingestion_run_documents rd
    JOIN evidence_documents d ON d.id=rd.document_id
    JOIN projects p ON p.id=d.project_id
    JOIN evidence_chunks c ON c.document_id=d.id
    WHERE rd.run_id=$1 AND p.visibility='public' AND d.visibility='public'
)
INSERT INTO retrieval_lexeme_visibility_stats (
    ingestion_run_id, access_scope, lexeme, chunk_frequency,
    occurrence_count, inverse_document_frequency
)
SELECT $1, 'public', stats.word, stats.ndoc, stats.nentry,
       ln((scope.chunk_count + 1.0) / (stats.ndoc::double precision + 1.0)) + 1.0
FROM public_scope scope
CROSS JOIN LATERAL ts_stat(format(
    'SELECT c.search_vector FROM evidence_chunks c JOIN ingestion_run_documents rd ON rd.document_id=c.document_id JOIN evidence_documents d ON d.id=c.document_id JOIN projects p ON p.id=d.project_id WHERE rd.run_id=%L AND p.visibility=''public'' AND d.visibility=''public''',
    $1
)) stats`, ingestionRunID); err != nil {
		return fmt.Errorf("build public lexeme stats: %w", err)
	}
	return nil
}

func (r *Repository) SearchLexicalCandidates(ctx context.Context, scope Scope, principal Principal, plan QueryPlan, filters Filters, limit int) ([]Candidate, error) {
	documentTypes := filters.DocumentTypes
	if documentTypes == nil {
		documentTypes = []string{}
	}
	sourceTypes := filters.SourceTypes
	if sourceTypes == nil {
		sourceTypes = []string{}
	}
	rows, err := r.db.QueryxContext(ctx, `
WITH query AS MATERIALIZED (
    SELECT to_tsquery('simple', $3) AS value
), matched_chunk_ids AS MATERIALIZED (
    SELECT c.id, c.document_id
    FROM evidence_chunks c
    CROSS JOIN query
    WHERE c.search_vector @@ query.value
), candidate_chunk_ids AS MATERIALIZED (
    SELECT matched.id
    FROM matched_chunk_ids matched
    JOIN ingestion_run_documents rd
      ON rd.document_id=matched.document_id AND rd.run_id=$1
    UNION
    SELECT c.id
    FROM evidence_documents d
    JOIN ingestion_run_documents rd ON rd.document_id=d.id AND rd.run_id=$1
    JOIN evidence_chunks c ON c.document_id=d.id
    LEFT JOIN evidence_document_sources primary_source
      ON primary_source.document_id=d.id AND primary_source.source_order=0
    LEFT JOIN evidence_source_payloads esp
      ON esp.evidence_source_id=primary_source.evidence_source_id
    WHERE $11<>'' AND d.document_type=$11
      AND CASE
            WHEN d.source_type IN ('note','note_comment_cluster','note_daily_fact') THEN d.source_id
            WHEN d.source_type='note_media' THEN NULLIF(esp.source_payload->>'note_id','')::bigint
            ELSE NULL
          END = ANY($10::bigint[])
      AND (NOT $12 OR NULLIF(esp.source_payload->>'position','')::int=$13)
), eligible_chunks AS MATERIALIZED (
    SELECT d.id AS document_id, d.document_key, d.document_type, d.source_type,
           d.source_id, d.source_version,
           CASE
             WHEN d.source_type IN ('note','note_comment_cluster','note_daily_fact') THEN d.source_id
             WHEN d.source_type='note_media' THEN NULLIF(esp.source_payload->>'note_id','')::bigint
             ELSE NULL
           END AS note_id,
           CASE WHEN d.source_type='note_media'
                THEN NULLIF(esp.source_payload->>'position','')::int ELSE NULL END AS media_position,
           c.id AS chunk_id, c.search_vector
    FROM candidate_chunk_ids candidate
    JOIN evidence_chunks c ON c.id=candidate.id
    JOIN evidence_documents d ON d.id=c.document_id
    JOIN projects p ON p.id=d.project_id AND p.status='active'
    LEFT JOIN evidence_document_sources primary_source
      ON primary_source.document_id=d.id AND primary_source.source_order=0
    LEFT JOIN evidence_source_payloads esp
      ON esp.evidence_source_id=primary_source.evidence_source_id
    WHERE d.lifecycle_status IN ('ready','superseded')
      AND ((p.visibility='public' AND d.visibility='public') OR EXISTS (
          SELECT 1 FROM project_members pm
          WHERE pm.project_id=d.project_id AND pm.user_id=$2 AND pm.status='active'
      ))
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
      AND (NOT $5 OR d.document_type=ANY($6::text[]))
      AND (NOT $7 OR d.source_type=ANY($8::text[]))
), ranked_chunks AS MATERIALIZED (
    SELECT eligible.*,
           ts_rank_cd(eligible.search_vector, query.value, 32) AS fts_score
    FROM eligible_chunks eligible
    CROSS JOIN query
    ORDER BY (eligible.document_type=$11) DESC, fts_score DESC, eligible.chunk_id
    LIMIT $9
)
SELECT ranked.document_id, ranked.document_key, ranked.document_type,
       ranked.source_type, ranked.source_id, ranked.source_version,
       ranked.note_id, ranked.media_position, ranked.chunk_id,
       c.chunk_key, c.chunk_index, c.content, c.content_hash, c.lexemes,
       c.start_byte, c.end_byte, ranked.fts_score,
       similarity(c.content, $4) AS trigram_score
FROM ranked_chunks ranked
JOIN evidence_chunks c ON c.id=ranked.chunk_id
ORDER BY ranked.fts_score DESC, trigram_score DESC, ranked.chunk_id`,
		scope.IngestionRunID, principal.UserID, BuildTSQuery(plan.Terms), plan.Original,
		len(documentTypes) > 0, documentTypes, len(sourceTypes) > 0, sourceTypes, limit,
		plan.HintedNoteIDs, plan.PreferredType, plan.PreferredPosition != nil, nullableInt(plan.PreferredPosition),
	)
	if err != nil {
		return nil, fmt.Errorf("search lexical candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]Candidate, 0, limit)
	for rows.Next() {
		var candidate Candidate
		var sourceID, noteID sql.NullInt64
		var mediaPosition sql.NullInt32
		if err := rows.Scan(
			&candidate.DocumentID, &candidate.DocumentKey, &candidate.DocumentType,
			&candidate.SourceType, &sourceID, &candidate.SourceVersion, &noteID,
			&mediaPosition, &candidate.ChunkID, &candidate.ChunkKey, &candidate.ChunkIndex,
			&candidate.Content, &candidate.ContentHash, &candidate.Lexemes,
			&candidate.StartByte, &candidate.EndByte, &candidate.FTSScore,
			&candidate.TrigramScore,
		); err != nil {
			return nil, fmt.Errorf("scan lexical candidate: %w", err)
		}
		if sourceID.Valid {
			value := sourceID.Int64
			candidate.SourceID = &value
		}
		if noteID.Valid {
			value := noteID.Int64
			candidate.NoteID = &value
		}
		if mediaPosition.Valid {
			value := int(mediaPosition.Int32)
			candidate.MediaPosition = &value
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lexical candidates: %w", err)
	}
	return candidates, nil
}

func nullableInt(value *int) any {
	if value == nil {
		return 0
	}
	return *value
}

func (r *Repository) LoadAuthorizedCandidates(ctx context.Context, scope Scope, principal Principal, chunkIDs []int64, filters Filters) ([]Candidate, error) {
	if len(chunkIDs) == 0 {
		return []Candidate{}, nil
	}
	documentTypes := filters.DocumentTypes
	if documentTypes == nil {
		documentTypes = []string{}
	}
	sourceTypes := filters.SourceTypes
	if sourceTypes == nil {
		sourceTypes = []string{}
	}
	rows, err := r.db.QueryxContext(ctx, `
SELECT d.id,d.document_key,d.document_type,d.source_type,d.source_id,d.source_version,
       CASE
         WHEN d.source_type IN ('note','note_comment_cluster','note_daily_fact') THEN d.source_id
         WHEN d.source_type='note_media' THEN NULLIF(esp.source_payload->>'note_id','')::bigint
         ELSE NULL
       END AS note_id,
       CASE WHEN d.source_type='note_media'
            THEN NULLIF(esp.source_payload->>'position','')::int ELSE NULL END AS media_position,
       c.id,c.chunk_key,c.chunk_index,c.content,c.content_hash,c.lexemes,
       c.start_byte,c.end_byte
FROM evidence_chunks c
JOIN ingestion_run_documents rd ON rd.document_id=c.document_id AND rd.run_id=$1
JOIN evidence_documents d ON d.id=c.document_id AND d.project_id=$2
JOIN projects p ON p.id=d.project_id AND p.status='active'
LEFT JOIN evidence_document_sources primary_source
  ON primary_source.document_id=d.id AND primary_source.source_order=0
LEFT JOIN evidence_source_payloads esp
  ON esp.evidence_source_id=primary_source.evidence_source_id
WHERE c.id=ANY($4::bigint[])
  AND d.lifecycle_status IN ('ready','superseded')
  AND ((p.visibility='public' AND d.visibility='public') OR EXISTS (
      SELECT 1 FROM project_members pm
      WHERE pm.project_id=d.project_id AND pm.user_id=$3 AND pm.status='active'
  ))
  AND NOT EXISTS (
      SELECT 1
      FROM evidence_document_sources document_source
      JOIN evidence_sources captured_source ON captured_source.id=document_source.evidence_source_id
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
  AND (NOT $5 OR d.document_type=ANY($6::text[]))
  AND (NOT $7 OR d.source_type=ANY($8::text[]))
ORDER BY array_position($4::bigint[],c.id)`,
		scope.IngestionRunID, scope.ProjectID, principal.UserID, chunkIDs,
		len(documentTypes) > 0, documentTypes, len(sourceTypes) > 0, sourceTypes)
	if err != nil {
		return nil, fmt.Errorf("load authorized vector candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]Candidate, 0, len(chunkIDs))
	for rows.Next() {
		var candidate Candidate
		var sourceID, noteID sql.NullInt64
		var mediaPosition sql.NullInt32
		if err := rows.Scan(
			&candidate.DocumentID, &candidate.DocumentKey, &candidate.DocumentType,
			&candidate.SourceType, &sourceID, &candidate.SourceVersion, &noteID,
			&mediaPosition, &candidate.ChunkID, &candidate.ChunkKey, &candidate.ChunkIndex,
			&candidate.Content, &candidate.ContentHash, &candidate.Lexemes,
			&candidate.StartByte, &candidate.EndByte,
		); err != nil {
			return nil, fmt.Errorf("scan authorized vector candidate: %w", err)
		}
		if sourceID.Valid {
			value := sourceID.Int64
			candidate.SourceID = &value
		}
		if noteID.Valid {
			value := noteID.Int64
			candidate.NoteID = &value
		}
		if mediaPosition.Valid {
			value := int(mediaPosition.Int32)
			candidate.MediaPosition = &value
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authorized vector candidates: %w", err)
	}
	return candidates, nil
}

func (r *Repository) LoadCitations(ctx context.Context, chunkIDs []int64) (map[int64][]Citation, error) {
	result := make(map[int64][]Citation, len(chunkIDs))
	if len(chunkIDs) == 0 {
		return result, nil
	}
	query, args, err := sqlx.In(`
SELECT sc.id, sc.citation_key, sc.project_id, sc.dataset_id, sc.dataset_version_id,
       sc.document_id, sc.chunk_id, sc.source_type, sc.source_id, sc.source_version,
       CASE
         WHEN sc.source_type IN ('note','note_daily_fact') THEN sc.source_id
         WHEN sc.source_type IN ('note_media','note_comment')
           THEN NULLIF(esp.source_payload->>'note_id','')::bigint
         ELSE NULL
       END AS note_id,
       CASE WHEN sc.source_type='note_media'
            THEN NULLIF(esp.source_payload->>'position','')::int ELSE NULL END AS media_position,
       sc.source_content_hash, sc.parser_version,
       sc.document_start_byte, sc.document_end_byte,
       sc.source_start_byte, sc.source_end_byte,
       convert_from(substring(convert_to(d.canonical_text,'UTF8')
         FROM sc.document_start_byte+1
         FOR sc.document_end_byte-sc.document_start_byte), 'UTF8') AS quote,
       sc.quote_hash
FROM source_citations sc
JOIN evidence_documents d ON d.id=sc.document_id
LEFT JOIN evidence_source_payloads esp ON esp.evidence_source_id=sc.evidence_source_id
WHERE sc.chunk_id IN (?)
ORDER BY sc.chunk_id, sc.document_start_byte, sc.id`, chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("build citation query: %w", err)
	}
	rows, err := r.db.QueryxContext(ctx, r.db.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("load citations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var citation Citation
		var noteID sql.NullInt64
		var mediaPosition sql.NullInt32
		if err := rows.Scan(
			&citation.CitationID, &citation.CitationKey, &citation.ProjectID,
			&citation.DatasetID, &citation.DatasetVersionID, &citation.DocumentID,
			&citation.ChunkID, &citation.SourceType, &citation.SourceID,
			&citation.SourceVersion, &noteID, &mediaPosition,
			&citation.SourceContentHash, &citation.ParserVersion,
			&citation.DocumentStartByte, &citation.DocumentEndByte,
			&citation.SourceStartByte, &citation.SourceEndByte,
			&citation.Quote, &citation.QuoteHash,
		); err != nil {
			return nil, fmt.Errorf("scan citation: %w", err)
		}
		if noteID.Valid {
			value := noteID.Int64
			citation.NoteID = &value
		}
		if mediaPosition.Valid {
			value := int(mediaPosition.Int32)
			citation.MediaPosition = &value
		}
		result[citation.ChunkID] = append(result[citation.ChunkID], citation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate citations: %w", err)
	}
	return result, nil
}

func lexicalIndexSelectSQL() string {
	return `SELECT ingestion_run_id, dataset_version_id, tokenizer_version, index_version,
       status, document_count, chunk_count, lexeme_count, COALESCE(index_checksum,''),
       started_at, completed_at FROM retrieval_lexical_indexes`
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLexicalIndex(row rowScanner, index *LexicalIndex) error {
	var completedAt sql.NullTime
	if err := row.Scan(
		&index.IngestionRunID, &index.DatasetVersionID, &index.TokenizerVersion,
		&index.IndexVersion, &index.Status, &index.DocumentCount, &index.ChunkCount,
		&index.LexemeCount, &index.IndexChecksum, &index.StartedAt, &completedAt,
	); err != nil {
		return err
	}
	if completedAt.Valid {
		index.CompletedAt = completedAt.Time
	}
	return nil
}

func (r *Repository) markIndexFailed(ingestionRunID string, cause error) {
	message := "unknown lexical index failure"
	if cause != nil {
		message = cause.Error()
	}
	failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.db.ExecContext(failureCtx, `
INSERT INTO retrieval_lexical_indexes (
    ingestion_run_id, dataset_version_id, tokenizer_version, index_version,
    status, document_count, chunk_count, error_message,
    started_at, completed_at, created_at, updated_at
)
SELECT run_id, dataset_version_id, tokenizer_version, $2,
       'failed', document_count, chunk_count, left($3,4000),
       clock_timestamp(), NULL, clock_timestamp(), clock_timestamp()
FROM ingestion_runs
WHERE run_id=$1
ON CONFLICT (ingestion_run_id) DO UPDATE
SET status='failed', error_message=left(EXCLUDED.error_message,4000),
    completed_at=NULL, updated_at=clock_timestamp()
WHERE retrieval_lexical_indexes.status<>'completed'`, ingestionRunID, LexicalIndexVersion, message)
}
