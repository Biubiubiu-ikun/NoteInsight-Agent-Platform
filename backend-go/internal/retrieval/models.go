package retrieval

import (
	"errors"
	"time"
)

const (
	ModeLexical            = "lexical"
	ModeVector             = "vector"
	ModeHybrid             = "hybrid"
	LexicalIndexVersion    = "postgres_ts_stat_v1"
	VectorIndexVersion     = "qwen3_dense_cosine_v1"
	RetrieverVersion       = "postgres_fts_lexical_v2"
	VectorRetrieverVersion = "qdrant_qwen3_dense_v1"
	HybridRetrieverVersion = "rrf_lexical_dense_v2"
	RerankerVersion        = "weighted_coverage_v2"
	MetricVersion          = "retrieval_metrics_v2"

	DefaultLimit          = 10
	MaxLimit              = 50
	DefaultCandidateLimit = 240
	MaxQueryRunes         = 2000
	MaxQueryTerms         = 64
	MaxChunksPerNote      = 2
	MinimumConfidence     = 0.30
	MinimumVectorScore    = 0.45
	MinimumHybridScore    = 0.60
)

var (
	ErrInvalidInput          = errors.New("invalid retrieval input")
	ErrScopeNotFound         = errors.New("retrieval scope not found")
	ErrIndexNotReady         = errors.New("retrieval index is not ready")
	ErrUnsupportedMode       = errors.New("retrieval mode is not implemented")
	ErrIndexVersionMismatch  = errors.New("retrieval index version mismatch")
	ErrDependencyUnavailable = errors.New("retrieval dependency unavailable")
)

type Principal struct {
	UserID int64
}

type Filters struct {
	DocumentTypes []string `json:"document_types,omitempty"`
	SourceTypes   []string `json:"source_types,omitempty"`
}

type SearchInput struct {
	ProjectID        int64   `json:"project_id"`
	DatasetVersionID int64   `json:"dataset_version_id"`
	IngestionRunID   string  `json:"ingestion_run_id,omitempty"`
	Query            string  `json:"query"`
	Mode             string  `json:"mode,omitempty"`
	Limit            int     `json:"limit,omitempty"`
	Filters          Filters `json:"filters,omitempty"`
}

type Scope struct {
	ProjectID               int64  `json:"project_id"`
	ProjectVisibility       string `json:"project_visibility"`
	DatasetID               int64  `json:"dataset_id"`
	DatasetVersionID        int64  `json:"dataset_version_id"`
	DatasetVersion          int64  `json:"dataset_version"`
	DatasetManifestChecksum string `json:"dataset_manifest_checksum"`
	IngestionRunID          string `json:"ingestion_run_id"`
	IngestionOutputChecksum string `json:"ingestion_output_checksum"`
	ParserVersion           string `json:"parser_version"`
	ChunkerVersion          string `json:"chunker_version"`
	TokenizerVersion        string `json:"tokenizer_version"`
	LexicalIndexVersion     string `json:"lexical_index_version"`
	LexicalIndexChecksum    string `json:"lexical_index_checksum"`
	VectorIndexVersion      string `json:"vector_index_version,omitempty"`
	VectorIndexChecksum     string `json:"vector_index_checksum,omitempty"`
	VectorCollection        string `json:"vector_collection,omitempty"`
	EmbeddingModel          string `json:"embedding_model,omitempty"`
	EmbeddingRevision       string `json:"embedding_revision,omitempty"`
	AccessScope             string `json:"-"`
}

type SearchResponse struct {
	Mode               string         `json:"mode"`
	RetrieverVersion   string         `json:"retriever_version"`
	RerankerVersion    string         `json:"reranker_version"`
	Scope              Scope          `json:"scope"`
	Query              QuerySummary   `json:"query"`
	Decision           SearchDecision `json:"decision"`
	Results            []Result       `json:"results"`
	CandidateCount     int            `json:"candidate_count"`
	TookMilliseconds   float64        `json:"took_ms"`
	ExternalModelCalls int            `json:"external_model_calls"`
	EmbeddingCalls     int            `json:"embedding_calls"`
}

type QuerySummary struct {
	Original       string   `json:"original"`
	Terms          []string `json:"terms"`
	IndexedTerms   []string `json:"indexed_terms"`
	CandidateTerms []string `json:"candidate_terms"`
	HintedNoteIDs  []int64  `json:"hinted_note_ids,omitempty"`
	PreferredType  string   `json:"preferred_document_type,omitempty"`
}

type SearchDecision struct {
	Status        string  `json:"status"`
	Reason        string  `json:"reason"`
	Threshold     float64 `json:"threshold"`
	TopConfidence float64 `json:"top_confidence"`
}

