package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"creatorinsight/backend-go/internal/api"
	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/cache"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.App.Env, cfg.Log.Level)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pgDB, err := database.NewPostgresDB(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := pgDB.Close(); closeErr != nil {
			logger.Warn("close postgres failed", "error", closeErr)
		}
	}()

	redisClient, err := cache.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		logger.Error("connect redis failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := redisClient.Close(); closeErr != nil {
			logger.Warn("close redis failed", "error", closeErr)
		}
	}()

	router := api.NewRouter(api.RouterDeps{
		Config: cfg,
		Logger: logger,
		DB:     pgDB,
		Redis:  redisClient,
	})

	server := &http.Server{
		Addr:              cfg.HTTP.Addr(),
		Handler:           router,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api server starting", "addr", cfg.HTTP.Addr(), "env", cfg.App.Env)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("api server failed", "error", serveErr)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	logger.Info("api server stopped")
}
