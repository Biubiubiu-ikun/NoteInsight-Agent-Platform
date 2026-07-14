package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/outbox"
	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	defaultStreamMaxBytes = 1 << 30
	defaultDLQMaxBytes    = 256 << 20
	defaultMaxMessageSize = 1 << 20
)

var invalidSubjectToken = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type EventEnvelope struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   int64           `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

type DeadLetterEnvelope struct {
	OriginalSubject string          `json:"original_subject"`
	EventID         string          `json:"event_id"`
	EventType       string          `json:"event_type"`
	Deliveries      uint64          `json:"deliveries"`
	Failure         string          `json:"failure"`
	OriginalMessage json.RawMessage `json:"original_message"`
	FailedAt        time.Time       `json:"failed_at"`
}

type Broker struct {
	conn     *nats.Conn
	js       jetstream.JetStream
	stream   jetstream.Stream
	consumer jetstream.Consumer
	cfg      config.NATSConfig
	logger   *slog.Logger
}

func NewBroker(ctx context.Context, cfg config.NATSConfig, logger *slog.Logger) (*Broker, error) {
	if logger == nil {
		logger = slog.Default()
	}
	conn, err := nats.Connect(
		cfg.URL,
		nats.Name(cfg.ConnectionName),
		nats.Timeout(cfg.ConnectTimeout),
		nats.DrainTimeout(cfg.DrainTimeout),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			observability.SetNATSConnected(false)
			logger.Warn("NATS disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			observability.SetNATSConnected(true)
			logger.Info("NATS reconnected", "url", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			observability.SetNATSConnected(false)
			logger.Info("NATS connection closed")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect NATS: %w", err)
	}

	js, err := jetstream.New(conn, jetstream.WithDefaultTimeout(cfg.RequestTimeout))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("create JetStream client: %w", err)
	}
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        cfg.Stream,
		Description: "NoteInsight domain events published from PostgreSQL Outbox",
		Subjects:    []string{cfg.SubjectPrefix + ".>"},
		Retention:   jetstream.LimitsPolicy,
		Discard:     jetstream.DiscardOld,
		MaxBytes:    defaultStreamMaxBytes,
		MaxAge:      cfg.StreamMaxAge,
		MaxMsgSize:  defaultMaxMessageSize,
		Storage:     jetstream.FileStorage,
		Replicas:    1,
		Duplicates:  cfg.DuplicateWindow,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ensure JetStream event stream: %w", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        cfg.DLQStream,
		Description: "NoteInsight events that exceeded worker delivery attempts",
		Subjects:    []string{cfg.DLQSubjectPrefix + ".>"},
		Retention:   jetstream.LimitsPolicy,
		Discard:     jetstream.DiscardOld,
		MaxBytes:    defaultDLQMaxBytes,
		MaxAge:      cfg.DLQMaxAge,
		MaxMsgSize:  defaultMaxMessageSize,
		Storage:     jetstream.FileStorage,
		Replicas:    1,
		Duplicates:  cfg.DuplicateWindow,
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ensure JetStream dead-letter stream: %w", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:            cfg.Consumer,
		Durable:         cfg.Consumer,
		Description:     "NoteInsight durable domain event worker",
		DeliverPolicy:   jetstream.DeliverAllPolicy,
		AckPolicy:       jetstream.AckExplicitPolicy,
		AckWait:         cfg.AckWait,
		MaxDeliver:      -1,
		FilterSubject:   cfg.SubjectPrefix + ".>",
		ReplayPolicy:    jetstream.ReplayInstantPolicy,
		MaxAckPending:   cfg.MaxAckPending,
		MaxRequestBatch: cfg.MaxAckPending,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ensure JetStream consumer: %w", err)
	}

	observability.SetNATSConnected(true)
	return &Broker{conn: conn, js: js, stream: stream, consumer: consumer, cfg: cfg, logger: logger}, nil
}

func (b *Broker) PublishEvent(ctx context.Context, event outbox.Event) error {
	envelope := EventEnvelope{
		EventID:       event.EventID,
		EventType:     event.EventType,
		AggregateType: event.AggregateType,
		AggregateID:   event.AggregateID,
		Payload:       event.Payload,
		OccurredAt:    event.CreatedAt,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal event envelope: %w", err)
	}
	_, err = b.js.Publish(
		ctx,
		b.eventSubject(event.EventType),
		payload,
		jetstream.WithMsgID(event.EventID),
		jetstream.WithExpectStream(b.cfg.Stream),
	)
	if err != nil {
		return fmt.Errorf("publish event %s: %w", event.EventID, err)
	}
	return nil
}

func (b *Broker) PublishDeadLetter(ctx context.Context, originalSubject string, raw []byte, eventID string, eventType string, deliveries uint64, failure error) error {
	if failure == nil {
		failure = errors.New("unknown processing failure")
	}
	deadLetter := DeadLetterEnvelope{
		OriginalSubject: originalSubject,
		EventID:         eventID,
		EventType:       eventType,
		Deliveries:      deliveries,
		Failure:         truncate(failure.Error(), 1000),
		OriginalMessage: append(json.RawMessage(nil), raw...),
		FailedAt:        time.Now().UTC(),
	}
	payload, err := json.Marshal(deadLetter)
	if err != nil {
		return fmt.Errorf("marshal dead-letter event: %w", err)
	}
	messageID := "dlq_" + eventID
	if strings.TrimSpace(eventID) == "" {
		messageID = fmt.Sprintf("dlq_%d", time.Now().UnixNano())
	}
	_, err = b.js.Publish(
		ctx,
		b.deadLetterSubject(eventType),
		payload,
		jetstream.WithMsgID(messageID),
		jetstream.WithExpectStream(b.cfg.DLQStream),
	)
	if err != nil {
		return fmt.Errorf("publish dead-letter event: %w", err)
	}
	return nil
}

func (b *Broker) Consumer() jetstream.Consumer {
	return b.consumer
}

func (b *Broker) Check(ctx context.Context) error {
	if b == nil || b.conn == nil || b.conn.Status() != nats.CONNECTED {
		return errors.New("NATS is not connected")
	}
	_, err := b.js.AccountInfo(ctx)
	return err
}

func (b *Broker) Connected() bool {
	return b != nil && b.conn != nil && b.conn.Status() == nats.CONNECTED
}

func (b *Broker) Close() error {
	if b == nil || b.conn == nil || b.conn.IsClosed() {
		return nil
	}
	observability.SetNATSConnected(false)
	return b.conn.Drain()
}

func (b *Broker) eventSubject(eventType string) string {
	return b.cfg.SubjectPrefix + "." + subjectSuffix(eventType)
}

func (b *Broker) deadLetterSubject(eventType string) string {
	return b.cfg.DLQSubjectPrefix + "." + subjectSuffix(eventType)
}

func subjectSuffix(eventType string) string {
	parts := strings.Split(strings.Trim(strings.ToLower(eventType), "."), ".")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = invalidSubjectToken.ReplaceAllString(strings.TrimSpace(part), "_")
		part = strings.Trim(part, "_")
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return "unknown"
	}
	return strings.Join(cleaned, ".")
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
