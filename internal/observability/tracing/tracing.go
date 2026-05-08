// Package tracing wires the OpenTelemetry TracerProvider, propagators, and
// OTLP exporter for loomcli (loom-cli, loom-serve, loom-daemon, loom-agent).
//
// This package is the canonical SDK init. The pre-existing
// internal/events/otelexport package, which subscribes to the event bus and
// emits loom.task / loom.agent.lifecycle spans, can either share this
// package's provider (preferred — call Init first then pass Provider() into
// otelexport.New via WithTracerProvider) or build its own (legacy path).
//
// Disabled mode (no endpoint configured) installs a no-op TracerProvider but
// still registers the W3C trace-context propagator so inbound traceparent
// headers parse and re-emit. Cost is one map lookup per request.
package tracing

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// BootstrapContext returns a context carrying the inherited traceparent from
// LOOM_TRACE_PARENT, when set. This lets a parent process (daemon, embedded
// fleet-db spawner) inject its active span via env so this process's
// startup-time spans inherit the parent's trace ID.
//
// Returns the input ctx unchanged when the env var is unset or malformed.
// The W3C TraceContext propagator is used (must already be installed
// globally, which Init does even in disabled mode).
func BootstrapContext(ctx context.Context) context.Context {
	tp := os.Getenv("LOOM_TRACE_PARENT")
	if tp == "" {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(
		ctx, propagation.MapCarrier{"traceparent": tp},
	)
}

// TraceparentFromContext serializes the active span context as a W3C
// traceparent header value, suitable for env-var propagation to a child
// process. Returns empty when there is no active span.
//
// Pairs with BootstrapContext on the consumer side: a parent loom process
// calls this to capture its current span, then injects the result via the
// LOOM_TRACE_PARENT env var when spawning a subprocess.
func TraceparentFromContext(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

// Config controls SDK initialization. See
// docs/observability/tracing-contract.md for the canonical service names and
// sampling defaults.
type Config struct {
	// ServiceName is the value of the service.name resource attribute. Required
	// when Enabled is true. Use one of: loom-cli, loom-serve, loom-daemon,
	// loom-agent.
	ServiceName string
	// ServiceVersion populates service.version. Typically the build commit.
	ServiceVersion string
	// Environment populates deployment.environment ("dev", "staging", "prod").
	Environment string
	// Endpoint is the OTLP HTTP receiver, e.g. "localhost:4318" or
	// "http://collector:4318". Empty disables export.
	Endpoint string
	// Insecure forces HTTP (no TLS). Default true; set false for prod TLS.
	Insecure bool
	// SampleRate is the parent-based ratio sampler argument when no override is
	// provided. 0 disables, 1.0 = always-on. Default 1.0.
	SampleRate float64
	// AlwaysOn skips ratio sampling and uses AlwaysOn (overrides SampleRate).
	AlwaysOn bool
	// Sync uses SimpleSpanProcessor instead of BatchSpanProcessor. Set to true
	// for short-lived CLI/agent processes that may exit via os.Exit (skipping
	// defers and the batched flush). Long-running services (serve, daemon)
	// should leave this false to amortize OTLP export overhead.
	Sync bool
}

// Enabled reports whether export is configured. A disabled config still wires
// the propagator so traceparent flows through.
func (c Config) Enabled() bool { return strings.TrimSpace(c.Endpoint) != "" }

// Shutdown flushes pending spans and tears down the provider. Safe to call
// multiple times. Returns nil when tracing was disabled.
type Shutdown func(ctx context.Context) error

// Provider returns the active TracerProvider, useful for handing to
// otelexport.New(WithTracerProvider(...)). Nil when tracing is disabled
// (callers should treat nil as "use the global no-op").
type Provider func() *sdktrace.TracerProvider

// Init installs the global TracerProvider and TextMapPropagator. The returned
// Shutdown must be called on process exit; pass a context with a short
// timeout (5s is typical) so a slow collector can't block shutdown.
//
// The returned Provider can be passed into otelexport.New so the event-driven
// span emitter shares the same provider — otherwise spans land in two
// providers and the trace tree fragments.
func Init(ctx context.Context, cfg Config) (Shutdown, Provider, error) {
	// Always install the W3C propagator pair so traceparent headers parse and
	// re-emit even when this hop is not exporting.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if !cfg.Enabled() {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		return func(context.Context) error { return nil },
			func() *sdktrace.TracerProvider { return nil },
			nil
	}

	if cfg.ServiceName == "" {
		return nil, nil, fmt.Errorf("tracing: ServiceName required when Endpoint is set")
	}

	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(strDefault(cfg.ServiceVersion, "unknown")),
			semconv.ServiceNamespace("loom"),
			semconv.DeploymentEnvironment(strDefault(cfg.Environment, "dev")),
		),
		sdkresource.WithFromEnv(),
		sdkresource.WithHost(),
		sdkresource.WithProcessRuntimeName(),
		sdkresource.WithProcessRuntimeVersion(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("tracing: build resource: %w", err)
	}

	expOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(stripScheme(cfg.Endpoint)),
		otlptracehttp.WithTimeout(10 * time.Second),
	}
	if cfg.Insecure || strings.HasPrefix(cfg.Endpoint, "http://") {
		expOpts = append(expOpts, otlptracehttp.WithInsecure())
	}
	exp, err := otlptracehttp.New(ctx, expOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("tracing: create OTLP exporter: %w", err)
	}

	var tpOpts []sdktrace.TracerProviderOption
	if cfg.Sync {
		// Synchronous: each span.End() blocks until export completes. Use for
		// CLI/agent processes that may os.Exit() and skip defers.
		tpOpts = append(tpOpts, sdktrace.WithSyncer(exp))
	} else {
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exp,
			sdktrace.WithBatchTimeout(2*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		))
	}
	tpOpts = append(tpOpts,
		sdktrace.WithResource(res),
		sdktrace.WithSampler(buildSampler(cfg)),
	)
	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)

	return func(ctx context.Context) error {
			_ = tp.ForceFlush(ctx)
			return tp.Shutdown(ctx)
		}, func() *sdktrace.TracerProvider {
			return tp
		}, nil
}

// Tracer returns a tracer named for the calling package. Pass a stable
// instrumentation name (typically the package import path).
func Tracer(name string) trace.Tracer {
	return otel.GetTracerProvider().Tracer(name)
}

func buildSampler(cfg Config) sdktrace.Sampler {
	if cfg.AlwaysOn {
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	rate := cfg.SampleRate
	if rate <= 0 {
		rate = 1.0
	}
	if rate > 1.0 {
		rate = 1.0
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))
}

func stripScheme(s string) string {
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimRight(s, "/")
}

func strDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
