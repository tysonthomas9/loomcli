package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- NewLogStreamer tests ---

// TestNewLogStreamer_Success verifies that creating a LogStreamer for an existing
// file in a valid directory succeeds and returns a non-nil streamer.
func TestNewLogStreamer_Success(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(logFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	if s == nil {
		t.Fatal("NewLogStreamer() returned nil")
	}
	if s.logFilePath != logFile {
		t.Errorf("logFilePath = %q, want %q", s.logFilePath, logFile)
	}
	if s.watcher == nil {
		t.Error("watcher should be non-nil")
	}
}

// TestNewLogStreamer_InvalidDirectory verifies that creating a LogStreamer for a
// path whose parent directory does not exist returns an error.
func TestNewLogStreamer_InvalidDirectory(t *testing.T) {
	s, err := NewLogStreamer("/nonexistent/parent/dir/test.log")
	if err == nil {
		s.Close()
		t.Fatal("NewLogStreamer() expected error for nonexistent directory, got nil")
	}
	if !strings.Contains(err.Error(), "failed to watch directory") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to watch directory")
	}
}

// TestNewLogStreamer_Close verifies that Close() releases resources without error.
func TestNewLogStreamer_Close(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(logFile, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}

	if err := s.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// --- Stream() tests using httptest ---

// logSSETestServer creates an httptest server that serves Stream() for a given LogStreamer.
// Returns the server and a cleanup function. The caller should cancel ctx to stop the stream.
func logSSETestServer(t *testing.T, s *LogStreamer, startLine int64) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = s.Stream(r.Context(), w, startLine)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// connectStreamSSE connects to an SSE server and returns a client for reading events.
func connectStreamSSE(t *testing.T, ctx context.Context, serverURL string) *sseTestClient {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	return &sseTestClient{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
	}
}

// TestLogStreamer_Stream_InitialContent tests that Stream() sends existing file
// lines as log-line SSE events with correct headers and JSON payloads.
func TestLogStreamer_Stream_InitialContent(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	server := logSSETestServer(t, s, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := connectStreamSSE(t, ctx, server.URL)
	defer client.close()

	// Verify SSE headers
	ct := client.resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	cc := client.resp.Header.Get("Cache-Control")
	if cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
	}
	conn := client.resp.Header.Get("Connection")
	if conn != "keep-alive" {
		t.Errorf("Connection = %q, want %q", conn, "keep-alive")
	}
	xab := client.resp.Header.Get("X-Accel-Buffering")
	if xab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want %q", xab, "no")
	}

	// Read 3 log-line events
	expectedLines := []string{"line one", "line two", "line three"}
	for i, expected := range expectedLines {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("event %d: failed to read: %v", i+1, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("event %d: type = %q, want %q", i+1, evt.Event, "log-line")
		}
		if evt.ID == "" {
			t.Errorf("event %d: expected non-empty ID", i+1)
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("event %d: failed to parse payload: %v", i+1, err)
		}
		if payload.Line != expected {
			t.Errorf("event %d: line = %q, want %q", i+1, payload.Line, expected)
		}
		if payload.LineNumber != int64(i+1) {
			t.Errorf("event %d: line_number = %d, want %d", i+1, payload.LineNumber, i+1)
		}
		if payload.Timestamp == "" {
			t.Errorf("event %d: expected non-empty timestamp", i+1)
		}
		// Verify timestamp is RFC3339
		if _, err := time.Parse(time.RFC3339, payload.Timestamp); err != nil {
			t.Errorf("event %d: timestamp %q is not RFC3339: %v", i+1, payload.Timestamp, err)
		}
	}

	cancel()
}

