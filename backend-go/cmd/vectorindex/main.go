package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
	"creatorinsight/backend-go/internal/retrieval"
)

func main() {
	ingestionRunID := flag.String("ingestion-run-id", "", "completed evidence ingestion run to embed")
	timeout := flag.Duration("timeout", 6*time.Hour, "maximum vector index build duration")
	flag.Parse()
	*ingestionRunID = strings.TrimSpace(*ingestionRunID)
	if *ingestionRunID == "" {
		slog.Error("ingestion-run-id is required")
		os.Exit(2)
	}
	appConfig, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.NewForService("noteinsight-vector-index", appConfig.App.Env, appConfig.Log.Level)
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
	indexer := retrieval.NewVectorIndexer(
		retrieval.NewRepository(db),
		retrieval.NewTEIEmbedder(
			appConfig.Retrieval.EmbeddingURL, appConfig.Retrieval.EmbeddingModel,
			appConfig.Retrieval.EmbeddingRevision, appConfig.Retrieval.EmbeddingDimension,
			appConfig.Retrieval.DependencyTimeout,
		),
		retrieval.NewQdrantClient(
			appConfig.Retrieval.QdrantURL, appConfig.Retrieval.QdrantAPIKey,
			appConfig.Retrieval.DependencyTimeout,
		),
		retrieval.VectorIndexOptions{
			EmbeddingModel: appConfig.Retrieval.EmbeddingModel, EmbeddingRevision: appConfig.Retrieval.EmbeddingRevision,
			VectorDimension: appConfig.Retrieval.EmbeddingDimension, BatchSize: appConfig.Retrieval.EmbeddingBatchSize,
			Logger: logger,
		},
	)
	index, err := indexer.Build(ctx, *ingestionRunID)
	if err != nil {
		logger.Error("build vector index failed", "ingestion_run_id", *ingestionRunID, "error", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(index); err != nil {
		logger.Error("encode vector index result failed", "error", err)
		os.Exit(1)
	}
}
