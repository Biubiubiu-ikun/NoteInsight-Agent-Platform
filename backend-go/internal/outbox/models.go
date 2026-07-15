package outbox

import (
	"encoding/json"
	"time"
)

type EventInput struct {
	AggregateType string
	AggregateID   int64
	EventType     string
	Payload       any
	SchemaVersion int
	Producer      string
}

type Event struct {
	ID            int64           `db:"id"`
	EventID       string          `db:"event_id"`
	AggregateType string          `db:"aggregate_type"`
	AggregateID   int64           `db:"aggregate_id"`
	EventType     string          `db:"event_type"`
	Payload       json.RawMessage `db:"payload"`
	SchemaVersion int             `db:"schema_version"`
	Producer      string          `db:"producer"`
	CorrelationID string          `db:"correlation_id"`
	TraceID       string          `db:"trace_id"`
	Status        string          `db:"status"`
	RetryCount    int             `db:"retry_count"`
	NextRetryAt   time.Time       `db:"next_retry_at"`
	CreatedAt     time.Time       `db:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at"`
}
