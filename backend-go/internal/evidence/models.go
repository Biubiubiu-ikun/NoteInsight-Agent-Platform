package evidence

import (
	"encoding/json"
	"time"
)

const (
	ParserVersion      = "evidence_parser_v1"
	ChunkerVersion     = "utf8_paragraph_1200_160_v1"
	TokenizerVersion   = "zh_unigram_bigram_latin_v1"
	MaxChunkBytes      = 1200
	ChunkOverlap       = 160
	CommentClusterSize = 12
	IngestConcurrency  = 8
	ReuseBatchSize     = 500
)

type Run struct {
	RunID                   string    `json:"run_id"`
	DatasetVersionID        int64     `json:"dataset_version_id"`
	DatasetID               int64     `json:"dataset_id"`
	ProjectID               int64     `json:"project_id"`
	DatasetVersion          int64     `json:"dataset_version"`
	Mode                    string    `json:"mode"`
	ParserVersion           string    `json:"parser_version"`
	ChunkerVersion          string    `json:"chunker_version"`
	TokenizerVersion        string    `json:"tokenizer_version"`
	Status                  string    `json:"status"`
	DatasetManifestChecksum string    `json:"dataset_manifest_checksum"`
	InputChecksum           string    `json:"input_checksum,omitempty"`
	OutputChecksum          string    `json:"output_checksum,omitempty"`
	SourceCount             int64     `json:"source_count"`
	FactSourceCount         int64     `json:"fact_source_count"`
	DocumentCount           int64     `json:"document_count"`
	ChunkCount              int64     `json:"chunk_count"`
	CitationCount           int64     `json:"citation_count"`
	ReusedDocumentCount     int64     `json:"reused_document_count"`
	StartedAt               time.Time `json:"started_at"`
	CompletedAt             time.Time `json:"completed_at,omitempty"`
	Resumed                 bool      `json:"resumed"`
}

type IngestRequest struct {
	RunID            string
	DatasetVersionID int64
	Mode             string
	Progress         func(completed int, total int)
}

type SourceInput struct {
	EvidenceSourceID int64
	ProjectID        int64
	DatasetID        int64
	DatasetVersionID int64
	DatasetVersion   int64
	SourceType       string
	SourceID         int64
	SourceVersion    int64
	ContentHash      string
	Visibility       string
	CanonicalText    string
	SourcePayload    json.RawMessage
}

type FactInput struct {
	DailyFactPayloadID int64
	ProjectID          int64
	DatasetID          int64
	DatasetVersionID   int64
	DatasetVersion     int64
	FactType           string
	SubjectID          int64
	FactDate           time.Time
	SourceVersion      int64
	ContentHash        string
	SourcePayload      json.RawMessage
	CapturedAt         time.Time
}

type DocumentSource struct {
	EvidenceSourceID   *int64
	DailyFactPayloadID *int64
	SourceType         string
	SourceID           int64
	SourceVersion      int64
	ContentHash        string
	SourceOrder        int
	CanonicalText      string
}

type DocumentInput struct {
	DocumentKey       string
	ProjectID         int64
	DatasetID         int64
	DatasetVersionID  int64
	DocumentType      string
	SourceType        string
	SourceID          *int64
	SourceKey         string
	SourceVersion     int64
	SourceContentHash string
	ParserVersion     string
	Visibility        string
	CanonicalText     string
	ContentHash       string
	Metadata          json.RawMessage
	SourceCreatedAt   *time.Time
	SourceUpdatedAt   *time.Time
	Sources           []DocumentSource
	Chunks            []ChunkInput
}

type ChunkInput struct {
	ChunkKey         string
	ChunkIndex       int
	StartByte        int
	EndByte          int
	StartRune        int
	EndRune          int
	Content          string
	ContentHash      string
	ChunkerVersion   string
	TokenizerVersion string
	Lexemes          string
	Metadata         json.RawMessage
	Citations        []CitationInput
}

type CitationInput struct {
	CitationKey        string
	EvidenceSourceID   *int64
	DailyFactPayloadID *int64
	SourceType         string
	SourceID           int64
	SourceVersion      int64
	SourceContentHash  string
	DocumentStartByte  int
	DocumentEndByte    int
	SourceStartByte    int
	SourceEndByte      int
	QuoteHash          string
}

type SaveResult struct {
	DocumentID    int64
	Reused        bool
	ChunkCount    int64
	CitationCount int64
}

type ReconcileResult struct {
	StaleDocuments      int64 `json:"stale_documents"`
	SupersededDocuments int64 `json:"superseded_documents"`
	DeletedDocuments    int64 `json:"deleted_documents"`
}

type AuditReport struct {
	RunID      string           `json:"run_id"`
	CheckedAt  time.Time        `json:"checked_at"`
	Violations map[string]int64 `json:"violations"`
	Healthy    bool             `json:"healthy"`
}
