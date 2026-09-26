package log

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type testSSEEvent struct {
	ID    string
	Event string
	Data  string
}

type streamSSEClient struct {
	resp    *http.Response
	scanner *bufio.Scanner
}

func (c *streamSSEClient) close() {
	if c.resp != nil && c.resp.Body != nil {
		_ = c.resp.Body.Close()
	}
}

func (c *streamSSEClient) readEvent(timeout time.Duration) (testSSEEvent, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return testSSEEvent{}, context.DeadlineExceeded
		}

		event := testSSEEvent{}
		hasFields := false
		for c.scanner.Scan() {
			line := c.scanner.Text()
			if line == "" {
				break
			}
			switch {
			case strings.HasPrefix(line, "id: "):
				event.ID = strings.TrimPrefix(line, "id: ")
				hasFields = true
			case strings.HasPrefix(line, "event: "):
				event.Event = strings.TrimPrefix(line, "event: ")
				hasFields = true
			case strings.HasPrefix(line, "data: "):
				event.Data = strings.TrimPrefix(line, "data: ")
				hasFields = true
			}
		}
		if err := c.scanner.Err(); err != nil {
			return testSSEEvent{}, err
		}
		if hasFields {
			return event, nil
		}
	}
}

func connectTestSSE(t *testing.T, ctx context.Context, url string) *streamSSEClient {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	return &streamSSEClient{resp: resp, scanner: bufio.NewScanner(resp.Body)}
}

func decodeChunkB64(t *testing.T, payload LogChunkPayload) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(payload.ChunkBase64)
	if err != nil {
		t.Fatalf("failed to decode chunk base64: %v", err)
	}
	return string(b)
}

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
	defer func() { _ = s.Close() }()

	if s.watcher == nil {
		t.Fatal("watcher should be non-nil")
	}
}

func TestLogStreamer_Stream_InitialContentRawChunk(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	content := "line one\nline two\rline three\n"
	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = s.Stream(r.Context(), w, 0)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := connectTestSSE(t, ctx, server.URL)
	defer client.close()

	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if evt.Event != "log-chunk" {
		t.Fatalf("event type = %q, want log-chunk", evt.Event)
	}

	var payload LogChunkPayload
	if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	decoded := decodeChunkB64(t, payload)
	if decoded != content {
		t.Fatalf("decoded chunk mismatch: got %q want %q", decoded, content)
	}
	if payload.ByteOffset != int64(len(content)) {
		t.Fatalf("byte_offset = %d, want %d", payload.ByteOffset, len(content))
	}
}

func TestLogStreamer_Stream_SinceBytes(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	content := "abcdef"
	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = s.Stream(r.Context(), w, 3)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := connectTestSSE(t, ctx, server.URL)
	defer client.close()

	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if evt.Event != "log-chunk" {
		t.Fatalf("event type = %q, want log-chunk", evt.Event)
	}

	var payload LogChunkPayload
	if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if got := decodeChunkB64(t, payload); got != "def" {
		t.Fatalf("decoded chunk = %q, want %q", got, "def")
	}
	if payload.ByteOffset != 6 {
		t.Fatalf("byte_offset = %d, want 6", payload.ByteOffset)
	}
}

func TestLogStreamer_Stream_AppendAndTruncate(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte(""), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = s.Stream(r.Context(), w, 0)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := connectTestSSE(t, ctx, server.URL)
	defer client.close()

	// Ensure watcher loop started
	time.Sleep(100 * time.Millisecond)

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("failed to open file for append: %v", err)
	}
	if _, err := f.WriteString("append-one\n"); err != nil {
		_ = f.Close()
		t.Fatalf("failed to append: %v", err)
	}
	_ = f.Close()

	evt, err := client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read append event: %v", err)
	}
	if evt.Event != "log-chunk" {
		t.Fatalf("append event type = %q, want log-chunk", evt.Event)
	}
	var payload LogChunkPayload
	if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
		t.Fatalf("failed to parse append payload: %v", err)
	}
	if !strings.Contains(decodeChunkB64(t, payload), "append-one\n") {
		t.Fatalf("append payload missing expected content")
	}

	if err := os.WriteFile(logFile, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("failed to truncate file: %v", err)
	}

	evt, err = client.readEvent(5 * time.Second)
	if err != nil {
		t.Fatalf("failed to read post-truncate event: %v", err)
	}
	if evt.Event != "truncated" {
		t.Fatalf("event type after truncate = %q, want truncated", evt.Event)
	}
}

func TestSendLogChunk_JSONFormat(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")
	if err := os.WriteFile(logFile, []byte("line\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	recorder := httptest.NewRecorder()
	sw, err := realtime.NewWriter(recorder)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	if err := s.sendLogChunk(sw, []byte("raw-data"), 42); err != nil {
		t.Fatalf("sendLogChunk() error = %v", err)
	}
	output := recorder.Body.String()
	if !strings.Contains(output, "event: log-chunk") {
		t.Fatalf("expected log-chunk event in output")
	}

	dataLine := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if dataLine == "" {
		t.Fatalf("missing data line in output")
	}

	var payload LogChunkPayload
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload.ByteOffset != 42 {
		t.Fatalf("byte_offset = %d, want 42", payload.ByteOffset)
	}
	if got := decodeChunkB64(t, payload); got != "raw-data" {
		t.Fatalf("decoded chunk = %q, want %q", got, "raw-data")
	}
}

func TestLogStreamer_StreamStopsOnWriteFailure(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	logDir := filepath.Join(tmpHome, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}
	logFile := filepath.Join(logDir, "test.log")
	if err := os.WriteFile(logFile, []byte("content"), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	s, err := NewLogStreamer(logFile)
	if err != nil {
		t.Fatalf("NewLogStreamer() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	writeErr := errors.New("client disconnected")
	rw := &failAtWriteResponseWriter{failAt: 2, err: writeErr}
	if err := s.Stream(context.Background(), rw, 0); !errors.Is(err, writeErr) {
		t.Fatalf("Stream() error = %v, want %v", err, writeErr)
	}
	if rw.flushes != 1 {
		t.Fatalf("flush count = %d, want retry frame only", rw.flushes)
	}
}

type failAtWriteResponseWriter struct {
	header  http.Header
	failAt  int
	writes  int
	flushes int
	err     error
}

func (w *failAtWriteResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failAtWriteResponseWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

func (*failAtWriteResponseWriter) WriteHeader(int) {}

func (w *failAtWriteResponseWriter) Flush() {
	w.flushes++
}
