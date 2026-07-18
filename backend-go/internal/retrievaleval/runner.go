package retrievaleval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"creatorinsight/backend-go/internal/evalbench"
	"creatorinsight/backend-go/internal/retrieval"
)

type Searcher interface {
	Search(context.Context, retrieval.Principal, retrieval.SearchInput) (retrieval.SearchResponse, error)
}

type Evaluator struct {
	searcher Searcher
}

func NewEvaluator(searcher Searcher) *Evaluator {
	return &Evaluator{searcher: searcher}
}

func (e *Evaluator) Run(ctx context.Context, config Config, manifest evalbench.Manifest, cases []evalbench.Case) (Report, error) {
	config.Normalize()
	if err := config.Validate(); err != nil {
		return Report{}, err
	}
	datasetContractMatched := manifest.DatasetVersionID == config.DatasetVersionID
	if !datasetContractMatched && !config.AllowDevelopmentDatasetOverride {
		return Report{}, fmt.Errorf("benchmark dataset_version_id %d does not match evaluation scope %d", manifest.DatasetVersionID, config.DatasetVersionID)
	}
	if len(cases) == 0 {
		return Report{}, fmt.Errorf("evaluation case set is empty")
	}
	startedAt := time.Now().UTC()
	results := make([]CaseResult, 0, len(cases))
	var scope retrieval.Scope
	var retrieverVersion string
	var rerankerVersion string
	for _, evalCase := range cases {
		projectID := metadataInt64(evalCase.Metadata, "required_project_id")
		if projectID == 0 {
			projectID = metadataInt64(evalCase.Metadata, "project_id")
		}
		if projectID == 0 {
			projectID = config.ProjectID
		}
		principal := retrieval.Principal{}
		if evalCase.TaskType == "authorization_boundary" {
			principal.UserID = config.AuthorizedUserID
		}
		response, err := e.searcher.Search(ctx, principal, retrieval.SearchInput{
			ProjectID:        projectID,
			DatasetVersionID: config.DatasetVersionID,
			IngestionRunID:   config.IngestionRunID,
			Query:            evalCase.Query,
			Mode:             config.Mode,
			Limit:            config.TopK,
		})
		if err != nil {
			return Report{}, fmt.Errorf("evaluate case %s: %w", evalCase.CaseChecksum, err)
		}
		if scope.IngestionRunID == "" && response.Scope.IngestionRunID != "" {
			scope = response.Scope
		}
		if retrieverVersion == "" {
			retrieverVersion = response.RetrieverVersion
			rerankerVersion = response.RerankerVersion
		} else if retrieverVersion != response.RetrieverVersion || rerankerVersion != response.RerankerVersion {
			return Report{}, fmt.Errorf("retrieval implementation version changed during evaluation")
		}
		unauthorizedCount := 0
		unauthorizedMetadataLeak := false
		if evalCase.TaskType == "authorization_boundary" {
			unauthorized, err := e.searcher.Search(ctx, retrieval.Principal{}, retrieval.SearchInput{
				ProjectID:        projectID,
				DatasetVersionID: config.DatasetVersionID,
				IngestionRunID:   config.IngestionRunID,
				Query:            evalCase.Query,
				Mode:             config.Mode,
				Limit:            config.TopK,
			})
			if err != nil {
				return Report{}, fmt.Errorf("evaluate unauthorized principal for case %s: %w", evalCase.CaseChecksum, err)
			}
			unauthorizedCount = len(unauthorized.Results)
			unauthorizedMetadataLeak = leaksUnauthorizedMetadata(unauthorized)
			response.TookMilliseconds += unauthorized.TookMilliseconds
		}
		results = append(results, evaluateCase(evalCase, response, unauthorizedCount, unauthorizedMetadataLeak, config.TopK))
	}
	if scope.IngestionRunID == "" {
		resolved, err := e.searcher.Search(ctx, retrieval.Principal{}, retrieval.SearchInput{
			ProjectID: config.ProjectID, DatasetVersionID: config.DatasetVersionID,
			IngestionRunID: config.IngestionRunID, Query: "检索作用域", Limit: 1,
			Mode: config.Mode,
		})
		if err == nil {
			scope = resolved.Scope
		}
	}
	metrics := aggregate(results)
	taskCases := make(map[string][]CaseResult)
	failures := make(map[string]int)
	for _, result := range results {
		taskCases[result.TaskType] = append(taskCases[result.TaskType], result)
		if result.FailureCategory != "" {
			failures[result.FailureCategory]++
		}
	}
	taskMetrics := make(map[string]Metrics, len(taskCases))
	for task, grouped := range taskCases {
		taskMetrics[task] = aggregate(grouped)
	}
	checksum, err := evaluationConfigChecksum(config, manifest, scope, retrieverVersion, rerankerVersion)
	if err != nil {
		return Report{}, err
	}
	gate := developmentGate(metrics)
	gate.Checks["dataset_contract_matched"] = datasetContractMatched
	gate.Values["dataset_contract_matched"] = map[bool]float64{true: 1, false: 0}[datasetContractMatched]
	gate.Passed = gate.Passed && datasetContractMatched
	return Report{
		RunID:                     config.RunID,
		BenchmarkID:               manifest.BenchmarkID,
		BenchmarkVersion:          manifest.BenchmarkVersion,
		BenchmarkManifestChecksum: manifest.ManifestChecksum,
		Split:                     config.Split,
		ReleaseID:                 config.ReleaseID,
		Scope:                     scope,
		RetrieverVersion:          retrieverVersion,
		RerankerVersion:           rerankerVersion,
		MetricVersion:             retrieval.MetricVersion,
		ConfigChecksum:            checksum,
		Metrics:                   metrics,
		TaskMetrics:               taskMetrics,
		FailureCounts:             failures,
		Cases:                     results,
		StartedAt:                 startedAt,
		CompletedAt:               time.Now().UTC(),
		DevelopmentGate:           gate,
		DatasetContractMatched:    datasetContractMatched,
	}, nil
}

