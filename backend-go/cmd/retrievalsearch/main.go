package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/retrieval"
)

func main() {
	query := flag.String("query", "", "retrieval query")
	projectID := flag.Int64("project-id", 1, "project scope")
	datasetVersionID := flag.Int64("dataset-version-id", 2, "frozen dataset version")
	ingestionRunID := flag.String("ingestion-run-id", "phase7a_dv2_rebuild_v2_20260718", "completed ingestion run")
	userID := flag.Int64("user-id", 0, "optional authenticated principal")
	limit := flag.Int("limit", 10, "maximum results")
	mode := flag.String("mode", retrieval.ModeLexical, "lexical, vector, or hybrid")
	flag.Parse()
	if *query == "" {
		_, _ = fmt.Fprintln(os.Stderr, "query is required")
		os.Exit(2)
	}
	appConfig, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.NewPostgresDB(ctx, appConfig.Postgres)
	if err != nil {
		slog.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	service := retrieval.NewService(retrieval.NewRepository(db))
	if err := service.EnableVector(
		retrieval.NewTEIEmbedder(appConfig.Retrieval.EmbeddingURL, appConfig.Retrieval.EmbeddingModel, appConfig.Retrieval.EmbeddingRevision, appConfig.Retrieval.EmbeddingDimension, appConfig.Retrieval.DependencyTimeout),
		retrieval.NewQdrantClient(appConfig.Retrieval.QdrantURL, appConfig.Retrieval.QdrantAPIKey, appConfig.Retrieval.DependencyTimeout),
	); err != nil {
		slog.Error("configure vector retrieval failed", "error", err)
		os.Exit(1)
	}
	response, err := service.Search(ctx, retrieval.Principal{UserID: *userID}, retrieval.SearchInput{
		ProjectID: *projectID, DatasetVersionID: *datasetVersionID,
		IngestionRunID: *ingestionRunID, Query: *query, Mode: *mode, Limit: *limit,
	})
	if err != nil {
		slog.Error("search failed", "error", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(response); err != nil {
		slog.Error("encode search response failed", "error", err)
		os.Exit(1)
	}
}
