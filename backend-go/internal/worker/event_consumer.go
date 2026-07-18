package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/platform/messaging"
	"creatorinsight/backend-go/internal/platform/observability"
	platformtracing "creatorinsight/backend-go/internal/platform/tracing"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type eventProcessor interface {
	Process(ctx context.Context, envelope messaging.EventEnvelope) (alreadyProcessed bool, err error)
}

type deadLetterPublisher interface {
	PublishDeadLetter(ctx context.Context, originalSubject string, raw []byte, eventID string, eventType string, deliveries uint64, failure error) error
}

type EventConsumer struct {
	consumer         jetstream.Consumer
	processor        eventProcessor
	deadLetters      deadLetterPublisher
	logger           *slog.Logger
	batchSize        int
	maxDeliver       int
	nakDelay         time.Duration
	operationTimeout time.Duration
	metricsInterval  time.Duration
	consumeContext   jetstream.ConsumeContext
}

type EventConsumerDeps struct {
	Consumer         jetstream.Consumer
	Processor        eventProcessor
	DeadLetters      deadLetterPublisher
	Logger           *slog.Logger
	BatchSize        int
	MaxDeliver       int
	NakDelay         time.Duration
	OperationTimeout time.Duration
	MetricsInterval  time.Duration
}

func NewEventConsumer(deps EventConsumerDeps) *EventConsumer {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &EventConsumer{
		consumer:         deps.Consumer,
		processor:        deps.Processor,
		deadLetters:      deps.DeadLetters,
		logger:           logger,
		batchSize:        positiveOr(deps.BatchSize, defaultOutboxBatchSize),
		maxDeliver:       positiveOr(deps.MaxDeliver, 5),
		nakDelay:         durationOr(deps.NakDelay, 2*time.Second),
		operationTimeout: durationOr(deps.OperationTimeout, 30*time.Second),
		metricsInterval:  durationOr(deps.MetricsInterval, defaultMetricsInterval),
	}
}

func (c *EventConsumer) Start(ctx context.Context) error {
	if c.consumer == nil || c.processor == nil || c.deadLetters == nil {
		return fmt.Errorf("event consumer dependencies are required")
	}
	consumeContext, err := c.consumer.Consume(
		c.handleMessage,
		jetstream.PullMaxMessages(c.batchSize),
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, consumeErr error) {
			c.logger.Warn("JetStream consume error", "error", consumeErr)
		}),
	)
	if err != nil {
		return fmt.Errorf("start JetStream consumer: %w", err)
	}
	c.consumeContext = consumeContext
	c.logger.Info("JetStream event consumer started", "batch_size", c.batchSize, "max_deliver", c.maxDeliver)
	go func() {
		<-ctx.Done()
		consumeContext.Drain()
	}()
	go c.monitor(ctx)
	return nil
}

func (c *EventConsumer) Wait() {
	if c.consumeContext != nil {
		<-c.consumeContext.Closed()
	}
}

func (c *EventConsumer) handleMessage(msg jetstream.Msg) {
	parentCtx := otel.GetTextMapPropagator().Extract(context.Background(), messaging.NATSHeaderCarrier(msg.Headers()))
	ctx, span := platformtracing.Tracer().Start(parentCtx, "nats consume",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", msg.Subject()),
			attribute.String("messaging.operation.type", "process"),
		),
	)
	defer span.End()
	metadata, metadataErr := msg.Metadata()
	deliveries := uint64(1)
	if metadataErr == nil {
		deliveries = metadata.NumDelivered
	}
	span.SetAttributes(attribute.Int64("messaging.message.delivery_count", int64(deliveries)))

	var envelope messaging.EventEnvelope
	decodeErr := json.Unmarshal(msg.Data(), &envelope)
	eventType := strings.TrimSpace(envelope.EventType)
	if eventType == "" {
		eventType = "unknown"
	}
	span.SetAttributes(attribute.String("noteinsight.event.type", eventType))
	if deliveries > 1 {
		observability.IncJetStreamRedelivery(eventType)
	}
	if decodeErr != nil {
		processingErr := fmt.Errorf("decode event envelope: %w", decodeErr)
		platformtracing.RecordError(span, processingErr)
		c.handleFailure(ctx, msg, envelope, eventType, deliveries, processingErr)
		return
	}

	operationCtx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	alreadyProcessed, err := c.processor.Process(operationCtx, envelope)
	cancel()
	if err != nil {
		platformtracing.RecordError(span, err)
		c.handleFailure(ctx, msg, envelope, eventType, deliveries, err)
		return
	}

	ackCtx, ackCancel := context.WithTimeout(ctx, c.operationTimeout)
	err = msg.DoubleAck(ackCtx)
	ackCancel()
	if err != nil {
		platformtracing.RecordError(span, err)
		observability.IncJetStreamConsumed(eventType, "ack_error")
		c.logger.Warn("JetStream double ack failed", "event_id", envelope.EventID, "error", err)
		return
	}
	result := "processed"
	if alreadyProcessed {
		result = "duplicate"
	}
	observability.IncJetStreamConsumed(eventType, result)
}

func (c *EventConsumer) handleFailure(parentCtx context.Context, msg jetstream.Msg, envelope messaging.EventEnvelope, eventType string, deliveries uint64, processingErr error) {
	observability.IncJetStreamConsumed(eventType, "error")
	if deliveries < uint64(c.maxDeliver) {
		if err := msg.NakWithDelay(c.nakDelay); err != nil {
			c.logger.Warn("JetStream delayed nack failed", "event_id", envelope.EventID, "error", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(parentCtx, c.operationTimeout)
	err := c.deadLetters.PublishDeadLetter(ctx, msg.Subject(), msg.Data(), envelope.EventID, eventType, deliveries, processingErr)
	cancel()
	if err != nil {
		c.logger.Warn("publish dead-letter event failed", "event_id", envelope.EventID, "error", err)
		if nakErr := msg.NakWithDelay(c.nakDelay); nakErr != nil {
			c.logger.Warn("JetStream nack after DLQ failure failed", "event_id", envelope.EventID, "error", nakErr)
		}
		return
	}
	if err := msg.TermWithReason(truncateReason(processingErr.Error())); err != nil {
		c.logger.Warn("terminate dead-lettered JetStream message failed", "event_id", envelope.EventID, "error", err)
		return
	}
	observability.IncJetStreamDeadLetter(eventType)
	observability.IncJetStreamConsumed(eventType, "dead_letter")
	c.logger.Warn("event moved to dead-letter stream", "event_id", envelope.EventID, "event_type", eventType, "deliveries", deliveries, "error", processingErr)
}

func (c *EventConsumer) monitor(ctx context.Context) {
	ticker := time.NewTicker(c.metricsInterval)
	defer ticker.Stop()
	for {
		infoCtx, cancel := context.WithTimeout(ctx, c.operationTimeout)
		info, err := c.consumer.Info(infoCtx)
		cancel()
		if err == nil {
			observability.SetJetStreamConsumerState(info.NumPending, info.NumAckPending, info.NumRedelivered)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func truncateReason(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 200 {
		return value
	}
	return value[:200]
}
