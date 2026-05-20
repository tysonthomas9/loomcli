package log

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type noFlushWriter struct {
	header http.Header
}

func (w *noFlushWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *noFlushWriter) WriteHeader(int)             {}

func TestLogStreamerAdditionalInternalBranches(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	logDir := filepath.Join(runtimeDir, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	logFile := filepath.Join(logDir, "branch.log")
	if err := os.WriteFile(logFile, []byte("alpha"), 0600); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	if got := clampOffset(-10, 5); got != 0 {
		t.Fatalf("negative clamp = %d", got)
	}
	if got := clampOffset(99, 5); got != 5 {
		t.Fatalf("oversized clamp = %d", got)
	}

	streamer, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer: %v", err)
	}
	defer func() { _ = streamer.Close() }()

	if err := streamer.Stream(context.Background(), &noFlushWriter{}, 0); err == nil || !strings.Contains(err.Error(), "streaming unsupported") {
		t.Fatalf("Stream without flusher err = %v", err)
	}

	rec := httptest.NewRecorder()
	writeSSEHeaders(rec)
	if rec.Header().Get("Content-Type") != "text/event-stream" || rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("SSE headers = %#v", rec.Header())
	}

	streamer.sendLogChunk(rec, rec, nil, 1)
	if rec.Body.Len() != 0 {
		t.Fatalf("empty chunk wrote %q", rec.Body.String())
	}
	streamer.sendTruncatedEvent(rec, rec)
	if !strings.Contains(rec.Body.String(), "event: truncated") {
		t.Fatalf("truncated event output = %q", rec.Body.String())
	}

	file, err := os.Open(logFile)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	offset, err := streamer.readAndEmitChunks(ctx, rec, rec, file, 2)
	_ = file.Close()
	if !errors.Is(err, context.Canceled) || offset != 2 {
		t.Fatalf("readAndEmitChunks canceled offset=%d err=%v", offset, err)
	}

	streamer.mu.Lock()
	streamer.currentSize = int64(len("alpha"))
	streamer.mu.Unlock()
	file, current, err := streamer.openAndCheckTruncation()
	if err != nil || file != nil || current != 0 {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("same-size openAndCheckTruncation file=%v current=%d err=%v", file, current, err)
	}

	if err := os.WriteFile(logFile, []byte("alpha\nbeta"), 0600); err != nil {
		t.Fatalf("append log content: %v", err)
	}
	rec.Body.Reset()
	if err := streamer.readNewChunks(rec, rec); err != nil {
		t.Fatalf("readNewChunks append: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "event: log-chunk") {
		t.Fatalf("append read output = %q", rec.Body.String())
	}
	streamer.mu.Lock()
	if streamer.currentSize != int64(len("alpha\nbeta")) {
		t.Fatalf("currentSize = %d", streamer.currentSize)
	}
	streamer.mu.Unlock()

	rec.Body.Reset()
	streamer.handleDebouncedRead(rec, rec)
	if rec.Body.Len() != 0 {
		t.Fatalf("no new data wrote %q", rec.Body.String())
	}

	if err := os.WriteFile(logFile, []byte("x"), 0600); err != nil {
		t.Fatalf("truncate log content: %v", err)
	}
	streamer.handleDebouncedRead(rec, rec)
	if !strings.Contains(rec.Body.String(), "event: truncated") {
		t.Fatalf("truncate output = %q", rec.Body.String())
	}
}

func TestNewLogStreamerMissingDirectory(t *testing.T) {
	_, err := NewLogStreamer(filepath.Join(t.TempDir(), "missing", "test.log"))
	if err == nil || !strings.Contains(err.Error(), "failed to watch directory") {
		t.Fatalf("NewLogStreamer missing dir err = %v", err)
	}
}
