package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/evidence"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
)

func main() {
	operation := flag.String("operation", "ingest", "ingest, rebuild, audit, or reconcile")
	datasetVersionID := flag.Int64("dataset-version-id", 2, "frozen dataset version to ingest")
	runID := flag.String("run-id", "", "stable run id; reuse it to resume a failed run")
	timeout := flag.Duration("timeout", 45*time.Minute, "maximum command duration")
	flag.Parse()

	*operation = strings.ToLower(strings.TrimSpace(*operation))
	if *runID == "" && (*operation == "ingest" || *operation == "rebuild") {
		*runID = fmt.Sprintf("evidence_dv%d_%s", *datasetVersionID, time.Now().UTC().Format("20060102_150405"))
	}
	if *operation != "ingest" && *operation != "rebuild" && *operation != "audit" && *operation != "reconcile" {
		slog.Error("operation must be ingest, rebuild, audit, or reconcile")
		os.Exit(2)
	}
	if (*operation == "audit") && strings.TrimSpace(*runID) == "" {
		slog.Error("run-id is required for audit")
		os.Exit(2)
	}

	appConfig, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.NewForService("noteinsight-evidence", appConfig.App.Env, appConfig.Log.Level)
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
	service := evidence.NewService(evidence.NewRepository(db))

	var output any
	switch *operation {
	case "ingest", "rebuild":
		output, err = service.Ingest(ctx, evidence.IngestRequest{
			RunID:            *runID,
			DatasetVersionID: *datasetVersionID,
			Mode:             map[bool]string{true: "rebuild", false: "incremental"}[*operation == "rebuild"],
			Progress: func(completed int, total int) {
				logger.Info("evidence ingestion progress", "run_id", *runID, "completed", completed, "total", total)
			},
		})
	case "audit":
		output, err = service.Audit(ctx, *runID)
	case "reconcile":
		output, err = service.Reconcile(ctx)
	}
	if err != nil {
		logger.Error("evidence command failed", "operation", *operation, "run_id", *runID, "error", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		logger.Error("encode evidence result failed", "error", err)
		os.Exit(1)
	}
}
