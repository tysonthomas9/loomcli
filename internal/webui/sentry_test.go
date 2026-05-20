package webui

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestInitSentryBranches(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if got := InitSentry(logger, "", ""); got != logger {
		t.Fatal("empty DSN should return original logger")
	}
	if got := InitSentry(logger, "://bad-dsn", "test-release"); got != logger {
		t.Fatal("invalid DSN should return original logger")
	}
	wrapped := InitSentry(logger, "https://public@example.com/1", "test-release")
	if wrapped == logger {
		t.Fatal("valid DSN should return wrapped logger")
	}
	wrapped.Error("captured test error")
	_ = FlushSentry(time.Millisecond)
}

func TestFanoutHandlerDelegatesPrimaryAndSecondary(t *testing.T) {
	primary := &recordingHandler{enabled: map[slog.Level]bool{slog.LevelInfo: true}}
	secondary := &recordingHandler{enabled: map[slog.Level]bool{slog.LevelError: true}}
	handler := &fanoutHandler{primary: primary, secondary: secondary}
	ctx := context.Background()

	if !handler.Enabled(ctx, slog.LevelInfo) || !handler.Enabled(ctx, slog.LevelError) {
		t.Fatal("fanout handler should be enabled when either child is enabled")
	}
	if handler.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("fanout handler should be disabled when both children are disabled")
	}
	if err := handler.Handle(ctx, slog.NewRecord(time.Now(), slog.LevelError, "boom", 0)); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if primary.handled != 0 || secondary.handled != 1 {
		t.Fatalf("error record handled primary=%d secondary=%d", primary.handled, secondary.handled)
	}
	if err := handler.Handle(ctx, slog.NewRecord(time.Now(), slog.LevelInfo, "info", 0)); err != nil {
		t.Fatalf("Handle info error: %v", err)
	}
	if primary.handled != 1 || secondary.handled != 1 {
		t.Fatalf("info record handled primary=%d secondary=%d", primary.handled, secondary.handled)
	}
	if _, ok := handler.WithAttrs([]slog.Attr{slog.String("k", "v")}).(*fanoutHandler); !ok {
		t.Fatal("WithAttrs should preserve fanout handler")
	}
	if _, ok := handler.WithGroup("g").(*fanoutHandler); !ok {
		t.Fatal("WithGroup should preserve fanout handler")
	}
}

type recordingHandler struct {
	enabled map[slog.Level]bool
	handled int
}

func (h *recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return h.enabled[level]
}

func (h *recordingHandler) Handle(context.Context, slog.Record) error {
	h.handled++
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *recordingHandler) WithGroup(string) slog.Handler {
	return h
}
