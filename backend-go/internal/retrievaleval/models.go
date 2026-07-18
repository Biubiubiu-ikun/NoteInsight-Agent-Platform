package retrievaleval

import (
	"fmt"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/evalbench"
	"creatorinsight/backend-go/internal/retrieval"
)

type Config struct {
	RunID                           string `json:"run_id"`
	BenchmarkDirectory              string `json:"benchmark_directory"`
	InputFile                       string `json:"input_file"`
	Split                           string `json:"split"`
	ReleaseID                       string `json:"release_id,omitempty"`
	AllowHoldout                    bool   `json:"allow_holdout"`
	AllowDevelopmentDatasetOverride bool   `json:"allow_development_dataset_override"`
	AuthorizedUserID                int64  `json:"authorized_user_id,omitempty"`
	ProjectID                       int64  `json:"project_id"`
	DatasetVersionID                int64  `json:"dataset_version_id"`
	IngestionRunID                  string `json:"ingestion_run_id"`
	TopK                            int    `json:"top_k"`
	Mode                            string `json:"mode"`
}

func (c *Config) Normalize() {
	c.RunID = strings.TrimSpace(c.RunID)
	c.BenchmarkDirectory = strings.TrimSpace(c.BenchmarkDirectory)
	c.InputFile = strings.TrimSpace(c.InputFile)
	c.Split = strings.ToLower(strings.TrimSpace(c.Split))
	c.ReleaseID = strings.TrimSpace(c.ReleaseID)
	c.IngestionRunID = strings.TrimSpace(c.IngestionRunID)
	if c.Split == "" {
		c.Split = "development"
	}
	if c.ProjectID == 0 {
		c.ProjectID = 1
	}
	if c.TopK == 0 {
		c.TopK = 10
	}
	if c.Mode == "" {
		c.Mode = retrieval.ModeLexical
	}
}

func (c Config) Validate() error {
	if c.RunID == "" || len(c.RunID) > 128 {
		return fmt.Errorf("run_id is required and must be at most 128 characters")
	}
	if c.BenchmarkDirectory == "" || c.IngestionRunID == "" {
		return fmt.Errorf("benchmark_directory and ingestion_run_id are required")
	}
	if c.Split != "development" && c.Split != "holdout" {
		return fmt.Errorf("split must be development or holdout")
	}
	if c.Split == "development" && c.InputFile != "" {
		return fmt.Errorf("development evaluation must use the verified public artifact")
	}
	if c.Split == "holdout" {
		if !c.AllowHoldout || c.ReleaseID == "" || c.InputFile == "" {
			return fmt.Errorf("holdout evaluation requires allow_holdout, release_id, and an explicit private input_file")
		}
		if c.AuthorizedUserID <= 0 {
			return fmt.Errorf("holdout release evaluation requires an authorized_user_id for dual-principal cases")
		}
	}
	if c.Split != "development" && c.AllowDevelopmentDatasetOverride {
		return fmt.Errorf("dataset override is allowed only for development diagnostics")
	}
	if c.ProjectID <= 0 || c.DatasetVersionID <= 0 || c.TopK < 1 || c.TopK > retrieval.MaxLimit {
		return fmt.Errorf("project_id, dataset_version_id, and top_k are outside their valid ranges")
	}
	if c.Mode != "" && c.Mode != retrieval.ModeLexical && c.Mode != retrieval.ModeVector && c.Mode != retrieval.ModeHybrid {
		return fmt.Errorf("mode must be lexical, vector, or hybrid")
	}
	return nil
}

type Report struct {
	RunID                     string             `json:"run_id"`
	BenchmarkID               string             `json:"benchmark_id"`
	BenchmarkVersion          string             `json:"benchmark_version"`
	BenchmarkManifestChecksum string             `json:"benchmark_manifest_checksum"`
	Split                     string             `json:"split"`
	ReleaseID                 string             `json:"release_id,omitempty"`
	Scope                     retrieval.Scope    `json:"scope"`
	RetrieverVersion          string             `json:"retriever_version"`
	RerankerVersion           string             `json:"reranker_version"`
	MetricVersion             string             `json:"metric_version"`
	ConfigChecksum            string             `json:"config_checksum"`
	Metrics                   Metrics            `json:"metrics"`
	TaskMetrics               map[string]Metrics `json:"task_metrics"`
	FailureCounts             map[string]int     `json:"failure_counts"`
	Cases                     []CaseResult       `json:"cases"`
	StartedAt                 time.Time          `json:"started_at"`
	CompletedAt               time.Time          `json:"completed_at"`
	DevelopmentGate           GateResult         `json:"development_gate"`
	DatasetContractMatched    bool               `json:"dataset_contract_matched"`
}