// TestLogStreamer_Stream_StartLine tests that Stream() with a startLine skips
// earlier lines and only sends lines from startLine onward.
func TestLogStreamer_Stream_StartLine(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(logFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	// Start from line 7
	server := logSSETestServer(t, s, 7)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := connectStreamSSE(t, ctx, server.URL)
	defer client.close()

	// Should receive lines 7, 8, 9, 10
	for i := 7; i <= 10; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read event for line %d: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("line %d: event type = %q, want %q", i, evt.Event, "log-line")
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("line %d: failed to parse payload: %v", i, err)
		}
		if payload.LineNumber != int64(i) {
			t.Errorf("line %d: line_number = %d, want %d", i, payload.LineNumber, i)
		}
		expected := fmt.Sprintf("line %d", i)
		if payload.Line != expected {
			t.Errorf("line %d: line = %q, want %q", i, payload.Line, expected)
		}
	}

	cancel()
}

// TestLogStreamer_Stream_NewLinesViaFsnotify tests that appending new lines to
// the file after the initial content is streamed triggers new SSE events.
func TestLogStreamer_Stream_NewLinesViaFsnotify(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte("initial line\n"), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	server := logSSETestServer(t, s, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := connectStreamSSE(t, ctx, server.URL)
	defer client.close()

	// Read initial line
	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read initial event: %v", err)
	}
	if evt.Event != "log-line" {
		t.Errorf("initial event type = %q, want %q", evt.Event, "log-line")
	}

	// Wait for watcher setup
	time.Sleep(100 * time.Millisecond)

	// Append new lines
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open for append: %v", err)
	}
	if _, err := f.WriteString("new line 2\nnew line 3\n"); err != nil {
		f.Close()
		t.Fatalf("failed to append: %v", err)
	}
	f.Close()

	// Read new events (account for debounce)
	for i := 2; i <= 3; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read event for line %d: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("line %d: event type = %q, want %q", i, evt.Event, "log-line")
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("line %d: failed to parse payload: %v", i, err)
		}
		if payload.LineNumber != int64(i) {
			t.Errorf("line %d: line_number = %d, want %d", i, payload.LineNumber, i)
		}
	}

	cancel()
}

// TestLogStreamer_Stream_FileTruncation tests that truncating the file sends a
// "truncated" SSE event and resets the streamer's position.
func TestLogStreamer_Stream_FileTruncation(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte("long line one\nlong line two\nlong line three\n"), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	server := logSSETestServer(t, s, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := connectStreamSSE(t, ctx, server.URL)
	defer client.close()

	// Read 3 initial events
	for i := 1; i <= 3; i++ {
		_, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read initial event %d: %v", i, err)
		}
	}

	// Wait for watcher setup
	time.Sleep(100 * time.Millisecond)

	// Truncate file (write shorter content)
	if err := os.WriteFile(logFile, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("failed to truncate: %v", err)
	}

	// Should receive a truncated event
	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read truncated event: %v", err)
	}
	if evt.Event != "truncated" {
		t.Errorf("event type = %q, want %q", evt.Event, "truncated")
	}
	if evt.Data != "{}" {
		t.Errorf("truncated event data = %q, want %q", evt.Data, "{}")
	}

	cancel()
}

// TestLogStreamer_Stream_ContextCancellation tests that canceling the context
// causes Stream() to return.
func TestLogStreamer_Stream_ContextCancellation(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte("line\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())

	streamErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamErr <- s.Stream(ctx, w, 1)
	}))
	t.Cleanup(server.Close)

	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		client := &http.Client{Timeout: 0}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Give the stream time to start
	time.Sleep(200 * time.Millisecond)

	// Cancel context
	cancel()

	select {
	case err := <-streamErr:
		if err != nil && err != context.Canceled {
			t.Errorf("Stream() returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream() did not return after context cancellation")
	}
}

