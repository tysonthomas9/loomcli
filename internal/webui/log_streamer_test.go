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

// TestNewLogStreamer_Success verifies that NewLogStreamer creates a streamer
// for a valid file and that it can be cleaned up.
func TestNewLogStreamer_Success(t *testing.T) {
	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(fp, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ls, err := NewLogStreamer(fp)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	if ls == nil {
		t.Fatal("NewLogStreamer() returned nil")
	}
	if err := ls.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// TestNewLogStreamer_InvalidDirectory verifies that NewLogStreamer returns an
// error when the parent directory does not exist.
func TestNewLogStreamer_InvalidDirectory(t *testing.T) {
	_, err := NewLogStreamer("/nonexistent/dir/test.log")
	if err == nil {
		t.Fatal("NewLogStreamer() expected error for invalid directory, got nil")
	}
	if !strings.Contains(err.Error(), "failed to watch directory") {
		t.Errorf("error = %q, want error containing 'failed to watch directory'", err.Error())
	}
}

// TestNewLogStreamer_Close verifies that Close releases resources without error.
func TestNewLogStreamer_Close(t *testing.T) {
	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(fp, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ls, err := NewLogStreamer(fp)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}

	if err := ls.Close(); err != nil {
		t.Errorf("first Close() error = %v", err)
	}
}

// logSSETestClient wraps a real HTTP connection for log SSE streaming.
type logSSETestClient struct {
	resp    *http.Response
	scanner *bufio.Scanner
}

// logSSEEvent represents a parsed SSE event from the log stream.
type logSSEEvent struct {
	ID    string
	Event string
	Data  string
}

// readLogSSEEvent reads the next SSE event from the log stream.
func (c *logSSETestClient) readLogSSEEvent(timeout time.Duration) (*logSSEEvent, error) {
	done := make(chan *logSSEEvent, 1)
	errCh := make(chan error, 1)

	go func() {
		evt := &logSSEEvent{}
		for c.scanner.Scan() {
			line := c.scanner.Text()
			if line == "" {
				if evt.Event != "" || evt.Data != "" || evt.ID != "" {
					done <- evt
					return
				}
				continue
			}
			if strings.HasPrefix(line, ":") {
				// Comment (heartbeat)
				continue
			}
			if strings.HasPrefix(line, "retry:") {
				continue
			}
			if strings.HasPrefix(line, "id: ") {
				evt.ID = strings.TrimPrefix(line, "id: ")
			} else if strings.HasPrefix(line, "event: ") {
				evt.Event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				evt.Data = strings.TrimPrefix(line, "data: ")
			}
		}
		if err := c.scanner.Err(); err != nil {
			errCh <- err
		} else {
			errCh <- fmt.Errorf("stream ended unexpectedly")
		}
	}()

	select {
	case evt := <-done:
		return evt, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for SSE event after %v", timeout)
	}
}

func (c *logSSETestClient) close() {
	c.resp.Body.Close()
}

// setupLogStreamEnv creates a temp HOME with .loom/logs/ structure and a log file.
// Returns the resolved tmpHome and the log file path.
func setupLogStreamEnv(t *testing.T, content string) (string, string) {
	t.Helper()
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	logDir := filepath.Join(tmpHome, ".loom", "logs", "agents")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "stream-test.log")
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	return tmpHome, logFile
}

// newLogStreamServer creates an httptest.Server that serves the LogStreamer.Stream().
// Returns server, LogStreamer, and a cancel func for the streaming context.
func newLogStreamServer(t *testing.T, logFile string, startLine int64) (*httptest.Server, *LogStreamer, context.CancelFunc) {
	t.Helper()

	ls, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		_ = ls.Stream(ctx, w, startLine)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		cancel()
		server.Close()
		ls.Close()
	})

	return server, ls, cancel
}

// connectLogStream opens a GET to the stream endpoint.
func connectLogStream(t *testing.T, serverURL string) *logSSETestClient {
	t.Helper()

	client := &http.Client{Timeout: 0}
	resp, err := client.Get(serverURL + "/stream")
	if err != nil {
		t.Fatalf("failed to connect to stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	return &logSSETestClient{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
	}
}

// TestLogStreamer_Stream_InitialContent verifies that Stream sends initial file
// content as SSE log-line events with correct headers and JSON format.
func TestLogStreamer_Stream_InitialContent(t *testing.T) {
	_, logFile := setupLogStreamEnv(t, "line one\nline two\nline three\n")

	server, _, _ := newLogStreamServer(t, logFile, 1)
	client := connectLogStream(t, server.URL)
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
		evt, err := client.readLogSSEEvent(3 * time.Second)
		if err != nil {
			t.Fatalf("event %d: failed to read: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("event %d: event = %q, want %q", i, evt.Event, "log-line")
		}
		if evt.ID == "" {
			t.Errorf("event %d: missing id", i)
		}

		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("event %d: failed to parse JSON: %v", i, err)
		}
		if payload.Line != expected {
			t.Errorf("event %d: line = %q, want %q", i, payload.Line, expected)
		}
		if payload.LineNumber != int64(i+1) {
			t.Errorf("event %d: line_number = %d, want %d", i, payload.LineNumber, i+1)
		}
		if payload.Timestamp == "" {
			t.Errorf("event %d: missing timestamp", i)
		}
		// Verify timestamp is RFC3339
		if _, err := time.Parse(time.RFC3339, payload.Timestamp); err != nil {
			t.Errorf("event %d: timestamp %q not RFC3339: %v", i, payload.Timestamp, err)
		}
	}
}

