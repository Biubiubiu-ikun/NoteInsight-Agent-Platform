package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests.",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)

	CacheHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_hit_total",
			Help: "Total cache hits.",
		},
		[]string{"cache"},
	)

	CacheMissesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_miss_total",
			Help: "Total cache misses.",
		},
		[]string{"cache"},
	)

	DBQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database query duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	HotRankingUpdatesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hot_ranking_update_total",
			Help: "Total hot ranking updates.",
		},
		[]string{"ranking"},
	)

	OutboxEventsLockedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "outbox_events_locked_total",
			Help: "Total outbox events locked by local workers.",
		},
	)

	OutboxEventsProcessedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "outbox_events_processed_total",
			Help: "Total outbox events processed successfully.",
		},
	)

	OutboxEventsRetriedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "outbox_events_retried_total",
			Help: "Total outbox events scheduled for retry.",
		},
	)

	OutboxEventsFailedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "outbox_events_failed_total",
			Help: "Total outbox events marked failed.",
		},
	)

	BehaviorEventsRecordedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "behavior_events_recorded_total",
			Help: "Total behavior events recorded by event type.",
		},
		[]string{"event_type"},
	)

	RateLimitDecisionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limit_decisions_total",
			Help: "Total rate limit decisions by policy and result.",
		},
		[]string{"policy", "result"},
	)

	OutboxStaleRecoveredTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "outbox_stale_recovered_total",
			Help: "Total stale processing outbox events returned to retry.",
		},
	)

	ReconcileRunsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "reconcile_runs_total",
			Help: "Total derived-data reconciliation runs by result.",
		},
		[]string{"result"},
	)

	ReconcileDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "reconcile_duration_seconds",
			Help:    "Derived-data reconciliation duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)

	ReconcileRowsRepairedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "reconcile_rows_repaired_total",
			Help: "Total database rows repaired by reconciliation.",
		},
		[]string{"entity"},
	)

	OutboxPublishTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "outbox_publish_total",
			Help: "Total PostgreSQL Outbox publish attempts by result.",
		},
		[]string{"result"},
	)

	OutboxStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "outbox_events",
			Help: "Current PostgreSQL Outbox event count by status.",
		},
		[]string{"status"},
	)

	OutboxOldestUnsentAgeSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "outbox_oldest_unsent_age_seconds",
			Help: "Age in seconds of the oldest pending, processing, or retry Outbox event.",
		},
	)

	JetStreamConsumedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jetstream_messages_consumed_total",
			Help: "Total JetStream message processing outcomes.",
		},
		[]string{"event_type", "result"},
	)

	JetStreamRedeliveriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jetstream_redeliveries_total",
			Help: "Total JetStream message redeliveries observed by event type.",
		},
		[]string{"event_type"},
	)

	JetStreamDeadLettersTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jetstream_dead_letters_total",
			Help: "Total messages moved to the JetStream dead-letter stream.",
		},
		[]string{"event_type"},
	)

	JetStreamConsumerPending = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "jetstream_consumer_pending_messages",
			Help: "Current undelivered message count for the worker consumer.",
		},
	)

	JetStreamConsumerAckPending = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "jetstream_consumer_ack_pending_messages",
			Help: "Current delivered but unacknowledged message count for the worker consumer.",
		},
	)

	JetStreamConsumerRedelivered = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "jetstream_consumer_redelivered_messages",
			Help: "Current redelivered and unacknowledged message count for the worker consumer.",
		},
	)

	NATSConnected = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "nats_connected",
			Help: "Whether the worker currently has a connected NATS client.",
		},
	)

	DomainEventLagSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "domain_event_lag_seconds",
			Help:    "Time from domain event creation to durable worker application.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 300, 900},
		},
		[]string{"event_type"},
	)

	DerivedRefreshTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "derived_data_refresh_total",
			Help: "Total Redis cache and ranking refresh outcomes after durable event application.",
		},
		[]string{"event_type", "result"},
	)
)

