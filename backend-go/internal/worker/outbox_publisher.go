package worker

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/outbox"
	"creatorinsight/backend-go/internal/platform/observability"
)

const (
	defaultOutboxBatchSize   = 50
	defaultOutboxInterval    = 500 * time.Millisecond
	defaultRecoveryInterval  = time.Minute
	defaultProcessingTimeout = 5 * time.Minute
	defaultOutboxMaxRetries  = 20
	defaultMetricsInterval   = 5 * time.Second
)

type outboxRepository interface {
	LockPending(ctx context.Context, limit int) ([]outbox.Event, error)
	MarkSent(ctx context.Context, id int64) error
	MarkRetry(ctx context.Context, id int64, retryCount int, nextRetryAt time.Time, lastError string) error
	MarkFailed(ctx context.Context, id int64, retryCount int, lastError string) error
	RecoverStaleProcessing(ctx context.Context, staleBefore time.Time) (int64, error)
	CountByStatus(ctx context.Context) (map[string]int64, error)
	OldestUnsentAge(ctx context.Context) (time.Duration, error)
}

type eventPublisher interface {
	PublishEvent(ctx context.Context, event outbox.Event) error
}

type OutboxPublisher struct {
	repo              outboxRepository
	publisher         eventPublisher
	logger            *slog.Logger
	batchSize         int
	maxRetries        int
	interval          time.Duration
	recoveryInterval  time.Duration
	processingTimeout time.Duration
	metricsInterval   time.Duration
	lastRecoveryAt    time.Time
	lastMetricsAt     time.Time
}

type OutboxPublisherDeps struct {
	Repository        outboxRepository
	Publisher         eventPublisher
	Logger            *slog.Logger
	BatchSize         int
	MaxRetries        int
	Interval          time.Duration
	RecoveryInterval  time.Duration
	ProcessingTimeout time.Duration
	MetricsInterval   time.Duration
}

func NewOutboxPublisher(deps OutboxPublisherDeps) *OutboxPublisher {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxPublisher{
		repo:              deps.Repository,
		publisher:         deps.Publisher,
		logger:            logger,
		batchSize:         positiveOr(deps.BatchSize, defaultOutboxBatchSize),
		maxRetries:        positiveOr(deps.MaxRetries, defaultOutboxMaxRetries),
		interval:          durationOr(deps.Interval, defaultOutboxInterval),
		recoveryInterval:  durationOr(deps.RecoveryInterval, defaultRecoveryInterval),
		processingTimeout: durationOr(deps.ProcessingTimeout, defaultProcessingTimeout),
		metricsInterval:   durationOr(deps.MetricsInterval, defaultMetricsInterval),
	}
}

func (p *OutboxPublisher) Start(ctx context.Context) {
	if p.repo == nil || p.publisher == nil {
		p.logger.Warn("outbox publisher disabled; missing dependencies")
		return
	}
	go p.loop(ctx)
}

func (p *OutboxPublisher) loop(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	p.logger.Info("outbox publisher started", "batch_size", p.batchSize, "interval", p.interval.String())
	defer p.logger.Info("outbox publisher stopped")

	for {
		if err := p.maintenance(ctx); err != nil {
			if isMissingOutboxSchema(err) {
				p.logger.Warn("outbox publisher waiting for migration", "error", err)
				if !waitOrDone(ctx, 5*time.Second) {
					return
				}
				continue
			}
			p.logger.Warn("outbox publisher maintenance failed", "error", err)
		}
		if err := p.processBatch(ctx); err != nil {
			if isMissingOutboxSchema(err) {
				p.logger.Warn("outbox publisher waiting for migration", "error", err)
				if !waitOrDone(ctx, 5*time.Second) {
					return
				}
				continue
			}
			p.logger.Warn("outbox publisher batch failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *OutboxPublisher) processBatch(ctx context.Context) error {
	events, err := p.repo.LockPending(ctx, p.batchSize)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := p.publisher.PublishEvent(ctx, event); err != nil {
			observability.IncOutboxPublish("error")
			nextRetry := event.RetryCount + 1
			if nextRetry >= p.maxRetries {
				if markErr := p.repo.MarkFailed(ctx, event.ID, nextRetry, err.Error()); markErr != nil {
					p.logger.Warn("mark outbox publish failed", "event_id", event.EventID, "error", markErr)
				}
				continue
			}
			if markErr := p.repo.MarkRetry(ctx, event.ID, nextRetry, time.Now().Add(retryDelay(nextRetry)), err.Error()); markErr != nil {
				p.logger.Warn("schedule outbox publish retry failed", "event_id", event.EventID, "error", markErr)
			}
			continue
		}
		observability.IncOutboxPublish("success")
		if err := p.repo.MarkSent(ctx, event.ID); err != nil {
			p.logger.Warn("mark published outbox event sent failed", "event_id", event.EventID, "error", err)
		}
	}
	return nil
}

func (p *OutboxPublisher) maintenance(ctx context.Context) error {
	now := time.Now()
	if p.lastRecoveryAt.IsZero() || now.Sub(p.lastRecoveryAt) >= p.recoveryInterval {
		p.lastRecoveryAt = now
		recovered, err := p.repo.RecoverStaleProcessing(ctx, now.Add(-p.processingTimeout))
		if err != nil {
			return err
		}
		if recovered > 0 {
			p.logger.Info("recovered stale outbox events", "count", recovered)
		}
	}
	if p.lastMetricsAt.IsZero() || now.Sub(p.lastMetricsAt) >= p.metricsInterval {
		p.lastMetricsAt = now
		counts, err := p.repo.CountByStatus(ctx)
		if err != nil {
			return err
		}
		observability.SetOutboxStatus(counts)
		oldestAge, err := p.repo.OldestUnsentAge(ctx)
		if err != nil {
			return err
		}
		observability.SetOutboxOldestUnsentAge(oldestAge)
	}
	return nil
}

func isMissingOutboxSchema(err error) bool {
	return err != nil && strings.Contains(err.Error(), `relation "outbox_events" does not exist`)
}

func retryDelay(retryCount int) time.Duration {
	if retryCount <= 0 {
		return time.Second
	}
	delay := time.Duration(1<<min(retryCount, 6)) * time.Second
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func positiveOr(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func durationOr(value time.Duration, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func waitOrDone(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
