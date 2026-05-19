package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRespondHelpersValidatorsAndServiceError(t *testing.T) {
	rr := httptest.NewRecorder()
	respondJSON(rr, http.StatusCreated, map[string]string{"ok": "yes"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("respondJSON status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	var decoded map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil || decoded["ok"] != "yes" {
		t.Fatalf("json body = %#v, err=%v", decoded, err)
	}

	errRR := httptest.NewRecorder()
	respondError(errRR, http.StatusTeapot, "short")
	if errRR.Code != http.StatusTeapot || !strings.Contains(errRR.Body.String(), `"error":"short"`) {
		t.Fatalf("respondError = %d %s", errRR.Code, errRR.Body.String())
	}

	serviceRR := httptest.NewRecorder()
	WriteServiceError(serviceRR, context.Canceled)
	if serviceRR.Code != http.StatusInternalServerError {
		t.Fatalf("WriteServiceError status = %d", serviceRR.Code)
	}

	for _, path := range []string{"secrets.pem", "/tmp/.env", "id_ed25519"} {
		if !isDeniedPath(path) {
			t.Fatalf("isDeniedPath(%q) = false", path)
		}
	}
	for _, path := range []string{"src/main.go", "nested/file.txt"} {
		if isDeniedPath(path) {
			t.Fatalf("isDeniedPath(%q) = true", path)
		}
	}
	for _, path := range []string{"src/main.go", "docs/readme.md"} {
		if !validateDiffPath(path) {
			t.Fatalf("validateDiffPath(%q) = false", path)
		}
	}
	for _, path := range []string{"", "/abs/path", "..", "../escape"} {
		if validateDiffPath(path) {
			t.Fatalf("validateDiffPath(%q) = true", path)
		}
	}
}

func TestSentryNoopAndFanoutHandler(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if got := InitSentry(base, "", "test-release"); got != base {
		t.Fatal("empty DSN should return original logger")
	}
	_ = FlushSentry(time.Millisecond)

	primary := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	secondary := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	h := &fanoutHandler{primary: primary, secondary: secondary}
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("fanout should be enabled when secondary is enabled")
	}
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "info message", 0)
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle info: %v", err)
	}
	if !strings.Contains(buf.String(), "info message") {
		t.Fatalf("secondary did not receive record: %q", buf.String())
	}
	grouped := h.WithAttrs([]slog.Attr{slog.String("k", "v")}).WithGroup("grp")
	if err := grouped.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelWarn, "warn message", 0)); err != nil {
		t.Fatalf("Handle warn: %v", err)
	}
	if !strings.Contains(buf.String(), "warn message") {
		t.Fatalf("primary did not receive warn record: %q", buf.String())
	}
}