func init() {
	prometheus.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		CacheHitsTotal,
		CacheMissesTotal,
		DBQueryDuration,
		HotRankingUpdatesTotal,
		OutboxEventsLockedTotal,
		OutboxEventsProcessedTotal,
		OutboxEventsRetriedTotal,
		OutboxEventsFailedTotal,
		BehaviorEventsRecordedTotal,
		RateLimitDecisionsTotal,
		OutboxStaleRecoveredTotal,
		ReconcileRunsTotal,
		ReconcileDuration,
		ReconcileRowsRepairedTotal,
		OutboxPublishTotal,
		OutboxStatus,
		OutboxOldestUnsentAgeSeconds,
		JetStreamConsumedTotal,
		JetStreamRedeliveriesTotal,
		JetStreamDeadLettersTotal,
		JetStreamConsumerPending,
		JetStreamConsumerAckPending,
		JetStreamConsumerRedelivered,
		NATSConnected,
		DomainEventLagSeconds,
		DerivedRefreshTotal,
	)
}

func ObserveDB(operation string, startedAt time.Time) {
	DBQueryDuration.WithLabelValues(operation).Observe(time.Since(startedAt).Seconds())
}

func IncCacheHit(cache string) {
	CacheHitsTotal.WithLabelValues(cache).Inc()
}

func IncCacheMiss(cache string) {
	CacheMissesTotal.WithLabelValues(cache).Inc()
}

func IncHotRankingUpdate(ranking string) {
	HotRankingUpdatesTotal.WithLabelValues(ranking).Inc()
}

func IncOutboxLocked(count int) {
	if count <= 0 {
		return
	}
	OutboxEventsLockedTotal.Add(float64(count))
}

func IncOutboxProcessed() {
	OutboxEventsProcessedTotal.Inc()
}

func IncOutboxRetried() {
	OutboxEventsRetriedTotal.Inc()
}

func IncOutboxFailed() {
	OutboxEventsFailedTotal.Inc()
}

func IncBehaviorRecorded(eventType string) {
	BehaviorEventsRecordedTotal.WithLabelValues(eventType).Inc()
}

func IncRateLimitDecision(policy string, result string) {
	RateLimitDecisionsTotal.WithLabelValues(policy, result).Inc()
}

func IncOutboxStaleRecovered(count int64) {
	if count > 0 {
		OutboxStaleRecoveredTotal.Add(float64(count))
	}
}

func ObserveReconcile(startedAt time.Time, err error, noteRows int64, commentRows int64) {
	result := "success"
	if err != nil {
		result = "error"
	}
	ReconcileRunsTotal.WithLabelValues(result).Inc()
	ReconcileDuration.Observe(time.Since(startedAt).Seconds())
	if noteRows > 0 {
		ReconcileRowsRepairedTotal.WithLabelValues("notes").Add(float64(noteRows))
	}
	if commentRows > 0 {
		ReconcileRowsRepairedTotal.WithLabelValues("comments").Add(float64(commentRows))
	}
}

func IncOutboxPublish(result string) {
	OutboxPublishTotal.WithLabelValues(result).Inc()
}

func SetOutboxStatus(counts map[string]int64) {
	for _, status := range []string{"pending", "processing", "retry", "sent", "failed"} {
		OutboxStatus.WithLabelValues(status).Set(float64(counts[status]))
	}
}

func SetOutboxOldestUnsentAge(age time.Duration) {
	seconds := age.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	OutboxOldestUnsentAgeSeconds.Set(seconds)
}

func IncJetStreamConsumed(eventType string, result string) {
	JetStreamConsumedTotal.WithLabelValues(eventType, result).Inc()
}

func IncJetStreamRedelivery(eventType string) {
	JetStreamRedeliveriesTotal.WithLabelValues(eventType).Inc()
}

func IncJetStreamDeadLetter(eventType string) {
	JetStreamDeadLettersTotal.WithLabelValues(eventType).Inc()
}

func SetJetStreamConsumerState(pending uint64, ackPending int, redelivered int) {
	JetStreamConsumerPending.Set(float64(pending))
	JetStreamConsumerAckPending.Set(float64(ackPending))
	JetStreamConsumerRedelivered.Set(float64(redelivered))
}

func SetNATSConnected(connected bool) {
	value := 0.0
	if connected {
		value = 1
	}
	NATSConnected.Set(value)
}

func ObserveDomainEventLag(eventType string, occurredAt time.Time) {
	if occurredAt.IsZero() {
		return
	}
	lag := time.Since(occurredAt).Seconds()
	if lag < 0 {
		lag = 0
	}
	DomainEventLagSeconds.WithLabelValues(eventType).Observe(lag)
}

func IncDerivedRefresh(eventType string, result string) {
	DerivedRefreshTotal.WithLabelValues(eventType, result).Inc()
}