type Metrics struct {
	CaseCount                        int     `json:"case_count"`
	GoldCaseCount                    int     `json:"gold_case_count"`
	RecallAtK                        float64 `json:"recall_at_k"`
	MRRAtK                           float64 `json:"mrr_at_k"`
	NDCGAtK                          float64 `json:"ndcg_at_k"`
	CitationPrecision                float64 `json:"citation_precision"`
	SourcePrecisionAtK               float64 `json:"source_precision_at_k"`
	NoRelevantCaseCount              int     `json:"no_relevant_case_count"`
	NoRelevantRejectionAccuracy      float64 `json:"no_relevant_rejection_accuracy"`
	InsufficientEvidenceCaseCount    int     `json:"insufficient_evidence_case_count"`
	InsufficientEvidenceSourceRecall float64 `json:"insufficient_evidence_source_recall"`
	AuthorizationCaseCount           int     `json:"authorization_case_count"`
	AuthorizationNonLeakageAccuracy  float64 `json:"authorization_non_leakage_accuracy"`
	LatencyP50Milliseconds           float64 `json:"latency_p50_ms"`
	LatencyP95Milliseconds           float64 `json:"latency_p95_ms"`
	LatencyP99Milliseconds           float64 `json:"latency_p99_ms"`
	ExternalModelCalls               int     `json:"external_model_calls"`
	EstimatedExternalCostUSD         float64 `json:"estimated_external_cost_usd"`
	EmbeddingCalls                   int     `json:"embedding_calls"`
}

type GateResult struct {
	Passed bool               `json:"passed"`
	Checks map[string]bool    `json:"checks"`
	Values map[string]float64 `json:"values"`
}

type CaseResult struct {
	CaseChecksum             string                 `json:"case_checksum"`
	TaskType                 string                 `json:"task_type"`
	Answerable               bool                   `json:"answerable"`
	GoldSources              []evalbench.GoldSource `json:"gold_sources"`
	RetrievedSources         []RetrievedSource      `json:"retrieved_sources"`
	Metrics                  CaseMetrics            `json:"metrics"`
	FailureCategory          string                 `json:"failure_category,omitempty"`
	LatencyMilliseconds      float64                `json:"latency_ms"`
	ResultCount              int                    `json:"result_count"`
	CandidateCount           int                    `json:"candidate_count"`
	DecisionStatus           string                 `json:"decision_status"`
	EmbeddingCalls           int                    `json:"embedding_calls"`
	UnauthorizedResultCount  int                    `json:"unauthorized_result_count,omitempty"`
	UnauthorizedMetadataLeak bool                   `json:"unauthorized_metadata_leak,omitempty"`
}

type CaseMetrics struct {
	RecallAtK          float64 `json:"recall_at_k"`
	ReciprocalRank     float64 `json:"reciprocal_rank"`
	NDCGAtK            float64 `json:"ndcg_at_k"`
	CitationPrecision  float64 `json:"citation_precision"`
	SourcePrecisionAtK float64 `json:"source_precision_at_k"`
	Rejected           bool    `json:"rejected"`
	AuthorizationLeak  bool    `json:"authorization_leak"`
	TopConfidence      float64 `json:"top_confidence"`
}

type RetrievedSource struct {
	Rank          int     `json:"rank"`
	Confidence    float64 `json:"confidence"`
	SourceType    string  `json:"source_type"`
	SourceID      int64   `json:"source_id"`
	SourceVersion int64   `json:"source_version"`
	NoteID        int64   `json:"note_id,omitempty"`
	Position      int     `json:"position,omitempty"`
	CitationKey   string  `json:"citation_key"`
}
