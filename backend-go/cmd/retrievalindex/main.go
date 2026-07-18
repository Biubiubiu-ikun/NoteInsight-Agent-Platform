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
	ingestionRunID := flag.String("ingestion-run-id", "", "completed evidence ingestion run to index")
	timeout := flag.Duration("timeout", 20*time.Minute, "maximum index build duration")
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
	logger := logging.NewForService("noteinsight-retrieval-index", appConfig.App.Env, appConfig.Log.Level)
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

	service := retrieval.NewService(retrieval.NewRepository(db))
	index, err := service.BuildLexicalIndex(ctx, *ingestionRunID)
	if err != nil {
		logger.Error("build lexical index failed", "ingestion_run_id", *ingestionRunID, "error", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(index); err != nil {
		logger.Error("encode lexical index result failed", "error", err)
		os.Exit(1)
	}
}
