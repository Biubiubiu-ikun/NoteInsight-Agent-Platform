package tracing

import (
	"context"
	"log/slog"
	"strings"

	"creatorinsight/backend-go/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "creatorinsight/backend-go"

type Shutdown func(context.Context) error

func Initialize(ctx context.Context, cfg config.TelemetryConfig, serviceName, environment string, logger *slog.Logger) (Shutdown, error) {
	if logger == nil {
		logger = slog.Default()
	}
	propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	otel.SetTextMapPropagator(propagator)
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	exporterOptions := []otlptracegrpc.Option{
		otlptracegrpc.WithTimeout(cfg.ExportTimeout),
	}
	if strings.Contains(cfg.Endpoint, "://") {
		exporterOptions = append(exporterOptions, otlptracegrpc.WithEndpointURL(cfg.Endpoint))
	} else {
		exporterOptions = append(exporterOptions, otlptracegrpc.WithEndpoint(cfg.Endpoint))
	}
	if cfg.Insecure {
		exporterOptions = append(exporterOptions, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, exporterOptions...)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", cfg.ServiceVersion),
			attribute.String("deployment.environment.name", environment),
		),
	)
	if err != nil {
		_ = exporter.Shutdown(context.Background())
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	otel.SetTracerProvider(provider)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("OpenTelemetry export failed", "error", err)
	}))
	return provider.Shutdown, nil
}

func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func SpanID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.SpanID().String()
}

func InjectMap(ctx context.Context) (traceParent, traceState string) {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier.Get("traceparent"), carrier.Get("tracestate")
}

func ExtractMap(ctx context.Context, traceParent, traceState string) context.Context {
	carrier := propagation.MapCarrier{}
	if traceParent != "" {
		carrier.Set("traceparent", traceParent)
	}
	if traceState != "" {
		carrier.Set("tracestate", traceState)
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
