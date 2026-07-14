package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
	"creatorinsight/backend-go/internal/platform/migration"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.App.Env, cfg.Log.Level)
	slog.SetDefault(logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, err := database.NewPostgresDB(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	migrationsDir := os.Getenv("MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "migrations"
	}

	result, err := migration.Apply(ctx, db, migrationsDir)
	if err != nil {
		logger.Error("apply migrations failed", "error", err)
		os.Exit(1)
	}

	logger.Info("migrations finished", "applied", result.Applied, "skipped", result.Skipped)
}
