package telemetry

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTraceIDFromContext(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("parse trace ID: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	if err != nil {
		t.Fatalf("parse span ID: %v", err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	if got := TraceIDFromContext(ctx); got != traceID.String() {
		t.Fatalf("TraceIDFromContext() = %q, want %q", got, traceID.String())
	}
	if got := TraceIDFromContext(context.Background()); got != "" {
		t.Fatalf("TraceIDFromContext() without a span = %q, want empty", got)
	}
}

func TestInitDisabled(t *testing.T) {
	shutdown, err := Init(slog.New(slog.NewTextHandler(io.Discard, nil)), "streamforge-test", "", false)
	if err != nil {
		t.Fatalf("Init() with tracing disabled returned error: %v", err)
	}
	shutdown()
}

func TestInitRejectsInvalidEndpoint(t *testing.T) {
	_, err := Init(slog.New(slog.NewTextHandler(io.Discard, nil)), "streamforge-test", "collector:4318", true)
	if err == nil {
		t.Fatal("Init() accepted an endpoint without an HTTP scheme")
	}
	if !strings.Contains(err.Error(), "invalid OTLP HTTP endpoint") {
		t.Fatalf("Init() returned an invalid endpoint error: %v", err)
	}
}
