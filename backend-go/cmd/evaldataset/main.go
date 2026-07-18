package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/dataset"
	"creatorinsight/backend-go/internal/platform/database"
)

func main() {
	projectID := flag.Int64("project-id", 1, "project owning the corpus")
	slug := flag.String("slug", "retrieval-v4-quality", "evaluation dataset slug")
	name := flag.String("name", "Retrieval V4 Quality Corpus", "evaluation dataset name")
	sourceRunID := flag.String("source-run-id", "phase6c_quality_v2_20260715", "completed content corpus run")
	createdBy := flag.Int64("created-by", 0, "optional operator user id")
	timeout := flag.Duration("timeout", 5*time.Minute, "maximum command duration")
	flag.Parse()
	appConfig, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := database.NewPostgresDB(ctx, appConfig.Postgres)
	if err != nil {
		slog.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	repository := dataset.NewRepository(db)
	prepared, err := repository.PrepareCorpusDataset(ctx, *projectID, *slug, *name, *sourceRunID, *createdBy)
	if err != nil {
		slog.Error("prepare evaluation dataset failed", "error", err)
		os.Exit(1)
	}
	snapshot, err := dataset.NewService(repository).Freeze(ctx, prepared.DatasetID, *createdBy)
	if err != nil {
		slog.Error("freeze evaluation dataset failed", "error", err)
		os.Exit(1)
	}
	output := struct {
		Prepared dataset.CorpusDatasetResult `json:"prepared"`
		Snapshot dataset.Snapshot            `json:"snapshot"`
	}{prepared, snapshot}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		slog.Error("encode evaluation dataset failed", "error", err)
		os.Exit(1)
	}
}
