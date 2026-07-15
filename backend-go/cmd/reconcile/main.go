package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/outbox"
	"creatorinsight/backend-go/internal/platform/cache"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
	"creatorinsight/backend-go/internal/reconcile"
)

func main() {
	fullAudit := flag.Bool("full", false, "run an explicit full-table counter audit before rebuilding derived data")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.App.Env, cfg.Log.Level)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Reconcile.Timeout)
	defer cancel()
	pgDB, err := database.NewPostgresDB(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer pgDB.Close()
	redisClient, err := cache.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		logger.Error("connect redis failed", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	recovered, err := outbox.NewRepository(pgDB).RecoverStaleProcessing(ctx, time.Now().Add(-cfg.Worker.OutboxProcessingTimeout))
	if err != nil {
		logger.Error("recover stale outbox events failed", "error", err)
		os.Exit(1)
	}
	repository := reconcile.NewRepository(pgDB)
	fullResult := reconcile.CounterRepairResult{}
	if *fullAudit {
		fullResult, err = repository.ReconcileAllCounters(ctx)
		if err != nil {
			logger.Error("full counter audit failed", "error", err)
			os.Exit(1)
		}
	}
	reconciler := reconcile.New(reconcile.Deps{
		Repository:   repository,
		Redis:        redisClient,
		Logger:       logger,
		Enabled:      true,
		StartupDelay: 0,
		Interval:     cfg.Reconcile.Interval,
		Timeout:      cfg.Reconcile.Timeout,
		RankingLimit: cfg.Reconcile.RankingLimit,
	})
	result, err := reconciler.RunOnce(ctx)
	if err != nil {
		logger.Error("reconcile failed", "error", err)
		os.Exit(1)
	}
	logger.Info("reconcile completed",
		"full_audit", *fullAudit,
		"stale_outbox_recovered", recovered,
		"notes_repaired", result.NotesRepaired+fullResult.NotesRepaired,
		"comments_repaired", result.CommentsRepaired+fullResult.CommentsRepaired,
		"note_ranking_keys", result.NoteRankingKeys,
		"comment_ranking_keys", result.CommentRankingKeys,
		"cache_keys_invalidated", result.InvalidatedCacheKeys,
	)
}
