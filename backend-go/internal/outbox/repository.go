package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func EnqueueTx(ctx context.Context, tx *sqlx.Tx, input EventInput) error {
	if strings.TrimSpace(input.AggregateType) == "" {
		return fmt.Errorf("outbox aggregate_type is required")
	}
	if input.AggregateID <= 0 {
		return fmt.Errorf("outbox aggregate_id must be positive")
	}
	if strings.TrimSpace(input.EventType) == "" {
		return fmt.Errorf("outbox event_type is required")
	}

	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO outbox_events (event_id, aggregate_type, aggregate_id, event_type, payload, status, next_retry_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, 'pending', now(), now(), now())`,
		NewEventID(input.EventType),
		input.AggregateType,
		input.AggregateID,
		input.EventType,
		string(payload),
	)
	return err
}

func (r *Repository) LockPending(ctx context.Context, limit int) ([]Event, error) {
	defer observability.ObserveDB("outbox_lock_pending", time.Now())
	if limit <= 0 {
		limit = 50
	}

	var events []Event
	err := r.db.SelectContext(ctx, &events, `
WITH picked AS (
    SELECT id
    FROM outbox_events
    WHERE status IN ('pending', 'retry')
      AND next_retry_at <= now()
    ORDER BY created_at ASC, id ASC
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events e
SET status = 'processing',
    updated_at = now()
FROM picked
WHERE e.id = picked.id
RETURNING e.id, e.event_id, e.aggregate_type, e.aggregate_id, e.event_type,
          e.payload, e.status, e.retry_count, e.next_retry_at, e.created_at, e.updated_at`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	observability.IncOutboxLocked(len(events))
	return events, nil
}

func (r *Repository) MarkSent(ctx context.Context, id int64) error {
	defer observability.ObserveDB("outbox_mark_sent", time.Now())
	_, err := r.db.ExecContext(ctx, `
UPDATE outbox_events
SET status = 'sent',
    sent_at = now(),
    updated_at = now(),
    last_error = NULL
WHERE id = $1`,
		id,
	)
	if err == nil {
		observability.IncOutboxProcessed()
	}
	return err
}

func (r *Repository) MarkRetry(ctx context.Context, id int64, retryCount int, nextRetryAt time.Time, lastError string) error {
	defer observability.ObserveDB("outbox_mark_retry", time.Now())
	_, err := r.db.ExecContext(ctx, `
UPDATE outbox_events
SET status = 'retry',
    retry_count = $2,
    next_retry_at = $3,
    last_error = $4,
    updated_at = now()
WHERE id = $1`,
		id,
		retryCount,
		nextRetryAt,
		truncateError(lastError),
	)
	if err == nil {
		observability.IncOutboxRetried()
	}
	return err
}

func (r *Repository) MarkFailed(ctx context.Context, id int64, retryCount int, lastError string) error {
	defer observability.ObserveDB("outbox_mark_failed", time.Now())
	_, err := r.db.ExecContext(ctx, `
UPDATE outbox_events
SET status = 'failed',
    retry_count = $2,
    last_error = $3,
    updated_at = now()
WHERE id = $1`,
		id,
		retryCount,
		truncateError(lastError),
	)
	if err == nil {
		observability.IncOutboxFailed()
	}
	return err
}

func (r *Repository) RecoverStaleProcessing(ctx context.Context, staleBefore time.Time) (int64, error) {
	defer observability.ObserveDB("outbox_recover_stale", time.Now())
	result, err := r.db.ExecContext(ctx, `
UPDATE outbox_events
SET status = 'retry',
    next_retry_at = now(),
    last_error = CASE
        WHEN COALESCE(last_error, '') = '' THEN 'processing lease expired'
        ELSE last_error
    END,
    updated_at = now()
WHERE status = 'processing'
  AND updated_at < $1`,
		staleBefore,
	)
	if err != nil {
		return 0, err
	}
	recovered, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read recovered outbox rows: %w", err)
	}
	observability.IncOutboxStaleRecovered(recovered)
	return recovered, nil
}

func (r *Repository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	defer observability.ObserveDB("outbox_count_by_status", time.Now())
	type statusCount struct {
		Status string `db:"status"`
		Count  int64  `db:"count"`
	}
	var rows []statusCount
	if err := r.db.SelectContext(ctx, &rows, `
SELECT status, COUNT(*) AS count
FROM outbox_events
GROUP BY status`); err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	return counts, nil
}

func (r *Repository) OldestUnsentAge(ctx context.Context) (time.Duration, error) {
	defer observability.ObserveDB("outbox_oldest_unsent_age", time.Now())
	var seconds float64
	if err := r.db.QueryRowContext(ctx, `
SELECT COALESCE(EXTRACT(EPOCH FROM now() - MIN(created_at)), 0)
FROM outbox_events
WHERE status IN ('pending', 'processing', 'retry')`).Scan(&seconds); err != nil {
		return 0, err
	}
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func NewEventID(eventType string) string {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("evt_%s_%d", normalizeEventType(eventType), time.Now().UnixNano())
	}
	return fmt.Sprintf("evt_%s_%d_%s", normalizeEventType(eventType), time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
}

func normalizeEventType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, ".", "_")
	value = strings.ReplaceAll(value, "-", "_")
	if value == "" {
		return "unknown"
	}
	return value
}

func truncateError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 1000 {
		return value
	}
	return value[:1000]
}
