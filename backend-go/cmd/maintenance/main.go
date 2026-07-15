package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"

	"github.com/jmoiron/sqlx"
)

type cleanupTarget struct {
	name   string
	query  string
	cutoff time.Time
}

func main() {
	apply := flag.Bool("apply", false, "delete expired operational records; default is dry-run")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.App.Env, cfg.Log.Level)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := database.NewPostgresDB(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	now := time.Now().UTC()
	targets := []cleanupTarget{
		{name: "sent_outbox", query: "outbox_events WHERE status = 'sent' AND sent_at < $1", cutoff: now.Add(-7 * 24 * time.Hour)},
		{name: "processed_events", query: "processed_events WHERE processed_at < $1", cutoff: now.Add(-30 * 24 * time.Hour)},
		{name: "expired_sessions", query: "user_sessions WHERE (revoked = TRUE OR expires_at < now()) AND updated_at < $1", cutoff: now.Add(-30 * 24 * time.Hour)},
	}
	if err := runCleanup(ctx, db, logger, targets, *apply); err != nil {
		logger.Error("maintenance failed", "error", err)
		os.Exit(1)
	}
}

func runCleanup(ctx context.Context, db *sqlx.DB, logger *slog.Logger, targets []cleanupTarget, apply bool) error {
	for _, target := range targets {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+target.query, target.cutoff).Scan(&count); err != nil {
			return err
		}
		logger.Info("maintenance candidate", "target", target.name, "rows", count, "apply", apply)
		if !apply || count == 0 {
			continue
		}
		result, err := db.ExecContext(ctx, "DELETE FROM "+target.query, target.cutoff)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		logger.Info("maintenance deleted records", "target", target.name, "rows", deleted)
	}
	return nil
}
