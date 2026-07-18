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

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/note"
	"creatorinsight/backend-go/internal/outbox"
	"creatorinsight/backend-go/internal/platform/cache"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
	"creatorinsight/backend-go/internal/platform/messaging"
	platformtracing "creatorinsight/backend-go/internal/platform/tracing"
	"creatorinsight/backend-go/internal/reconcile"
	"creatorinsight/backend-go/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.NewForService(cfg.App.Name, cfg.App.Env, cfg.Log.Level)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	traceShutdown, err := platformtracing.Initialize(ctx, cfg.Telemetry, cfg.App.Name, cfg.App.Env, logger)
	if err != nil {
		logger.Error("initialize tracing failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Telemetry.ExportTimeout)
		defer cancel()
		if shutdownErr := traceShutdown(shutdownCtx); shutdownErr != nil {
			logger.Warn("flush tracing failed", "error", shutdownErr)
		}
	}()

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

	broker, err := messaging.NewBroker(ctx, cfg.NATS, logger)
	if err != nil {
		logger.Error("initialize JetStream failed", "error", err)
		os.Exit(1)
	}
	defer broker.Close()

	outboxRepo := outbox.NewRepository(pgDB)
	eventProcessor := worker.NewEventProcessor(worker.EventProcessorDeps{
		EventRepo:    worker.NewRepository(pgDB),
		RankingRepo:  note.NewRepository(pgDB),
		Redis:        redisClient,
		ConsumerName: cfg.NATS.Consumer,
		Logger:       logger,
	})
	eventConsumer := worker.NewEventConsumer(worker.EventConsumerDeps{
		Consumer:         broker.Consumer(),
		Processor:        eventProcessor,
		DeadLetters:      broker,
		Logger:           logger,
		BatchSize:        cfg.Worker.ConsumerBatchSize,
		MaxDeliver:       cfg.NATS.MaxDeliver,
		NakDelay:         cfg.NATS.NakDelay,
		OperationTimeout: cfg.NATS.RequestTimeout,
		MetricsInterval:  cfg.Worker.MetricsInterval,
	})
	if err := eventConsumer.Start(ctx); err != nil {
		logger.Error("start JetStream consumer failed", "error", err)
		os.Exit(1)
	}
	outboxPublisher := worker.NewOutboxPublisher(worker.OutboxPublisherDeps{
		Repository:        outboxRepo,
		Publisher:         broker,
		Logger:            logger,
		BatchSize:         cfg.Worker.OutboxBatchSize,
		MaxRetries:        cfg.Worker.OutboxMaxRetries,
		Interval:          cfg.Worker.OutboxPollInterval,
		RecoveryInterval:  cfg.Worker.OutboxRecoveryInterval,
		ProcessingTimeout: cfg.Worker.OutboxProcessingTimeout,
		MetricsInterval:   cfg.Worker.MetricsInterval,
	})
	outboxPublisher.Start(ctx)

	reconciler := reconcile.New(reconcile.Deps{
		Repository:   reconcile.NewRepository(pgDB),
		Redis:        redisClient,
		Logger:       logger,
		Enabled:      cfg.Reconcile.Enabled,
		StartupDelay: cfg.Reconcile.StartupDelay,
		Interval:     cfg.Reconcile.Interval,
		Timeout:      cfg.Reconcile.Timeout,
		RankingLimit: cfg.Reconcile.RankingLimit,
	})
	reconciler.Start(ctx)

	statusServer := worker.NewStatusServer(worker.StatusServerDeps{
		Host:         cfg.Worker.HTTPHost,
		Port:         cfg.Worker.HTTPPort,
		DB:           pgDB,
		Redis:        redisClient,
		Broker:       broker,
		Logger:       logger,
		CheckTimeout: cfg.NATS.RequestTimeout,
	})
	errCh := make(chan error, 1)
	go func() {
		logger.Info("worker status server starting", "addr", statusServer.Addr)
		errCh <- statusServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("worker shutdown signal received")
	case serverErr := <-errCh:
		if serverErr != nil && !errors.Is(serverErr, http.ErrServerClosed) {
			logger.Error("worker status server failed", "error", serverErr)
		}
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := statusServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("worker status server shutdown failed", "error", err)
	}
	eventConsumer.Wait()
	if err := broker.Close(); err != nil {
		logger.Warn("drain NATS connection failed", "error", err)
	}
	logger.Info("worker stopped")
}