// TestLogStreamer_Stream_StartLine verifies that startLine parameter skips earlier lines.
func TestLogStreamer_Stream_StartLine(t *testing.T) {
	content := ""
	for i := 1; i <= 10; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	_, logFile := setupLogStreamEnv(t, content)

	server, _, _ := newLogStreamServer(t, logFile, 5)
	client := connectLogStream(t, server.URL)
	defer client.close()

	// Should receive lines 5-10 (6 events)
	for i := 5; i <= 10; i++ {
		evt, err := client.readLogSSEEvent(3 * time.Second)
		if err != nil {
			t.Fatalf("line %d: failed to read: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("line %d: event = %q, want %q", i, evt.Event, "log-line")
		}
		var payload LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			t.Fatalf("line %d: failed to parse: %v", i, err)
		}
		expected := fmt.Sprintf("line %d", i)
		if payload.Line != expected {
			t.Errorf("line %d: got %q, want %q", i, payload.Line, expected)
		}
		if payload.LineNumber != int64(i) {
			t.Errorf("line %d: line_number = %d, want %d", i, payload.LineNumber, i)
		}
	}
}

// TestLogStreamer_Stream_NewLinesViaFsnotify verifies that appending to the file
// triggers new SSE events after the debounce interval.
func TestLogStreamer_Stream_NewLinesViaFsnotify(t *testing.T) {
	_, logFile := setupLogStreamEnv(t, "initial line\n")

	server, _, _ := newLogStreamServer(t, logFile, 1)
	client := connectLogStream(t, server.URL)
	defer client.close()

	// Read initial line
	evt, err := client.readLogSSEEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read initial event: %v", err)
	}
	var payload LogLinePayload
	if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
		t.Fatalf("failed to parse initial event: %v", err)
	}
	if payload.Line != "initial line" {
		t.Errorf("initial: got %q, want %q", payload.Line, "initial line")
	}

	// Append new lines (triggers fsnotify Write event)
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open file for append: %v", err)
	}
	fmt.Fprintln(f, "new line one")
	fmt.Fprintln(f, "new line two")
	f.Close()

	// Wait for debounce + fsnotify propagation
	expectedNew := []string{"new line one", "new line two"}
	for i, expected := range expectedNew {
		evt, err := client.readLogSSEEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("new event %d: failed to read: %v", i, err)
		}
		if evt.Event != "log-line" {
			t.Errorf("new event %d: event = %q, want %q", i, evt.Event, "log-line")
		}
		var p LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &p); err != nil {
			t.Fatalf("new event %d: failed to parse: %v", i, err)
		}
		if p.Line != expected {
			t.Errorf("new event %d: got %q, want %q", i, p.Line, expected)
		}
		if p.LineNumber != int64(i+2) { // initial was line 1
			t.Errorf("new event %d: line_number = %d, want %d", i, p.LineNumber, i+2)
		}
	}
}

// TestLogStreamer_Stream_FileTruncation verifies that truncating a file
// sends a "truncated" event.
func TestLogStreamer_Stream_FileTruncation(t *testing.T) {
	_, logFile := setupLogStreamEnv(t, "line one\nline two\nline three\n")

	server, _, _ := newLogStreamServer(t, logFile, 1)
	client := connectLogStream(t, server.URL)
	defer client.close()

	// Read initial events
	for i := 0; i < 3; i++ {
		_, err := client.readLogSSEEvent(3 * time.Second)
		if err != nil {
			t.Fatalf("initial event %d: %v", i, err)
		}
	}

	// Append and confirm a new line to ensure the streamer is in its
	// steady-state event loop and has recorded the correct fileSize.
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open file for append: %v", err)
	}
	fmt.Fprintln(f, "sync line")
	f.Close()

	evt, err := client.readLogSSEEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read sync event: %v", err)
	}
	if evt.Event != "log-line" {
		t.Fatalf("expected sync log-line event, got %q", evt.Event)
	}

	// Now truncate the file (write shorter content than current size)
	if err := os.WriteFile(logFile, []byte("short\n"), 0o644); err != nil {
		t.Fatalf("failed to truncate file: %v", err)
	}

	// Should receive a "truncated" event
	evt, err = client.readLogSSEEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read truncated event: %v", err)
	}
	if evt.Event != "truncated" {
		t.Errorf("event = %q, want %q", evt.Event, "truncated")
	}
	if evt.Data != "{}" {
		t.Errorf("data = %q, want %q", evt.Data, "{}")
	}
}