func leaksUnauthorizedMetadata(response retrieval.SearchResponse) bool {
	return response.CandidateCount != 0 || len(response.Query.IndexedTerms) != 0 ||
		response.Scope.DatasetManifestChecksum != "" || response.Scope.IngestionRunID != "" ||
		response.Scope.IngestionOutputChecksum != "" || response.Scope.LexicalIndexChecksum != "" ||
		response.Scope.VectorIndexChecksum != "" || response.Scope.VectorCollection != "" ||
		response.Scope.EmbeddingModel != "" || response.Scope.EmbeddingRevision != ""
}

func evaluationConfigChecksum(config Config, manifest evalbench.Manifest, scope retrieval.Scope, retrieverVersion string, rerankerVersion string) (string, error) {
	canonical := struct {
		BenchmarkID          string  `json:"benchmark_id"`
		ManifestChecksum     string  `json:"manifest_checksum"`
		Split                string  `json:"split"`
		ReleaseID            string  `json:"release_id,omitempty"`
		IngestionRunID       string  `json:"ingestion_run_id"`
		IngestionChecksum    string  `json:"ingestion_checksum"`
		LexicalIndexChecksum string  `json:"lexical_index_checksum"`
		VectorIndexChecksum  string  `json:"vector_index_checksum"`
		Mode                 string  `json:"mode"`
		RetrieverVersion     string  `json:"retriever_version"`
		RerankerVersion      string  `json:"reranker_version"`
		MetricVersion        string  `json:"metric_version"`
		TopK                 int     `json:"top_k"`
		MinimumConfidence    float64 `json:"minimum_confidence"`
	}{
		BenchmarkID: manifest.BenchmarkID, ManifestChecksum: manifest.ManifestChecksum,
		Split: config.Split, ReleaseID: config.ReleaseID,
		IngestionRunID: scope.IngestionRunID, IngestionChecksum: scope.IngestionOutputChecksum,
		LexicalIndexChecksum: scope.LexicalIndexChecksum, VectorIndexChecksum: scope.VectorIndexChecksum,
		Mode: config.Mode, RetrieverVersion: retrieverVersion,
		RerankerVersion: rerankerVersion, MetricVersion: retrieval.MetricVersion,
		TopK: config.TopK, MinimumConfidence: evaluationThreshold(config.Mode),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode evaluation config: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func evaluationThreshold(mode string) float64 {
	switch mode {
	case retrieval.ModeVector:
		return retrieval.MinimumVectorScore
	case retrieval.ModeHybrid:
		return retrieval.MinimumHybridScore
	default:
		return retrieval.MinimumConfidence
	}
}

func metadataInt64(metadata map[string]any, key string) int64 {
	value, found := metadata[key]
	if !found {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}
