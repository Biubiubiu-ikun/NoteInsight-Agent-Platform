package tracing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTraceContextRoundTripAndErrorStatus(t *testing.T) {
	originalProvider := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(exporter),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(originalProvider)
		otel.SetTextMapPropagator(originalPropagator)
	})

	parentCtx, parent := Tracer().Start(context.Background(), "request")
	traceParent, traceState := InjectMap(parentCtx)
	if traceParent == "" {
		t.Fatal("traceparent was not injected")
	}
	extracted := ExtractMap(context.Background(), traceParent, traceState)
	childCtx, child := Tracer().Start(extracted, "worker")
	RecordError(child, errors.New("processing failed"))
	child.End()
	parent.End()

	if TraceID(parentCtx) == "" || TraceID(childCtx) != TraceID(parentCtx) {
		t.Fatalf("trace continuity failed: parent=%q child=%q", TraceID(parentCtx), TraceID(childCtx))
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 || spans[0].Status.Code != codes.Error {
		t.Fatalf("unexpected exported spans: %+v", spans)
	}
}

func TestExtractMapIgnoresInvalidTraceParent(t *testing.T) {
	ctx := ExtractMap(context.Background(), "invalid", "")
	if got := TraceID(ctx); got != "" {
		t.Fatalf("TraceID() = %q, want empty", got)
	}
}

func TestOTLPExporterSmoke(t *testing.T) {
	endpoint := os.Getenv("OTEL_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("OTEL_TEST_ENDPOINT is not configured")
	}
	originalProvider := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	shutdown, err := Initialize(context.Background(), config.TelemetryConfig{
		Enabled:        true,
		Endpoint:       endpoint,
		Insecure:       true,
		SampleRatio:    1,
		ServiceVersion: "test",
		ExportTimeout:  5 * time.Second,
	}, "otel-integration-probe", "test", logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		otel.SetTracerProvider(originalProvider)
		otel.SetTextMapPropagator(originalPropagator)
	})

	_, span := Tracer().Start(context.Background(), "otlp exporter smoke")
	span.End()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown and flush OTLP exporter: %v", err)
	}
}
