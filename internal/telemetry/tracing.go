package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

func Init(logger *slog.Logger, serviceName, endpoint string, enabled bool) (func(), error) {
	if !enabled {
		return func() {}, nil
	}
	if endpoint == "" {
		endpoint = "http://localhost:4318"
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || (parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") || parsedEndpoint.Host == "" {
		return func() {}, fmt.Errorf("invalid OTLP HTTP endpoint %q: expected an http(s) URL with a host", endpoint)
	}
	exporter, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return func() {}, fmt.Errorf("create OTLP HTTP trace exporter: %w", err)
	}
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("0.1.0"),
		),
		resource.WithFromEnv(),
	)
	if err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := exporter.Shutdown(shutdownCtx); shutdownErr != nil {
			return func() {}, errors.Join(
				fmt.Errorf("create OpenTelemetry resource: %w", err),
				fmt.Errorf("shutdown OTLP HTTP trace exporter: %w", shutdownErr),
			)
		}
		return func() {}, fmt.Errorf("create OpenTelemetry resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(2048), sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			logger.Warn("otel shutdown failed", "error", err)
		}
	}, nil
}

func TraceIDFromContext(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func StartJobSpan(ctx context.Context, operation, jobID, videoID string) (context.Context, trace.Span) {
	return otel.Tracer("github.com/yourusername/streamforge/internal/telemetry").Start(
		ctx,
		operation,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.operation", operation),
			attribute.String("streamforge.job.id", jobID),
			attribute.String("streamforge.video.id", videoID),
		),
	)
}