type Result struct {
	Rank             int        `json:"rank"`
	Score            float64    `json:"score"`
	Confidence       float64    `json:"confidence"`
	FTSScore         float64    `json:"fts_score"`
	VectorScore      float64    `json:"vector_score,omitempty"`
	HybridScore      float64    `json:"hybrid_score,omitempty"`
	WeightedCoverage float64    `json:"weighted_coverage"`
	DocumentID       int64      `json:"document_id"`
	DocumentKey      string     `json:"document_key"`
	DocumentType     string     `json:"document_type"`
	SourceType       string     `json:"source_type"`
	SourceID         *int64     `json:"source_id,omitempty"`
	SourceVersion    int64      `json:"source_version"`
	NoteID           *int64     `json:"note_id,omitempty"`
	MediaPosition    *int       `json:"media_position,omitempty"`
	ChunkID          int64      `json:"chunk_id"`
	ChunkKey         string     `json:"chunk_key"`
	ChunkIndex       int        `json:"chunk_index"`
	Content          string     `json:"content"`
	ContentHash      string     `json:"content_hash"`
	StartByte        int        `json:"start_byte"`
	EndByte          int        `json:"end_byte"`
	Citations        []Citation `json:"citations"`
}

type Citation struct {
	CitationID        int64  `json:"citation_id"`
	CitationKey       string `json:"citation_key"`
	ProjectID         int64  `json:"project_id"`
	DatasetID         int64  `json:"dataset_id"`
	DatasetVersionID  int64  `json:"dataset_version_id"`
	DocumentID        int64  `json:"document_id"`
	ChunkID           int64  `json:"chunk_id"`
	SourceType        string `json:"source_type"`
	SourceID          int64  `json:"source_id"`
	SourceVersion     int64  `json:"source_version"`
	NoteID            *int64 `json:"note_id,omitempty"`
	MediaPosition     *int   `json:"media_position,omitempty"`
	SourceContentHash string `json:"source_content_hash"`
	ParserVersion     string `json:"parser_version"`
	DocumentStartByte int    `json:"document_start_byte"`
	DocumentEndByte   int    `json:"document_end_byte"`
	SourceStartByte   int    `json:"source_start_byte"`
	SourceEndByte     int    `json:"source_end_byte"`
	Quote             string `json:"quote"`
	QuoteHash         string `json:"quote_hash"`
}

type TermStat struct {
	Lexeme                   string
	ChunkFrequency           int64
	OccurrenceCount          int64
	InverseDocumentFrequency float64
}

type Candidate struct {
	DocumentID    int64
	DocumentKey   string
	DocumentType  string
	SourceType    string
	SourceID      *int64
	SourceVersion int64
	NoteID        *int64
	MediaPosition *int
	ChunkID       int64
	ChunkKey      string
	ChunkIndex    int
	Content       string
	ContentHash   string
	Lexemes       string
	StartByte     int
	EndByte       int
	FTSScore      float64
	VectorScore   float64
	HybridScore   float64
	TrigramScore  float64
}

type LexicalIndex struct {
	IngestionRunID   string    `json:"ingestion_run_id"`
	DatasetVersionID int64     `json:"dataset_version_id"`
	TokenizerVersion string    `json:"tokenizer_version"`
	IndexVersion     string    `json:"index_version"`
	Status           string    `json:"status"`
	DocumentCount    int64     `json:"document_count"`
	ChunkCount       int64     `json:"chunk_count"`
	LexemeCount      int64     `json:"lexeme_count"`
	IndexChecksum    string    `json:"index_checksum"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
}

type VectorIndex struct {
	IngestionRunID    string    `json:"ingestion_run_id"`
	IndexVersion      string    `json:"index_version"`
	EmbeddingModel    string    `json:"embedding_model"`
	EmbeddingRevision string    `json:"embedding_revision"`
	VectorDimension   int       `json:"vector_dimension"`
	DistanceMetric    string    `json:"distance_metric"`
	CollectionName    string    `json:"collection_name"`
	Status            string    `json:"status"`
	PointCount        int64     `json:"point_count"`
	IndexChecksum     string    `json:"index_checksum"`
	StartedAt         time.Time `json:"started_at"`
	CompletedAt       time.Time `json:"completed_at,omitempty"`
}

type VectorChunk struct {
	ChunkID            int64
	Content            string
	ContentHash        string
	DocumentID         int64
	DocumentType       string
	SourceType         string
	SourceID           *int64
	SourceVersion      int64
	NoteID             *int64
	MediaPosition      *int
	ProjectID          int64
	ProjectVisibility  string
	DocumentVisibility string
	DocumentLifecycle  string
}

type QueryPlan struct {
	Original          string
	Terms             []string
	AllTerms          []string
	HintedNoteIDs     []int64
	PreferredType     string
	PreferredPosition *int
	SubjectTerms      []string
}