// TestLogStreamer_Stream_NonFlusher tests that Stream() returns "streaming unsupported"
// when given a ResponseWriter that doesn't implement http.Flusher.
func TestLogStreamer_Stream_NonFlusher(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(logFile, []byte("line\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	// Use a writer that does NOT implement http.Flusher
	w := &nonFlusherWriter{header: http.Header{}}
	ctx := context.Background()

	err = s.Stream(ctx, w, 1)
	if err == nil {
		t.Fatal("Stream() expected error for non-flusher, got nil")
	}
	if err.Error() != "streaming unsupported" {
		t.Errorf("error = %q, want %q", err.Error(), "streaming unsupported")
	}
}

// nonFlusherWriter is an http.ResponseWriter that does NOT implement http.Flusher.
type nonFlusherWriter struct {
	header http.Header
}

func (w *nonFlusherWriter) Header() http.Header         { return w.header }
func (w *nonFlusherWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nonFlusherWriter) WriteHeader(int)             {}

// TestLogStreamer_Stream_EmptyFile tests that streaming an empty file sends no
// initial events, and new lines appear when appended.
func TestLogStreamer_Stream_EmptyFile(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte{}, 0o644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	server := logSSETestServer(t, s, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := connectStreamSSE(t, ctx, server.URL)
	defer client.close()

	// Wait for the stream to be fully set up (no initial events to read)
	time.Sleep(200 * time.Millisecond)

	// Append a line
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open for append: %v", err)
	}
	if _, err := f.WriteString("first line\n"); err != nil {
		f.Close()
		t.Fatalf("failed to append: %v", err)
	}
	f.Close()

	// Should receive the new line
	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if evt.Event != "log-line" {
		t.Errorf("event type = %q, want %q", evt.Event, "log-line")
	}

	var payload LogLinePayload
	if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload.Line != "first line" {
		t.Errorf("line = %q, want %q", payload.Line, "first line")
	}
	if payload.LineNumber != 1 {
		t.Errorf("line_number = %d, want 1", payload.LineNumber)
	}

	cancel()
}

// TestLogStreamer_Stream_WatcherClosed tests that closing the watcher externally
// causes Stream() to exit cleanly.
func TestLogStreamer_Stream_WatcherClosed(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte("line\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}

	streamErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamErr <- s.Stream(r.Context(), w, 1)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		client := &http.Client{Timeout: 0}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Wait for stream to reach the event loop
	time.Sleep(300 * time.Millisecond)

	// Close the watcher externally
	s.watcher.Close()

	select {
	case err := <-streamErr:
		// Stream should return nil (watcher closed) or context.Canceled (HTTP cleanup).
		// Both are acceptable since the close/cancel order is non-deterministic.
		if err != nil && err != context.Canceled {
			t.Errorf("Stream() returned %v, want nil or context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream() did not return after watcher close")
	}
}

// TestLogStreamer_ReadNewLines_Concurrent tests that concurrent file appends
// with simultaneous streaming don't cause data races.
func TestLogStreamer_ReadNewLines_Concurrent(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte("initial\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	server := logSSETestServer(t, s, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := connectStreamSSE(t, ctx, server.URL)
	defer client.close()

	// Read initial event
	_, err = client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read initial event: %v", err)
	}

	// Wait for watcher setup
	time.Sleep(100 * time.Millisecond)

	// Launch concurrent writers
	const numWriters = 5
	const linesPerWriter = 3
	var wg sync.WaitGroup
	wg.Add(numWriters)

	for w := 0; w < numWriters; w++ {
		go func(writerID int) {
			defer wg.Done()
			f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return
			}
			defer f.Close()
			for l := 0; l < linesPerWriter; l++ {
				fmt.Fprintf(f, "writer-%d-line-%d\n", writerID, l)
			}
		}(w)
	}

	wg.Wait()

	// Read all appended events (total = numWriters * linesPerWriter = 15)
	totalExpected := numWriters * linesPerWriter
	received := 0
	var prevLineNum int64
	deadline := time.After(10 * time.Second)

	for received < totalExpected {
		select {
		case <-deadline:
			t.Fatalf("timed out: received %d of %d events", received, totalExpected)
		default:
		}

		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("failed to read event %d: %v", received+1, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("event %d: type = %q, want %q", received+1, evt.Event, "log-line")
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("event %d: failed to parse payload: %v", received+1, err)
		}

		// Verify monotonic line numbers
		if payload.LineNumber <= prevLineNum {
			t.Errorf("event %d: line_number %d not greater than previous %d",
				received+1, payload.LineNumber, prevLineNum)
		}
		prevLineNum = payload.LineNumber
		received++
	}

	cancel()
}

// --- sendLogLine and sendTruncatedEvent format tests ---

// TestSendLogLine_JSONFormat tests that sendLogLine produces correct SSE wire format
// with proper event type, ID, and JSON payload.
func TestSendLogLine_JSONFormat(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(logFile, []byte("line\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	recorder := httptest.NewRecorder()
	flusher := recorder

	s.sendLogLine(recorder, flusher, "test line content", 42)

	output := recorder.Body.String()

	// Verify it contains id, event, and data fields
	if !strings.Contains(output, "id: ") {
		t.Error("output missing 'id: ' field")
	}
	if !strings.Contains(output, "event: log-line") {
		t.Error("output missing 'event: log-line'")
	}
	if !strings.Contains(output, "data: ") {
		t.Error("output missing 'data: ' field")
	}

	// Extract and parse the JSON data
	dataLine := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if dataLine == "" {
		t.Fatal("could not find data line in output")
	}

	var payload LogLinePayload
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("failed to parse JSON payload: %v", err)
	}
	if payload.Line != "test line content" {
		t.Errorf("line = %q, want %q", payload.Line, "test line content")
	}
	if payload.LineNumber != 42 {
		t.Errorf("line_number = %d, want 42", payload.LineNumber)
	}
	if payload.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	if _, err := time.Parse(time.RFC3339, payload.Timestamp); err != nil {
		t.Errorf("timestamp %q is not RFC3339: %v", payload.Timestamp, err)
	}

	// Verify output ends with double newline (SSE event delimiter)
	if !strings.HasSuffix(output, "\n\n") {
		t.Errorf("output should end with double newline, got %q", output[len(output)-4:])
	}
}

// TestSendTruncatedEvent_Format tests that sendTruncatedEvent produces the correct
// SSE wire format with "truncated" event type and empty JSON data.
func TestSendTruncatedEvent_Format(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(logFile, []byte("line\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	recorder := httptest.NewRecorder()
	flusher := recorder

	s.sendTruncatedEvent(recorder, flusher)

	output := recorder.Body.String()

	if !strings.Contains(output, "id: ") {
		t.Error("output missing 'id: ' field")
	}
	if !strings.Contains(output, "event: truncated") {
		t.Error("output missing 'event: truncated'")
	}
	if !strings.Contains(output, "data: {}") {
		t.Error("output missing 'data: {}'")
	}
	if !strings.HasSuffix(output, "\n\n") {
		t.Errorf("output should end with double newline, got %q", output[len(output)-4:])
	}
}

// TestLogStreamer_Stream_MonotonicEventIDs tests that event IDs are strictly increasing
// across multiple events in a single stream.
func TestLogStreamer_Stream_MonotonicEventIDs(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	var lines []string
	for i := 1; i <= 5; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(logFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer s.Close()

	server := logSSETestServer(t, s, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := connectStreamSSE(t, ctx, server.URL)
	defer client.close()

	var prevID int64
	for i := 1; i <= 5; i++ {
		evt, err := client.readEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("event %d: failed to read: %v", i, err)
		}

		var id int64
		if _, err := fmt.Sscanf(evt.ID, "%d", &id); err != nil {
			t.Fatalf("event %d: failed to parse ID %q: %v", i, evt.ID, err)
		}
		if i > 1 && id <= prevID {
			t.Errorf("event %d: ID %d not greater than previous %d", i, id, prevID)
		}
		prevID = id
	}

	cancel()
}

// TestNewLogStreamerFixed_IsAlias verifies NewLogStreamerFixed is an alias for NewLogStreamer.
func TestNewLogStreamerFixed_IsAlias(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(logFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamerFixed(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamerFixed() error = %v", err)
	}
	defer s.Close()

	if s == nil {
		t.Fatal("NewLogStreamerFixed() returned nil")
	}
	if s.logFilePath != logFile {
		t.Errorf("logFilePath = %q, want %q", s.logFilePath, logFile)
	}
}
