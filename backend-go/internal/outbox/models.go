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
}

type Event struct {
	ID            int64           `db:"id"`
	EventID       string          `db:"event_id"`
	AggregateType string          `db:"aggregate_type"`
	AggregateID   int64           `db:"aggregate_id"`
	EventType     string          `db:"event_type"`
	Payload       json.RawMessage `db:"payload"`
	Status        string          `db:"status"`
	RetryCount    int             `db:"retry_count"`
	NextRetryAt   time.Time       `db:"next_retry_at"`
	CreatedAt     time.Time       `db:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at"`
}
