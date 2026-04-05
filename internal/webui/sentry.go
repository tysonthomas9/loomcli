package webui

import (
	"context"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
	sentryslog "github.com/getsentry/sentry-go/slog"
)

// FlushSentry waits up to the given timeout for queued Sentry events to be sent.
// Safe to call even if Sentry was never initialized.
func FlushSentry(timeout time.Duration) bool {
	return sentry.Flush(timeout)
}

// InitSentry initializes Sentry/GlitchTip error tracking and returns a wrapped
// logger that fans out ERROR+ records to Sentry alongside the primary handler.
// Returns the original logger unchanged if DSN is empty or init fails.
func InitSentry(logger *slog.Logger, dsn, release string) *slog.Logger {
	if dsn == "" {
		return logger
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Release:          release,
		AttachStacktrace: true,
	})
	if err != nil {
		logger.Warn("sentry init failed, error tracking disabled", "error", err)
		return logger
	}

	sentryHandler := sentryslog.Option{
		AddSource: true,
	}.NewSentryHandler(context.Background())

	handler := &fanoutHandler{
		primary:   logger.Handler(),
		secondary: sentryHandler,
	}

	logger.Info("sentry error tracking enabled", "dsn_host", dsn[:min(len(dsn), 30)]+"...")
	return slog.New(handler)
}

// fanoutHandler sends log records to both a primary and secondary handler.
// The primary handler's result determines Enabled and error propagation;
// the secondary handler (Sentry) is best-effort.
type fanoutHandler struct {
	primary   slog.Handler
	secondary slog.Handler
}

func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.primary.Enabled(ctx, level) || h.secondary.Enabled(ctx, level)
}

func (h *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.secondary.Enabled(ctx, r.Level) {
		_ = h.secondary.Handle(ctx, r.Clone())
	}
	if h.primary.Enabled(ctx, r.Level) {
		return h.primary.Handle(ctx, r)
	}
	return nil
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &fanoutHandler{
		primary:   h.primary.WithAttrs(attrs),
		secondary: h.secondary.WithAttrs(attrs),
	}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	return &fanoutHandler{
		primary:   h.primary.WithGroup(name),
		secondary: h.secondary.WithGroup(name),
	}
}
