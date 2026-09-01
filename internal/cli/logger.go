package cli

import (
	"context"
	"fmt"
	"io"
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

	slog.SetDefault(slog.New(NewSlogHandler(w, format, level)))

	// Bridge existing log.Printf calls through slog
	slog.SetLogLoggerLevel(parseLogLevel(level))

	return nil
}

// LogFormat returns the log format the process was started with, so a caller
// that re-installs the default handler over a different writer keeps the same
// line shape.
func LogFormat() string { return logFormat }

// LogLevel returns the log level the process was started with, so a caller
// re-installing the handler over a different writer keeps the operator's
// --log-level rather than silently reverting to info.
func LogLevel() string { return logLevel }

// NewSlogHandler builds the handler shape InitLogger installs: JSON or text
// (text for any unrecognized format), wrapped so trace_id / span_id from the
// context are injected when an active span is present. The level comes from
// the process-wide --log-level flag, so a re-install over a different writer
// keeps the level the operator asked for.
func NewSlogHandler(w io.Writer, format, level string) slog.Handler {
	lvl := parseLogLevel(level)
	opts := &slog.HandlerOptions{
		Level: lvl,
	}

	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		handler = slog.NewTextHandler(w, opts)
	}

	// Inject trace_id / span_id from context. Pass-through when no active
	// span is present in context, so non-traced runs are unchanged.
	return &traceContextHandler{inner: handler}
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
