package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/platform/messaging"
	platformtracing "creatorinsight/backend-go/internal/platform/tracing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type fakeProcessor struct {
	already bool
	err     error
	calls   int
	traceID string
}

func (f *fakeProcessor) Process(ctx context.Context, _ messaging.EventEnvelope) (bool, error) {
	f.calls++
	f.traceID = platformtracing.TraceID(ctx)
	return f.already, f.err
}

type fakeDeadLetters struct {
	calls int
}

func (f *fakeDeadLetters) PublishDeadLetter(context.Context, string, []byte, string, string, uint64, error) error {
	f.calls++
	return nil
}

type fakeJetStreamMsg struct {
	data       []byte
	deliveries uint64
	doubleAck  int
	nak        int
	terminated int
	headers    nats.Header
}

func (f *fakeJetStreamMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{NumDelivered: f.deliveries}, nil
}
func (f *fakeJetStreamMsg) Data() []byte                     { return f.data }
func (f *fakeJetStreamMsg) Headers() nats.Header             { return f.headers }
func (f *fakeJetStreamMsg) Subject() string                  { return "noteinsight.events.note.liked" }
func (f *fakeJetStreamMsg) Reply() string                    { return "" }
func (f *fakeJetStreamMsg) Ack() error                       { return nil }
func (f *fakeJetStreamMsg) DoubleAck(context.Context) error  { f.doubleAck++; return nil }
func (f *fakeJetStreamMsg) Nak() error                       { f.nak++; return nil }
func (f *fakeJetStreamMsg) NakWithDelay(time.Duration) error { f.nak++; return nil }
func (f *fakeJetStreamMsg) InProgress() error                { return nil }
func (f *fakeJetStreamMsg) Term() error                      { f.terminated++; return nil }
func (f *fakeJetStreamMsg) TermWithReason(string) error      { f.terminated++; return nil }

func eventMessage(t *testing.T, deliveries uint64) *fakeJetStreamMsg {
	t.Helper()
	payload, err := json.Marshal(messaging.EventEnvelope{
		EventID: "evt_1", EventType: "note.liked", Payload: json.RawMessage(`{"user_id":42,"note_id":9}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeJetStreamMsg{data: payload, deliveries: deliveries}
}

func TestEventConsumerDoubleAcksSuccess(t *testing.T) {
	processor := &fakeProcessor{}
	consumer := NewEventConsumer(EventConsumerDeps{
		Processor: processor, DeadLetters: &fakeDeadLetters{}, OperationTimeout: time.Second,
	})
	msg := eventMessage(t, 1)

	consumer.handleMessage(msg)
	if processor.calls != 1 || msg.doubleAck != 1 || msg.nak != 0 {
		t.Fatalf("calls=%d double_ack=%d nak=%d", processor.calls, msg.doubleAck, msg.nak)
	}
}

func TestEventConsumerNaksBeforeDeliveryLimit(t *testing.T) {
	consumer := NewEventConsumer(EventConsumerDeps{
		Processor: &fakeProcessor{err: errors.New("temporary")}, DeadLetters: &fakeDeadLetters{}, MaxDeliver: 3,
	})
	msg := eventMessage(t, 2)

	consumer.handleMessage(msg)
	if msg.nak != 1 || msg.terminated != 0 {
		t.Fatalf("nak=%d terminated=%d", msg.nak, msg.terminated)
	}
}

func TestEventConsumerMovesFailureToDLQ(t *testing.T) {
	deadLetters := &fakeDeadLetters{}
	consumer := NewEventConsumer(EventConsumerDeps{
		Processor: &fakeProcessor{err: errors.New("permanent")}, DeadLetters: deadLetters, MaxDeliver: 3,
	})
	msg := eventMessage(t, 3)

	consumer.handleMessage(msg)
	if deadLetters.calls != 1 || msg.terminated != 1 || msg.nak != 0 {
		t.Fatalf("dlq=%d terminated=%d nak=%d", deadLetters.calls, msg.terminated, msg.nak)
	}
}

func TestEventConsumerContinuesPublishedTrace(t *testing.T) {
	originalProvider := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(originalProvider)
		otel.SetTextMapPropagator(originalPropagator)
	})

	parentCtx, parentSpan := platformtracing.Tracer().Start(context.Background(), "publisher")
	headers := nats.Header{}
	otel.GetTextMapPropagator().Inject(parentCtx, propagation.HeaderCarrier(headers))
	processor := &fakeProcessor{}
	consumer := NewEventConsumer(EventConsumerDeps{
		Processor: processor, DeadLetters: &fakeDeadLetters{}, OperationTimeout: time.Second,
	})
	msg := eventMessage(t, 1)
	msg.headers = headers
	consumer.handleMessage(msg)
	parentSpan.End()

	if processor.traceID == "" || processor.traceID != platformtracing.TraceID(parentCtx) {
		t.Fatalf("consumer trace=%q, publisher trace=%q", processor.traceID, platformtracing.TraceID(parentCtx))
	}
}