// TestLogStreamer_Stream_ContextCancellation verifies that cancelling the
// context causes Stream() to return.
func TestLogStreamer_Stream_ContextCancellation(t *testing.T) {
	_, logFile := setupLogStreamEnv(t, "test line\n")

	ls, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer ls.Close()

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		errCh <- ls.Stream(ctx, w, 1)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := connectLogStream(t, server.URL)
	defer client.close()

	// Read the initial event to ensure streaming started
	_, err = client.readLogSSEEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read initial event: %v", err)
	}

	// Cancel the context
	cancel()

	// Stream should return with context.Canceled
	select {
	case streamErr := <-errCh:
		if streamErr != context.Canceled {
			t.Errorf("Stream() error = %v, want context.Canceled", streamErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream() did not return after context cancellation")
	}
}

// TestLogStreamer_Stream_NonFlusher verifies that Stream returns
// "streaming unsupported" when the writer doesn't implement http.Flusher.
func TestLogStreamer_Stream_NonFlusher(t *testing.T) {
	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(fp, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ls, err := NewLogStreamer(fp)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer ls.Close()

	// httptest.ResponseRecorder implements http.Flusher, so we need a custom writer.
	w := &nonFlusherWriter{header: make(http.Header)}
	err = ls.Stream(context.Background(), w, 1)
	if err == nil {
		t.Fatal("Stream() expected error for non-flusher, got nil")
	}
	if !strings.Contains(err.Error(), "streaming unsupported") {
		t.Errorf("error = %q, want 'streaming unsupported'", err.Error())
	}
}

// nonFlusherWriter is an http.ResponseWriter that does NOT implement http.Flusher.
type nonFlusherWriter struct {
	header http.Header
}

func (w *nonFlusherWriter) Header() http.Header        { return w.header }
func (w *nonFlusherWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nonFlusherWriter) WriteHeader(int)             {}

// TestLogStreamer_Stream_EmptyFile verifies streaming an empty file sends
// no initial events, and new lines arrive after append.
func TestLogStreamer_Stream_EmptyFile(t *testing.T) {
	_, logFile := setupLogStreamEnv(t, "")

	server, _, _ := newLogStreamServer(t, logFile, 1)

	// Connect - the stream should start but send no log-line events
	httpClient := &http.Client{Timeout: 0}
	resp, err := httpClient.Get(server.URL + "/stream")
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	client := &logSSETestClient{resp: resp, scanner: scanner}

	// Append a line to the empty file
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open file: %v", err)
	}
	fmt.Fprintln(f, "first line")
	f.Close()

	// Should now receive the new line
	evt, err := client.readLogSSEEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if evt.Event != "log-line" {
		t.Errorf("event = %q, want %q", evt.Event, "log-line")
	}
	var payload LogLinePayload
	if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if payload.Line != "first line" {
		t.Errorf("line = %q, want %q", payload.Line, "first line")
	}
	if payload.LineNumber != 1 {
		t.Errorf("line_number = %d, want 1", payload.LineNumber)
	}
}

// TestLogStreamer_Stream_WatcherClosed verifies that closing the watcher
// causes Stream to return nil.
func TestLogStreamer_Stream_WatcherClosed(t *testing.T) {
	_, logFile := setupLogStreamEnv(t, "test\n")

	ls, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}

	errCh := make(chan error, 1)
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		errCh <- ls.Stream(ctx, w, 1)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := connectLogStream(t, server.URL)
	defer client.close()

	// Read initial event
	_, err = client.readLogSSEEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read initial event: %v", err)
	}

	// Close the watcher externally
	time.Sleep(100 * time.Millisecond) // Let stream enter the select loop
	ls.Close()

	// Stream should return nil (watcher.Events closed)
	select {
	case streamErr := <-errCh:
		if streamErr != nil {
			t.Errorf("Stream() error = %v, want nil", streamErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream() did not return after watcher close")
	}
}

// TestLogStreamer_ReadNewLines_Concurrent verifies that concurrent file appends
// produce monotonically increasing line numbers with no data races.
// This test writes in batches with synchronization to ensure deterministic behavior.
func TestLogStreamer_ReadNewLines_Concurrent(t *testing.T) {
	_, logFile := setupLogStreamEnv(t, "")

	server, _, _ := newLogStreamServer(t, logFile, 1)
	client := connectLogStream(t, server.URL)
	defer client.close()

	// Write lines concurrently using a shared file handle with mutex
	// to avoid interleaved writes.
	const totalLines = 20
	var mu sync.Mutex
	var wg sync.WaitGroup

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open file: %v", err)
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				mu.Lock()
				fmt.Fprintf(f, "writer-%d-line-%d\n", writerID, j)
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
	f.Close()

	// Read events with generous timeout and verify monotonic line numbers
	var prevLineNum int64
	eventsRead := 0
	deadline := time.After(10 * time.Second)
	for eventsRead < totalLines {
		evt, err := client.readLogSSEEvent(3 * time.Second)
		if err != nil {
			// Check if we've hit the overall deadline
			select {
			case <-deadline:
				t.Logf("read %d/%d events before deadline", eventsRead, totalLines)
				break
			default:
			}
			break
		}
		if evt.Event != "log-line" {
			continue
		}
		var p LogLinePayload
		if err := json.Unmarshal([]byte(evt.Data), &p); err != nil {
			t.Fatalf("event %d: failed to parse: %v", eventsRead, err)
		}
		if p.LineNumber <= prevLineNum {
			t.Errorf("event %d: line_number %d not greater than previous %d", eventsRead, p.LineNumber, prevLineNum)
		}
		prevLineNum = p.LineNumber
		eventsRead++
	}

	if eventsRead < totalLines {
		t.Errorf("expected %d events, got %d", totalLines, eventsRead)
	}
}

// TestSendLogLine_JSONFormat verifies the SSE wire format of sendLogLine.
func TestSendLogLine_JSONFormat(t *testing.T) {
	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(fp, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ls, err := NewLogStreamer(fp)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer ls.Close()

	rec := httptest.NewRecorder()
	ls.sendLogLine(rec, rec, "hello world", 42)

	output := rec.Body.String()

	// Verify SSE wire format: id, event, data, blank line
	if !strings.Contains(output, "event: log-line\n") {
		t.Errorf("output missing 'event: log-line': %q", output)
	}
	if !strings.Contains(output, "id: ") {
		t.Errorf("output missing 'id: ': %q", output)
	}
	if !strings.Contains(output, "data: ") {
		t.Errorf("output missing 'data: ': %q", output)
	}
	// Must end with double newline
	if !strings.HasSuffix(output, "\n\n") {
		t.Errorf("output not terminated with double newline: %q", output)
	}

	// Parse the data JSON
	dataIdx := strings.Index(output, "data: ")
	if dataIdx < 0 {
		t.Fatal("could not find data: in output")
	}
	dataLine := output[dataIdx+len("data: "):]
	dataLine = strings.TrimRight(dataLine, "\n")
	var payload LogLinePayload
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("failed to parse data JSON: %v (raw: %q)", err, dataLine)
	}
	if payload.Line != "hello world" {
		t.Errorf("line = %q, want %q", payload.Line, "hello world")
	}
	if payload.LineNumber != 42 {
		t.Errorf("line_number = %d, want 42", payload.LineNumber)
	}
	if payload.Timestamp == "" {
		t.Error("timestamp should not be empty")
	}
	if _, err := time.Parse(time.RFC3339, payload.Timestamp); err != nil {
		t.Errorf("timestamp %q not RFC3339: %v", payload.Timestamp, err)
	}
}

// TestSendTruncatedEvent_Format verifies the SSE wire format of sendTruncatedEvent.
func TestSendTruncatedEvent_Format(t *testing.T) {
	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(fp, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ls, err := NewLogStreamer(fp)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer ls.Close()

	rec := httptest.NewRecorder()
	ls.sendTruncatedEvent(rec, rec)

	output := rec.Body.String()

	if !strings.Contains(output, "event: truncated\n") {
		t.Errorf("output missing 'event: truncated': %q", output)
	}
	if !strings.Contains(output, "data: {}\n") {
		t.Errorf("output missing 'data: {}': %q", output)
	}
	if !strings.Contains(output, "id: ") {
		t.Errorf("output missing 'id: ': %q", output)
	}
	if !strings.HasSuffix(output, "\n\n") {
		t.Errorf("output not terminated with double newline: %q", output)
	}
}

// TestNewLogStreamerFixed_Alias verifies that NewLogStreamerFixed is an alias
// for NewLogStreamer and works identically.
func TestNewLogStreamerFixed_Alias(t *testing.T) {
	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(fp, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	ls, err := NewLogStreamerFixed(fp)
	if err != nil {
		t.Fatalf("NewLogStreamerFixed() error = %v", err)
	}
	if ls == nil {
		t.Fatal("NewLogStreamerFixed() returned nil")
	}
	if err := ls.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
