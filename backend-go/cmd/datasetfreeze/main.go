package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/dataset"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
)

func main() {
	datasetID := flag.Int64("dataset-id", 1, "dataset to freeze")
	createdBy := flag.Int64("created-by", 0, "optional user that initiated the freeze")
	timeout := flag.Duration("timeout", 10*time.Minute, "maximum snapshot duration")
	flag.Parse()

	appConfig, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.NewForService("noteinsight-datasetfreeze", appConfig.App.Env, appConfig.Log.Level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	db, err := database.NewPostgresDB(ctx, appConfig.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	service := dataset.NewService(dataset.NewRepository(db))
	snapshot, err := service.Freeze(ctx, *datasetID, *createdBy)
	if err != nil {
		logger.Error("freeze dataset failed", "dataset_id", *datasetID, "error", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "encode snapshot: %v\n", err)
		os.Exit(1)
	}
}
