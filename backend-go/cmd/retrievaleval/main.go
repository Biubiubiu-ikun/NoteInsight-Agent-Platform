package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
	"creatorinsight/backend-go/internal/retrieval"
	"creatorinsight/backend-go/internal/retrievaleval"
)

func main() {
	runID := flag.String("run-id", "", "immutable evaluation run identifier")
	benchmarkDirectory := flag.String("benchmark-dir", "../evaluation/benchmarks/retrieval_v4", "public frozen benchmark directory")
	split := flag.String("split", "development", "development or holdout")
	inputFile := flag.String("input-file", "", "explicit private holdout JSONL; forbidden for development")
	allowHoldout := flag.Bool("allow-holdout", false, "explicitly authorize a sealed holdout release run")
	allowDatasetOverride := flag.Bool("allow-development-dataset-override", false, "diagnostic-only development run against a non-manifest dataset")
	releaseID := flag.String("release-id", "", "versioned holdout release identifier")
	authorizedUserID := flag.Int64("authorized-user-id", 0, "active project member used for holdout dual-principal cases")
	projectID := flag.Int64("project-id", 1, "retrieval project scope")
	datasetVersionID := flag.Int64("dataset-version-id", 2, "frozen retrieval dataset version")
	ingestionRunID := flag.String("ingestion-run-id", "phase7a_dv2_rebuild_v2_20260718", "completed evidence ingestion run")
	topK := flag.Int("top-k", 10, "retrieval depth used by metrics")
	mode := flag.String("mode", retrieval.ModeLexical, "lexical, vector, or hybrid")
	output := flag.String("output", "", "optional JSON report path")
	strict := flag.Bool("strict", false, "exit non-zero when the development quality gate fails")
	timeout := flag.Duration("timeout", 30*time.Minute, "maximum evaluation duration")
	flag.Parse()
	if *runID == "" {
		*runID = fmt.Sprintf("retrieval_%s_%s", *split, time.Now().UTC().Format("20060102_150405"))
	}

	evalConfig := retrievaleval.Config{
		RunID: *runID, BenchmarkDirectory: *benchmarkDirectory, InputFile: *inputFile,
		Split: *split, ReleaseID: *releaseID, AllowHoldout: *allowHoldout,
		AllowDevelopmentDatasetOverride: *allowDatasetOverride,
		AuthorizedUserID:                *authorizedUserID, ProjectID: *projectID,
		DatasetVersionID: *datasetVersionID, IngestionRunID: *ingestionRunID, TopK: *topK,
		Mode: *mode,
	}
	evalConfig.Normalize()
	if err := evalConfig.Validate(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "invalid evaluation config: %v\n", err)
		os.Exit(2)
	}
	manifest, cases, err := retrievaleval.LoadCases(evalConfig)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "verify evaluation artifacts: %v\n", err)
		os.Exit(1)
	}

	appConfig, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.NewForService("noteinsight-retrieval-eval", appConfig.App.Env, appConfig.Log.Level)
	rootContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(rootContext, *timeout)
	defer cancel()
	db, err := database.NewPostgresDB(ctx, appConfig.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	searchService := retrieval.NewService(retrieval.NewRepository(db))
	if err := searchService.EnableVector(
		retrieval.NewTEIEmbedder(appConfig.Retrieval.EmbeddingURL, appConfig.Retrieval.EmbeddingModel, appConfig.Retrieval.EmbeddingRevision, appConfig.Retrieval.EmbeddingDimension, appConfig.Retrieval.DependencyTimeout),
		retrieval.NewQdrantClient(appConfig.Retrieval.QdrantURL, appConfig.Retrieval.QdrantAPIKey, appConfig.Retrieval.DependencyTimeout),
	); err != nil {
		logger.Error("configure vector retrieval failed", "error", err)
		os.Exit(1)
	}
	report, err := retrievaleval.NewEvaluator(searchService).Run(ctx, evalConfig, manifest, cases)
	if err != nil {
		logger.Error("retrieval evaluation failed", "error", err)
		os.Exit(1)
	}
	publish, err := stageReport(*output, report)
	if err != nil {
		logger.Error("stage retrieval report failed", "error", err)
		os.Exit(1)
	}
	if err := retrievaleval.NewRepository(db).Save(ctx, evalConfig, manifest, report); err != nil {
		logger.Error("persist retrieval evaluation failed", "error", err)
		os.Exit(1)
	}
	if err := publish(); err != nil {
		logger.Error("publish retrieval report failed", "error", err)
		os.Exit(1)
	}
	summary := struct {
		RunID    string                   `json:"run_id"`
		Metrics  retrievaleval.Metrics    `json:"metrics"`
		Gate     retrievaleval.GateResult `json:"development_gate"`
		Failures map[string]int           `json:"failure_counts"`
		Output   string                   `json:"output,omitempty"`
	}{report.RunID, report.Metrics, report.DevelopmentGate, report.FailureCounts, *output}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(summary)
	if *strict && report.Split == "development" && !report.DevelopmentGate.Passed {
		os.Exit(3)
	}
}

func stageReport(path string, report retrievaleval.Report) (func() error, error) {
	if path == "" {
		return func() error { return nil }, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return nil, err
	}
	temporary := abs + ".tmp"
	file, err := os.Create(temporary)
	if err != nil {
		return nil, err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return nil, err
	}
	return func() error { return os.Rename(temporary, abs) }, nil
}
