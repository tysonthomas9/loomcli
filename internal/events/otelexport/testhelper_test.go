package otelexport

import (
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newTestExporter creates an Exporter wired to in-memory exporters for assertions.
func newTestExporter(t *testing.T) (*Exporter, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()

	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(spanExporter),
	)

	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricReader),
	)

	cfg := Config{
		Enabled:     true,
		ServiceName: "test",
		SampleRate:  1.0,
	}

	exp, err := New(cfg,
		WithTracerProvider(tp),
		WithMeterProvider(mp),
	)
	if err != nil {
		t.Fatalf("newTestExporter: %v", err)
	}

	return exp, spanExporter, metricReader
}
