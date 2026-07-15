package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/facts"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
)

func main() {
	days := flag.Int("days", 30, "number of UTC days to rebuild")
	runID := flag.String("run-id", "", "materialization run id")
	flag.Parse()
	if *days < 1 || *days > 3650 {
		slog.Error("days must be between 1 and 3650")
		os.Exit(2)
	}
	if *runID == "" {
		*runID = "facts_" + time.Now().UTC().Format("20060102_150405")
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.App.Env, cfg.Log.Level)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := database.NewPostgresDB(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	to := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	from := to.AddDate(0, 0, -*days)
	result, err := facts.NewRepository(db).Materialize(ctx, *runID, from, to)
	if err != nil {
		logger.Error("materialize facts failed", "error", err)
		os.Exit(1)
	}
	logger.Info("fact materialization completed", "run_id", result.RunID, "note_facts", result.NoteFactCount, "user_facts", result.UserFactCount)
}
