package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// logFormat, logOutput and logLevel are bound to persistent CLI flags in
// root.go.
var (
	logFormat string
	logOutput string
	logLevel  string
)

// parseLogLevel maps a level name to a slog.Level. Matching is
// case-insensitive; an empty or unrecognized value yields slog.LevelInfo.
// It deliberately never fails: a bad --log-level must not stop the daemon
// from starting.
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// InitLogger configures the process-wide default slog logger.
// format: "text" (default) or "json"
// output: "stderr" (default) or a file path
// level: "debug"|"info"|"warn"|"error" (anything else means info)
func InitLogger(format, output, level string) error {
	// Determine writer
	var w *os.File
	switch output {
	case "", "stderr":
		w = os.Stderr
	default:
		f, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600) // #nosec G304 - path from --log-output CLI flag
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %w", output, err)
		}
		w = f
	}

	lvl := parseLogLevel(level)

	opts := &slog.HandlerOptions{
		Level: lvl,
	}

	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		// "text" or any unrecognized value defaults to text
		handler = slog.NewTextHandler(w, opts)
	}

	// Inject trace_id / span_id from context. Pass-through when no active
	// span is present in context, so non-traced runs are unchanged.
	handler = &traceContextHandler{inner: handler}

	slog.SetDefault(slog.New(handler))

	// Bridge existing log.Printf calls through slog
	slog.SetLogLoggerLevel(lvl)

	return nil
}

type traceContextHandler struct {
	inner slog.Handler
}

func (h *traceContextHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return h.inner.Handle(ctx, r)
	}
	r.AddAttrs(
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	)
	return h.inner.Handle(ctx, r)
}

func (h *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{inner: h.inner.WithGroup(name)}
}
