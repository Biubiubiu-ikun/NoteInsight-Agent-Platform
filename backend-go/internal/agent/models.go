package agent

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	DefaultPromptKey     = "insight-agent-system"
	DefaultPromptVersion = "v1"
	DefaultListLimit     = 20
	MaxListLimit         = 100
	MaxQueryRunes        = 2000
)

var (
	ErrInvalidInput        = errors.New("invalid agent input")
	ErrNotFound            = errors.New("agent run not found")
	ErrForbidden           = errors.New("agent operation forbidden")
	ErrConflict            = errors.New("agent run state conflict")
	ErrIdempotencyConflict = errors.New("agent idempotency key reused with different input")
	ErrScopeUnavailable    = errors.New("agent retrieval scope is unavailable")
)

type Budget struct {
	MaxSteps          int   `json:"max_steps"`
	MaxRetrievalCalls int   `json:"max_retrieval_calls"`
	MaxModelCalls     int   `json:"max_model_calls"`
	MaxInputTokens    int   `json:"max_input_tokens"`
	MaxOutputTokens   int   `json:"max_output_tokens"`
	MaxDurationMS     int64 `json:"max_duration_ms"`
	MaxCostMicros     int64 `json:"max_cost_micros"`
}

type Usage struct {
	Steps          int   `json:"steps"`
	RetrievalCalls int   `json:"retrieval_calls"`
	ModelCalls     int   `json:"model_calls"`
	InputTokens    int64 `json:"input_tokens"`
	OutputTokens   int64 `json:"output_tokens"`
	CostMicros     int64 `json:"cost_micros"`
}

type PromptVersion struct {
	ID             int64     `json:"id"`
	PromptKey      string    `json:"prompt_key"`
	Version        string    `json:"version"`
	Purpose        string    `json:"purpose"`
	TemplateSHA256 string    `json:"template_sha256"`
	CreatedAt      time.Time `json:"created_at"`
}

type ModelVersion struct {
	ID             int64           `json:"id"`
	Provider       string          `json:"provider"`
	Model          string          `json:"model"`
	Revision       string          `json:"revision"`
	Parameters     json.RawMessage `json:"parameters"`
	ArtifactSHA256 string          `json:"artifact_sha256,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type RetrievalContract struct {
	Mode                    string `json:"mode"`
	ProjectID               int64  `json:"project_id"`
	DatasetID               int64  `json:"dataset_id"`
	DatasetVersionID        int64  `json:"dataset_version_id"`
	DatasetManifestChecksum string `json:"dataset_manifest_checksum"`
	IngestionRunID          string `json:"ingestion_run_id"`
	IngestionOutputChecksum string `json:"ingestion_output_checksum"`
	ParserVersion           string `json:"parser_version"`
	ChunkerVersion          string `json:"chunker_version"`
	TokenizerVersion        string `json:"tokenizer_version"`
	LexicalIndexVersion     string `json:"lexical_index_version,omitempty"`
	LexicalIndexChecksum    string `json:"lexical_index_checksum,omitempty"`
	VectorIndexVersion      string `json:"vector_index_version,omitempty"`
	VectorIndexChecksum     string `json:"vector_index_checksum,omitempty"`
	EmbeddingModel          string `json:"embedding_model,omitempty"`
	EmbeddingRevision       string `json:"embedding_revision,omitempty"`
	AccessScope             string `json:"access_scope"`
}

type Run struct {
	ID                    string            `json:"id"`
	ProjectID             int64             `json:"project_id"`
	DatasetVersionID      int64             `json:"dataset_version_id"`
	IngestionRunID        string            `json:"ingestion_run_id"`
	RequestedBy           int64             `json:"requested_by"`
	Query                 string            `json:"query"`
	RequestedMode         string            `json:"requested_mode"`
	Intent                json.RawMessage   `json:"intent"`
	RetrievalPlan         RetrievalContract `json:"retrieval_plan"`
	Report                json.RawMessage   `json:"report,omitempty"`
	Prompt                PromptVersion     `json:"prompt"`
	Model                 *ModelVersion     `json:"model,omitempty"`
	Status                string            `json:"status"`
	Budget                Budget            `json:"budget"`
	Usage                 Usage             `json:"usage"`
	CancellationRequested bool              `json:"cancellation_requested"`
	IdempotencyKey        string            `json:"idempotency_key,omitempty"`
	RequestID             string            `json:"request_id,omitempty"`
	TraceID               string            `json:"trace_id,omitempty"`
	FailureCode           string            `json:"failure_code,omitempty"`
	FailureMessage        string            `json:"failure_message,omitempty"`
	StartedAt             *time.Time        `json:"started_at,omitempty"`
	CompletedAt           *time.Time        `json:"completed_at,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

type CreateRunInput struct {
	ProjectID        int64
	DatasetVersionID int64
	IngestionRunID   string
	Query            string
	Mode             string
	Budget           Budget
	IdempotencyKey   string
}

type CreateResult struct {
	Run      Run  `json:"run"`
	Replayed bool `json:"replayed"`
}

type ListRunsInput struct {
	Limit  int
	Cursor string
}

type RunPage struct {
	Items      []Run  `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type createRecord struct {
	ProjectID        int64
	DatasetVersionID int64
	IngestionRunID   string
	RequestedBy      int64
	Query            string
	Mode             string
	Intent           json.RawMessage
	RetrievalPlan    RetrievalContract
	PromptVersionID  int64
	Budget           Budget
	IdempotencyKey   string
	RequestHash      string
	RequestID        string
	TraceID          string
}

type runCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}
